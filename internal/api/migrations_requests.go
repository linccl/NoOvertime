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

const (
	migrationsRequestsPath           = "/api/v1/migrations/requests"
	migrationClientFingerprintHeader = "X-Client-Fingerprint"
)

type migrationRequestResponse struct {
	MigrationRequestID string `json:"migration_request_id"`
	Status             string `json:"status"`
	Mode               string `json:"mode"`
	ExpiresAt          string `json:"expires_at"`
}

type migrationRequestTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) migrationRequestsHandler(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}

	input, err := parseMigrationRequestCreateInput(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, input.FromDeviceID)
	if err := s.checkMigrationRequestRateLimit(input.UserID, fingerprint); err != nil {
		return err
	}

	response, err := persistMigrationRequest(r.Context(), s.db, input)
	if err != nil {
		if mappedErr, ok := mapMigrationRequestPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func migrationClientFingerprint(r *http.Request, fallback string) string {
	value := strings.TrimSpace(r.Header.Get(migrationClientFingerprintHeader))
	if value == "" {
		return fallback
	}
	return value
}

func persistMigrationRequest(ctx context.Context, db HealthChecker, input migrationRequestCreateInput) (migrationRequestResponse, error) {
	txDB, ok := db.(migrationRequestTxDB)
	if !ok {
		return migrationRequestResponse{}, errors.New("database transaction is not available")
	}

	var response migrationRequestResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		const query = `
INSERT INTO migration_requests (
	user_id, from_device_id, to_device_id, mode, expires_at
) VALUES (
	$1, $2, $3, $4, $5
)
RETURNING id, status, mode, expires_at
`
		var expiresAt time.Time
		if scanErr := tx.QueryRow(
			ctx,
			query,
			input.UserID,
			input.FromDeviceID,
			input.ToDeviceID,
			input.Mode,
			input.ExpiresAt,
		).Scan(&response.MigrationRequestID, &response.Status, &response.Mode, &expiresAt); scanErr != nil {
			return scanErr
		}
		response.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
		return nil
	})
	if err != nil {
		return migrationRequestResponse{}, err
	}

	return response, nil
}

func mapMigrationRequestPersistenceError(err error) (error, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil, false
	}

	if pgErr.Code == "23505" && pgErr.ConstraintName == "uk_migration_user_pending" {
		return apperrors.New(http.StatusConflict, migrationPendingExistsCode, "pending migration request already exists"), true
	}

	if pgErr.Code == "P0001" {
		if mappedErr, ok := mapRuleErrorKeyToAPIError(extractErrorKey(pgErr.Message)); ok {
			return mappedErr, true
		}
	}

	return nil, false
}
