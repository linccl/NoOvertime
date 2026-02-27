package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
)

const recoveryCodeLength = 16

var generateRecoveryCode = randomRecoveryCode

type recoveryCodeGenerateRequest struct {
	RequireFirstTime *bool `json:"require_first_time"`
}

type recoveryCodeGenerateResponse struct {
	RecoveryCode       string `json:"recovery_code"`
	RecoveryCodeMasked string `json:"recovery_code_masked"`
	ShownOnce          bool   `json:"shown_once"`
	UpdatedAt          string `json:"updated_at"`
}

type recoveryCodeGenerateSnapshot struct {
	RecoveryCodeHash string
	WriterDeviceID   string
	WriterEpoch      int64
	DeviceAuthorized bool
}

type recoveryCodeGenerateTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) recoveryCodeGenerateHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	requireFirstTime, err := parseRecoveryCodeGenerateBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}
	auth, err := parseDeviceAuthHeaders(r)
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkRecoveryVerifyRateLimit(auth.UserID, fingerprint); err != nil {
		return err
	}

	response, err := persistRecoveryCodeGenerate(r.Context(), s.db, auth, requireFirstTime)
	if err != nil {
		if mappedErr, ok := mapRecoveryCodeGeneratePersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseRecoveryCodeGenerateBody(reader io.Reader) (bool, error) {
	var body recoveryCodeGenerateRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return false, err
	}
	if body.RequireFirstTime == nil {
		return false, invalidArgument("require_first_time is required")
	}
	if !*body.RequireFirstTime {
		return false, invalidArgument("require_first_time must be true")
	}
	return true, nil
}

func persistRecoveryCodeGenerate(
	ctx context.Context,
	db HealthChecker,
	auth pairingCodeQueryAuth,
	requireFirstTime bool,
) (recoveryCodeGenerateResponse, error) {
	_ = requireFirstTime

	txDB, ok := db.(recoveryCodeGenerateTxDB)
	if !ok {
		return recoveryCodeGenerateResponse{}, errors.New("database transaction is not available")
	}

	var response recoveryCodeGenerateResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		snapshot, err := loadRecoveryCodeGenerateSnapshot(ctx, tx, auth.UserID, auth.DeviceID)
		if err != nil {
			return err
		}
		if !snapshot.DeviceAuthorized {
			return unauthorizedDevice()
		}
		if snapshot.WriterDeviceID != auth.DeviceID || snapshot.WriterEpoch != auth.WriterEpoch {
			return apperrors.New(http.StatusConflict, staleWriterRejectedCode, "device_id or writer_epoch does not match current writer")
		}
		if strings.TrimSpace(snapshot.RecoveryCodeHash) != "" {
			return apperrors.New(http.StatusConflict, recoveryCodeAlreadyInitializedCode, "recovery code already initialized")
		}

		recoveryCode, err := generateRecoveryCode()
		if err != nil {
			return err
		}
		updatedAt, err := updateRecoveryCodeHash(ctx, tx, auth.UserID, recoveryCode)
		if err != nil {
			return err
		}

		response = recoveryCodeGenerateResponse{
			RecoveryCode:       recoveryCode,
			RecoveryCodeMasked: maskRecoveryCode(recoveryCode),
			ShownOnce:          true,
			UpdatedAt:          updatedAt.UTC().Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return recoveryCodeGenerateResponse{}, err
	}

	return response, nil
}

func loadRecoveryCodeGenerateSnapshot(ctx context.Context, tx pgx.Tx, userID, deviceID string) (recoveryCodeGenerateSnapshot, error) {
	const query = `
SELECT u.recovery_code_hash,
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
	var snapshot recoveryCodeGenerateSnapshot
	if err := tx.QueryRow(ctx, query, userID, deviceID).Scan(
		&snapshot.RecoveryCodeHash,
		&snapshot.WriterDeviceID,
		&snapshot.WriterEpoch,
		&snapshot.DeviceAuthorized,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return recoveryCodeGenerateSnapshot{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
		}
		return recoveryCodeGenerateSnapshot{}, err
	}

	snapshot.WriterDeviceID = strings.ToLower(strings.TrimSpace(snapshot.WriterDeviceID))
	return snapshot, nil
}

func updateRecoveryCodeHash(ctx context.Context, tx pgx.Tx, userID, recoveryCode string) (time.Time, error) {
	const query = `
UPDATE users
   SET recovery_code_hash = crypt($2, gen_salt('bf')),
       updated_at = now()
 WHERE user_id = $1
 RETURNING updated_at
`
	var updatedAt time.Time
	if err := tx.QueryRow(ctx, query, userID, recoveryCode).Scan(&updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
		}
		return time.Time{}, err
	}
	return updatedAt, nil
}

func randomRecoveryCode() (string, error) {
	var chars [recoveryCodeLength]byte
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := 0; i < len(chars); i++ {
		var one [1]byte
		if _, err := rand.Read(one[:]); err != nil {
			return "", err
		}
		chars[i] = alphabet[int(one[0])%len(alphabet)]
	}
	return string(chars[:]), nil
}

func maskRecoveryCode(code string) string {
	if len(code) <= 8 {
		return code
	}
	return code[:4] + "********" + code[len(code)-4:]
}

func mapRecoveryCodeGeneratePersistenceError(err error) (error, bool) {
	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
