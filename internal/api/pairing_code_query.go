package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const pairingCodeGenerateMaxRetrys = 8

type pairingCodeQueryRequest struct {
	EnsureGenerated *bool `json:"ensure_generated"`
}

type pairingCodeQueryAuth struct {
	UserID      string
	DeviceID    string
	WriterEpoch int64
}

type pairingCodeQueryResponse struct {
	PairingCode          string `json:"pairing_code"`
	PairingCodeVersion   int64  `json:"pairing_code_version"`
	PairingCodeUpdatedAt string `json:"pairing_code_updated_at"`
	IsNewlyGenerated     bool   `json:"is_newly_generated"`
}

type pairingCodeQuerySnapshot struct {
	PairingCode        string
	PairingCodeVersion int64
	PairingCodeAt      time.Time
	WriterDeviceID     string
	WriterEpoch        int64
	DeviceAuthorized   bool
}

type pairingCodeQueryTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) pairingCodeQueryHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	ensureGenerated, err := parsePairingCodeQueryBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}
	auth, err := resolvePairingCodeQueryAuth(r.Context(), s.db, r)
	if err != nil {
		return err
	}

	response, err := persistPairingCodeQuery(r.Context(), s.db, auth, ensureGenerated)
	if err != nil {
		if mappedErr, ok := mapPairingCodeQueryPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parsePairingCodeQueryBody(reader io.Reader) (bool, error) {
	var body pairingCodeQueryRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return false, err
	}
	if body.EnsureGenerated == nil {
		return false, invalidArgument("ensure_generated is required")
	}
	return *body.EnsureGenerated, nil
}

func resolvePairingCodeQueryAuth(ctx context.Context, db HealthChecker, r *http.Request) (pairingCodeQueryAuth, error) {
	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return pairingCodeQueryAuth{}, err
	}
	auth, err := resolveMobileAuthContext(ctx, db, header, true)
	if err != nil {
		return pairingCodeQueryAuth{}, err
	}
	return pairingCodeQueryAuth{
		UserID:      auth.UserID,
		DeviceID:    auth.DeviceID,
		WriterEpoch: auth.WriterEpoch,
	}, nil
}

func unauthorizedDevice() error {
	return apperrors.New(http.StatusUnauthorized, unauthorizedDeviceCode, "unauthorized device")
}

func persistPairingCodeQuery(
	ctx context.Context,
	db HealthChecker,
	auth pairingCodeQueryAuth,
	ensureGenerated bool,
) (pairingCodeQueryResponse, error) {
	txDB, ok := db.(pairingCodeQueryTxDB)
	if !ok {
		return pairingCodeQueryResponse{}, errors.New("database transaction is not available")
	}

	var response pairingCodeQueryResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		snapshot, err := loadPairingCodeQuerySnapshot(ctx, tx, auth.UserID, auth.DeviceID)
		if err != nil {
			return err
		}

		if !snapshot.DeviceAuthorized {
			return unauthorizedDevice()
		}
		if snapshot.WriterDeviceID != auth.DeviceID || snapshot.WriterEpoch != auth.WriterEpoch {
			return apperrors.New(http.StatusConflict, staleWriterRejectedCode, "device_id or writer_epoch does not match current writer")
		}

		pairingCode := strings.TrimSpace(snapshot.PairingCode)
		pairingCodeVersion := snapshot.PairingCodeVersion
		pairingCodeUpdatedAt := snapshot.PairingCodeAt
		isNewlyGenerated := false

		if pairingCode == "" {
			if !ensureGenerated {
				return invalidArgument("pairing_code is not initialized; ensure_generated must be true")
			}

			newCode, newVersion, newUpdatedAt, err := generateInitialPairingCode(ctx, tx, auth.UserID)
			if err != nil {
				return err
			}
			pairingCode = newCode
			pairingCodeVersion = newVersion
			pairingCodeUpdatedAt = newUpdatedAt
			isNewlyGenerated = true
		}

		response = pairingCodeQueryResponse{
			PairingCode:          pairingCode,
			PairingCodeVersion:   pairingCodeVersion,
			PairingCodeUpdatedAt: pairingCodeUpdatedAt.UTC().Format(time.RFC3339),
			IsNewlyGenerated:     isNewlyGenerated,
		}
		return nil
	})
	if err != nil {
		return pairingCodeQueryResponse{}, err
	}

	return response, nil
}

func loadPairingCodeQuerySnapshot(ctx context.Context, tx pgx.Tx, userID, deviceID string) (pairingCodeQuerySnapshot, error) {
	const query = `
SELECT u.pairing_code,
       u.pairing_code_version,
       u.pairing_code_updated_at,
       COALESCE(u.writer_device_id::text, ''),
       u.writer_epoch,
       EXISTS (
         SELECT 1
           FROM devices d
          WHERE d.user_id = u.user_id
            AND d.device_id = $2
            AND d.status = 'ACTIVE'
       ) AS device_authorized
  FROM users u
 WHERE u.user_id = $1
 FOR UPDATE
`

	var snapshot pairingCodeQuerySnapshot
	if err := tx.QueryRow(ctx, query, userID, deviceID).Scan(
		&snapshot.PairingCode,
		&snapshot.PairingCodeVersion,
		&snapshot.PairingCodeAt,
		&snapshot.WriterDeviceID,
		&snapshot.WriterEpoch,
		&snapshot.DeviceAuthorized,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pairingCodeQuerySnapshot{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
		}
		return pairingCodeQuerySnapshot{}, err
	}

	snapshot.WriterDeviceID = strings.ToLower(strings.TrimSpace(snapshot.WriterDeviceID))
	return snapshot, nil
}

func generateInitialPairingCode(ctx context.Context, tx pgx.Tx, userID string) (string, int64, time.Time, error) {
	for attempt := 0; attempt < pairingCodeGenerateMaxRetrys; attempt++ {
		code, err := randomPairingCode()
		if err != nil {
			return "", 0, time.Time{}, err
		}

		const query = `
UPDATE users
   SET pairing_code = $2,
       pairing_code_updated_at = now(),
       updated_at = now()
 WHERE user_id = $1
   AND pairing_code = ''
 RETURNING pairing_code, pairing_code_version, pairing_code_updated_at
`
		var updatedCode string
		var version int64
		var updatedAt time.Time
		err = tx.QueryRow(ctx, query, userID, code).Scan(&updatedCode, &version, &updatedAt)
		if err == nil {
			return updatedCode, version, updatedAt, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			const currentQuery = `
SELECT pairing_code, pairing_code_version, pairing_code_updated_at
  FROM users
 WHERE user_id = $1
`
			err = tx.QueryRow(ctx, currentQuery, userID).Scan(&updatedCode, &version, &updatedAt)
			if err == nil && strings.TrimSpace(updatedCode) != "" {
				return updatedCode, version, updatedAt, nil
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return "", 0, time.Time{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
			}
			if err != nil {
				return "", 0, time.Time{}, err
			}
			continue
		}

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_users_pairing_code" {
			continue
		}
		return "", 0, time.Time{}, err
	}

	return "", 0, time.Time{}, apperrors.New(http.StatusConflict, pairingCodeGenerateFailedCode, "pairing code generate failed")
}

func randomPairingCode() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	value := (uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])) % 100000000
	return fmt.Sprintf("%08d", value), nil
}

func mapPairingCodeQueryPersistenceError(err error) (error, bool) {
	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil, false
	}

	if pgErr.Code == "P0001" {
		if mappedErr, ok := mapRuleErrorKeyToAPIError(extractErrorKey(pgErr.Message)); ok {
			return mappedErr, true
		}
	}

	return nil, false
}
