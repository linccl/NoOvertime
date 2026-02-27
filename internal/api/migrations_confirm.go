package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const migrationsConfirmPathPattern = "/api/v1/migrations/{migration_request_id}/confirm"

type migrationConfirmResponse struct {
	MigrationRequestID string `json:"migration_request_id"`
	Status             string `json:"status"`
	WriterDeviceID     string `json:"writer_device_id"`
	WriterEpoch        int64  `json:"writer_epoch"`
	RevokedDeviceID    string `json:"revoked_device_id"`
	CompletedAt        string `json:"completed_at"`
}

type migrationConfirmTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type migrationConfirmSnapshot struct {
	UserID       string
	FromDeviceID string
	ToDeviceID   string
	Status       string
	ExpiresAt    time.Time
}

type userWriterState struct {
	WriterDeviceID string
	WriterEpoch    int64
}

func (s *Server) migrationConfirmHandler(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}

	migrationRequestID := migrationConfirmPathID(r)
	input, err := parseMigrationConfirmInput(migrationRequestID, io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, input.OperatorDeviceID)
	if err := s.checkMigrationConfirmRateLimit(input.MigrationRequestID, fingerprint); err != nil {
		return err
	}

	response, err := persistMigrationConfirm(r.Context(), s.db, input)
	if err != nil {
		if mappedErr, ok := mapMigrationConfirmPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func migrationConfirmPathID(r *http.Request) string {
	pathValue := strings.TrimSpace(r.PathValue("migration_request_id"))
	if pathValue != "" {
		return pathValue
	}
	path := strings.TrimSpace(r.URL.Path)
	prefix := "/api/v1/migrations/"
	suffix := "/confirm"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return strings.Trim(inner, "/")
}

func persistMigrationConfirm(ctx context.Context, db HealthChecker, input migrationConfirmInput) (migrationConfirmResponse, error) {
	txDB, ok := db.(migrationConfirmTxDB)
	if !ok {
		return migrationConfirmResponse{}, errors.New("database transaction is not available")
	}

	var response migrationConfirmResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		snapshot, err := loadMigrationConfirmSnapshot(ctx, tx, input.MigrationRequestID)
		if err != nil {
			return err
		}
		if snapshot.Status != "PENDING" {
			return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration request is not in PENDING state")
		}
		if time.Now().UTC().After(snapshot.ExpiresAt.UTC()) {
			return apperrors.New(http.StatusConflict, migrationExpiredCode, "migration request is expired")
		}
		if snapshot.FromDeviceID != input.OperatorDeviceID {
			return apperrors.New(http.StatusConflict, migrationSourceMismatchCode, "operator_device_id must match migration source device")
		}

		writerState, err := loadUserWriterState(ctx, tx, snapshot.UserID)
		if err != nil {
			return err
		}
		if writerState.WriterDeviceID != input.OperatorDeviceID {
			return apperrors.New(http.StatusConflict, staleWriterRejectedCode, "device_id or writer_epoch does not match current writer")
		}

		if _, err := tx.Exec(ctx, `
UPDATE migration_requests
   SET status = 'CONFIRMED',
       updated_at = now()
 WHERE id = $1
`, input.MigrationRequestID); err != nil {
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
`, snapshot.ToDeviceID, snapshot.UserID).Scan(&writerEpoch); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
UPDATE devices
   SET status = 'REVOKED',
       revoked_at = now(),
       updated_at = now()
 WHERE user_id = $1
   AND device_id = $2
`, snapshot.UserID, snapshot.FromDeviceID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
UPDATE devices
   SET status = 'ACTIVE',
       revoked_at = NULL,
       updated_at = now()
 WHERE user_id = $1
   AND device_id = $2
`, snapshot.UserID, snapshot.ToDeviceID); err != nil {
			return err
		}

		var completedAt time.Time
		if err := tx.QueryRow(ctx, `
UPDATE migration_requests
   SET status = 'COMPLETED',
       updated_at = now()
 WHERE id = $1
 RETURNING updated_at
`, input.MigrationRequestID).Scan(&completedAt); err != nil {
			return err
		}

		response = migrationConfirmResponse{
			MigrationRequestID: input.MigrationRequestID,
			Status:             "COMPLETED",
			WriterDeviceID:     snapshot.ToDeviceID,
			WriterEpoch:        writerEpoch,
			RevokedDeviceID:    snapshot.FromDeviceID,
			CompletedAt:        completedAt.UTC().Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return migrationConfirmResponse{}, err
	}

	return response, nil
}

func loadMigrationConfirmSnapshot(ctx context.Context, tx pgx.Tx, migrationRequestID string) (migrationConfirmSnapshot, error) {
	const query = `
SELECT user_id, from_device_id, to_device_id, status, expires_at
  FROM migration_requests
 WHERE id = $1
 FOR UPDATE
`
	var result migrationConfirmSnapshot
	if err := tx.QueryRow(ctx, query, migrationRequestID).Scan(
		&result.UserID,
		&result.FromDeviceID,
		&result.ToDeviceID,
		&result.Status,
		&result.ExpiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return migrationConfirmSnapshot{}, apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration request is not found")
		}
		return migrationConfirmSnapshot{}, err
	}
	return result, nil
}

func loadUserWriterState(ctx context.Context, tx pgx.Tx, userID string) (userWriterState, error) {
	const query = `
SELECT writer_device_id, writer_epoch
  FROM users
 WHERE user_id = $1
 FOR UPDATE
`
	var result userWriterState
	if err := tx.QueryRow(ctx, query, userID).Scan(&result.WriterDeviceID, &result.WriterEpoch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return userWriterState{}, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
		}
		return userWriterState{}, err
	}
	return result, nil
}

func mapMigrationConfirmPersistenceError(err error) (error, bool) {
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
