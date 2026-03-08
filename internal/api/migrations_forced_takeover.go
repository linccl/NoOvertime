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

const (
	migrationsTakeoverPath       = "/api/v1/migrations/takeover"
	migrationsForcedTakeoverPath = "/api/v1/migrations/forced-takeover"

	pairingCodeFormatInvalidCode = "PAIRING_CODE_FORMAT_INVALID"
	pairingCodeInvalidCode       = "PAIRING_CODE_INVALID"
	recoveryCodeInvalidCode      = "RECOVERY_CODE_INVALID"
)

type migrationForcedTakeoverResponse struct {
	MigrationRequestID string `json:"migration_request_id"`
	Status             string `json:"status"`
	Mode               string `json:"mode"`
	WriterDeviceID     string `json:"writer_device_id"`
	WriterEpoch        int64  `json:"writer_epoch"`
	CompletedAt        string `json:"completed_at"`
}

type migrationForcedTakeoverTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type forcedTakeoverUserSnapshot struct {
	UserID         string
	WriterDeviceID string
}

func (s *Server) migrationForcedTakeoverHandler(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}

	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}
	auth, err := resolveMobileAuthContext(r.Context(), s.db, header, true)
	if err != nil {
		return err
	}
	input, err := parseMigrationForcedTakeoverInputWithAuth(io.LimitReader(r.Body, migrationRequestBodyMaxBytes), auth)
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkRecoveryVerifyRateLimit(input.PairingCode, fingerprint); err != nil {
		return err
	}
	if err := s.checkMigrationRequestRateLimit(input.PairingCode, fingerprint); err != nil {
		return err
	}

	response, err := persistMigrationForcedTakeover(r.Context(), s.db, input)
	if err != nil {
		if mappedErr, ok := mapMigrationForcedTakeoverPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func persistMigrationForcedTakeover(ctx context.Context, db HealthChecker, input migrationForcedTakeoverInput) (migrationForcedTakeoverResponse, error) {
	txDB, ok := db.(migrationForcedTakeoverTxDB)
	if !ok {
		return migrationForcedTakeoverResponse{}, errors.New("database transaction is not available")
	}

	var response migrationForcedTakeoverResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		userSnapshot, err := loadForcedTakeoverUserByPairingCode(ctx, tx, input.PairingCode)
		if err != nil {
			return err
		}

		recoveryMatched, err := verifyRecoveryCode(ctx, tx, userSnapshot.UserID, input.RecoveryCode)
		if err != nil {
			return err
		}
		if !recoveryMatched {
			return apperrors.New(http.StatusConflict, recoveryCodeInvalidCode, "recovery code is invalid")
		}
		if userSnapshot.WriterDeviceID != "" && userSnapshot.WriterDeviceID == input.ToDeviceID {
			return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid")
		}

		var fromDeviceID any
		if userSnapshot.WriterDeviceID != "" {
			fromDeviceID = userSnapshot.WriterDeviceID
		}

		var migrationRequestID string
		expiresAt := time.Now().UTC().Add(30 * time.Minute)
		if err := tx.QueryRow(ctx, `
INSERT INTO migration_requests (
	user_id, from_device_id, to_device_id, mode, status, recovery_code_verified, expires_at
) VALUES (
	$1, $2, $3, 'FORCED', 'PENDING', TRUE, $4
)
RETURNING id
`, userSnapshot.UserID, fromDeviceID, input.ToDeviceID, expiresAt).Scan(&migrationRequestID); err != nil {
			return err
		}

		var writerEpoch int64
		if err := tx.QueryRow(ctx, `
UPDATE users
   SET writer_device_id = $1,
       writer_epoch = writer_epoch + 1,
       updated_at = now()
 WHERE user_id = $2
 RETURNING writer_epoch
`, input.ToDeviceID, userSnapshot.UserID).Scan(&writerEpoch); err != nil {
			return err
		}

		if userSnapshot.WriterDeviceID != "" && userSnapshot.WriterDeviceID != input.ToDeviceID {
			if _, err := tx.Exec(ctx, `
UPDATE devices
   SET status = 'REVOKED',
       revoked_at = now(),
       updated_at = now()
 WHERE user_id = $1
   AND device_id = $2
`, userSnapshot.UserID, userSnapshot.WriterDeviceID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
UPDATE devices
   SET status = 'ACTIVE',
       revoked_at = NULL,
       updated_at = now()
 WHERE user_id = $1
   AND device_id = $2
`, userSnapshot.UserID, input.ToDeviceID); err != nil {
			return err
		}
		if err := rotateActiveMobileTokensByUser(ctx, tx, userSnapshot.UserID); err != nil {
			return err
		}
		if err := bindMobileTokensByDevice(ctx, tx, userSnapshot.UserID, input.ToDeviceID, writerEpoch); err != nil {
			return err
		}

		var completedAt time.Time
		if err := tx.QueryRow(ctx, `
UPDATE migration_requests
   SET status = 'COMPLETED',
       updated_at = now()
 WHERE id = $1
 RETURNING updated_at
`, migrationRequestID).Scan(&completedAt); err != nil {
			return err
		}

		response = migrationForcedTakeoverResponse{
			MigrationRequestID: migrationRequestID,
			Status:             "COMPLETED",
			Mode:               "FORCED",
			WriterDeviceID:     input.ToDeviceID,
			WriterEpoch:        writerEpoch,
			CompletedAt:        completedAt.UTC().Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return migrationForcedTakeoverResponse{}, err
	}

	return response, nil
}

func loadForcedTakeoverUserByPairingCode(ctx context.Context, tx pgx.Tx, pairingCode string) (forcedTakeoverUserSnapshot, error) {
	const query = `
SELECT user_id, COALESCE(writer_device_id::text, '')
  FROM users
 WHERE pairing_code = $1
 FOR UPDATE
`

	var snapshot forcedTakeoverUserSnapshot
	if err := tx.QueryRow(ctx, query, pairingCode).Scan(&snapshot.UserID, &snapshot.WriterDeviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return forcedTakeoverUserSnapshot{}, apperrors.New(http.StatusConflict, pairingCodeInvalidCode, "pairing code is invalid")
		}
		return forcedTakeoverUserSnapshot{}, err
	}
	return snapshot, nil
}

func verifyRecoveryCode(ctx context.Context, tx pgx.Tx, userID, recoveryCode string) (bool, error) {
	const query = `
SELECT recovery_code_hash = crypt($2, recovery_code_hash)
  FROM users
 WHERE user_id = $1
 FOR UPDATE
`

	var matched bool
	if err := tx.QueryRow(ctx, query, userID, recoveryCode).Scan(&matched); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, apperrors.New(http.StatusConflict, pairingCodeInvalidCode, "pairing code is invalid")
		}
		return false, err
	}
	return matched, nil
}

func mapMigrationForcedTakeoverPersistenceError(err error) (error, bool) {
	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil, false
	}

	if pgErr.Code == "23514" && pgErr.ConstraintName == "ck_users_pairing_code_format" {
		return apperrors.New(http.StatusBadRequest, pairingCodeFormatInvalidCode, "pairing_code must be 8 digits"), true
	}
	if pgErr.Code == "23514" && pgErr.ConstraintName == "ck_migration_requests_device_distinct" {
		return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid"), true
	}

	if pgErr.Code == "23505" && pgErr.ConstraintName == "uk_migration_user_pending" {
		return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid"), true
	}

	if pgErr.Code == "23503" {
		switch pgErr.ConstraintName {
		case "fk_migration_requests_user", "fk_migration_requests_from_device", "fk_migration_requests_to_device":
			return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid"), true
		}
	}

	if pgErr.Code == "P0001" {
		errorKey := extractErrorKey(pgErr.Message)
		if errorKey == "MIGRATION_TRANSITION_INVALID" || errorKey == "MIGRATION_IMMUTABLE_FIELDS" || errorKey == "MIGRATION_SOURCE_MISMATCH" {
			return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid"), true
		}
		if mappedErr, ok := mapRuleErrorKeyToAPIError(errorKey); ok {
			return mappedErr, true
		}
	}

	return nil, false
}
