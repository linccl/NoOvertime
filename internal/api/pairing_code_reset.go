package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var allowedPairingResetReasons = map[string]struct{}{
	"USER_INITIATED": {},
}

type pairingCodeResetRequest struct {
	Reason string `json:"reason"`
}

type pairingCodeResetResponse struct {
	PairingCode          string `json:"pairing_code"`
	PairingCodeVersion   int64  `json:"pairing_code_version"`
	PairingCodeUpdatedAt string `json:"pairing_code_updated_at"`
	RevokedBindingsCount int64  `json:"revoked_bindings_count"`
}

type pairingCodeResetTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) pairingCodeResetHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	reason, err := parsePairingCodeResetBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}
	auth, err := parseDeviceAuthHeaders(r)
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkPairingResetRateLimit(auth.UserID, fingerprint); err != nil {
		return err
	}

	response, err := persistPairingCodeReset(r.Context(), s.db, auth, reason)
	if err != nil {
		if mappedErr, ok := mapPairingCodeResetPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parsePairingCodeResetBody(reader io.Reader) (string, error) {
	var body pairingCodeResetRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return "", err
	}
	return parseRequiredEnum("reason", body.Reason, allowedPairingResetReasons)
}

func persistPairingCodeReset(
	ctx context.Context,
	db HealthChecker,
	auth pairingCodeQueryAuth,
	reason string,
) (pairingCodeResetResponse, error) {
	_ = reason

	txDB, ok := db.(pairingCodeResetTxDB)
	if !ok {
		return pairingCodeResetResponse{}, errors.New("database transaction is not available")
	}

	var response pairingCodeResetResponse
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

		revokedCount, err := countActiveWebBindings(ctx, tx, auth.UserID)
		if err != nil {
			return err
		}

		pairingCode, version, updatedAt, err := rotatePairingCode(ctx, tx, auth.UserID)
		if err != nil {
			return err
		}

		response = pairingCodeResetResponse{
			PairingCode:          pairingCode,
			PairingCodeVersion:   version,
			PairingCodeUpdatedAt: updatedAt.UTC().Format(time.RFC3339),
			RevokedBindingsCount: revokedCount,
		}
		return nil
	})
	if err != nil {
		return pairingCodeResetResponse{}, err
	}

	return response, nil
}

func countActiveWebBindings(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	const query = `
SELECT count(*)
  FROM web_read_bindings
 WHERE user_id = $1
   AND status = 'ACTIVE'
`
	var count int64
	if err := tx.QueryRow(ctx, query, userID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func rotatePairingCode(ctx context.Context, tx pgx.Tx, userID string) (string, int64, time.Time, error) {
	for attempt := 0; attempt < pairingCodeGenerateMaxRetrys; attempt++ {
		newCode, err := randomPairingCode()
		if err != nil {
			return "", 0, time.Time{}, err
		}

		if _, err := tx.Exec(ctx, `SELECT rotate_pairing_code($1, $2)`, userID, newCode); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_users_pairing_code" {
				continue
			}
			return "", 0, time.Time{}, err
		}

		const query = `
SELECT pairing_code, pairing_code_version, pairing_code_updated_at
  FROM users
 WHERE user_id = $1
`
		var pairingCode string
		var version int64
		var updatedAt time.Time
		if err := tx.QueryRow(ctx, query, userID).Scan(&pairingCode, &version, &updatedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", 0, time.Time{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
			}
			return "", 0, time.Time{}, err
		}
		return pairingCode, version, updatedAt, nil
	}

	return "", 0, time.Time{}, apperrors.New(http.StatusConflict, pairingCodeGenerateFailedCode, "pairing code generate failed")
}

func mapPairingCodeResetPersistenceError(err error) (error, bool) {
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
