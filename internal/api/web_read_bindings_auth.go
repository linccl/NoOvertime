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

type webReadBindingsAuthRequest struct {
	BindingToken      string `json:"binding_token"`
	ClientFingerprint string `json:"client_fingerprint"`
}

type webReadBindingsAuthInput struct {
	BindingToken      string
	ClientFingerprint string
}

type webReadBindingsAuthResponse struct {
	BindingID          string `json:"binding_id"`
	UserID             string `json:"user_id"`
	Status             string `json:"status"`
	PairingCodeVersion int64  `json:"pairing_code_version"`
	LastSeenAt         string `json:"last_seen_at"`
}

type webReadBindingsAuthSnapshot struct {
	BindingID                 string
	UserID                    string
	Status                    string
	BindingPairingCodeVersion int64
	CurrentPairingCodeVersion int64
}

func (s *Server) webReadBindingsAuthHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	input, err := parseWebReadBindingsAuthBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	subjectHash := hashWebBindingToken(input.BindingToken)
	if err := s.checkWebPairBindRateLimit(subjectHash, input.ClientFingerprint); err != nil {
		return err
	}

	response, err := persistWebReadBindingAuth(r.Context(), s.db, input)
	if err != nil {
		if mappedErr, ok := mapWebReadBindingAuthPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseWebReadBindingsAuthBody(reader io.Reader) (webReadBindingsAuthInput, error) {
	var body webReadBindingsAuthRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return webReadBindingsAuthInput{}, err
	}

	token := strings.TrimSpace(body.BindingToken)
	if token == "" {
		return webReadBindingsAuthInput{}, invalidArgument("binding_token is required")
	}
	if !strings.HasPrefix(token, webBindingTokenPrefix) || len(token) <= len(webBindingTokenPrefix) {
		return webReadBindingsAuthInput{}, unauthorizedWebToken()
	}

	clientFingerprint := strings.TrimSpace(body.ClientFingerprint)
	if clientFingerprint == "" {
		return webReadBindingsAuthInput{}, invalidArgument("client_fingerprint is required")
	}

	return webReadBindingsAuthInput{
		BindingToken:      token,
		ClientFingerprint: clientFingerprint,
	}, nil
}

func unauthorizedWebToken() error {
	return apperrors.New(http.StatusUnauthorized, unauthorizedWebTokenCode, "unauthorized web token")
}

func persistWebReadBindingAuth(ctx context.Context, db HealthChecker, input webReadBindingsAuthInput) (webReadBindingsAuthResponse, error) {
	txDB, ok := db.(webReadBindingsTxDB)
	if !ok {
		return webReadBindingsAuthResponse{}, errors.New("database transaction is not available")
	}

	tokenHash := hashWebBindingCredential(input.BindingToken, input.ClientFingerprint)

	var response webReadBindingsAuthResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		snapshot, err := loadWebReadBindingByTokenHash(ctx, tx, tokenHash)
		if err != nil {
			return err
		}
		if snapshot.Status != webBindingStatusActive {
			return apperrors.New(http.StatusConflict, webBindingReactivateDeniedCode, "revoked web binding cannot be re-activated")
		}
		if snapshot.BindingPairingCodeVersion != snapshot.CurrentPairingCodeVersion {
			return apperrors.New(http.StatusConflict, webBindingVersionMismatchCode, "web binding version mismatch")
		}

		lastSeenAt, err := touchWebReadBindingLastSeenAt(ctx, tx, snapshot.BindingID)
		if err != nil {
			return err
		}

		response = webReadBindingsAuthResponse{
			BindingID:          snapshot.BindingID,
			UserID:             snapshot.UserID,
			Status:             snapshot.Status,
			PairingCodeVersion: snapshot.BindingPairingCodeVersion,
			LastSeenAt:         lastSeenAt.UTC().Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return webReadBindingsAuthResponse{}, err
	}

	return response, nil
}

func loadWebReadBindingByTokenHash(ctx context.Context, tx pgx.Tx, tokenHash string) (webReadBindingsAuthSnapshot, error) {
	const query = `
SELECT b.id,
       b.user_id,
       b.status,
       b.pairing_code_version,
       u.pairing_code_version
  FROM web_read_bindings b
  JOIN users u
    ON u.user_id = b.user_id
 WHERE b.token_hash = $1
 FOR UPDATE OF b
`
	var snapshot webReadBindingsAuthSnapshot
	if err := tx.QueryRow(ctx, query, tokenHash).Scan(
		&snapshot.BindingID,
		&snapshot.UserID,
		&snapshot.Status,
		&snapshot.BindingPairingCodeVersion,
		&snapshot.CurrentPairingCodeVersion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return webReadBindingsAuthSnapshot{}, unauthorizedWebToken()
		}
		return webReadBindingsAuthSnapshot{}, err
	}
	return snapshot, nil
}

func touchWebReadBindingLastSeenAt(ctx context.Context, tx pgx.Tx, bindingID string) (time.Time, error) {
	const query = `
UPDATE web_read_bindings
   SET last_seen_at = now()
 WHERE id = $1
 RETURNING last_seen_at
`
	var lastSeenAt time.Time
	if err := tx.QueryRow(ctx, query, bindingID).Scan(&lastSeenAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, unauthorizedWebToken()
		}
		return time.Time{}, err
	}
	return lastSeenAt, nil
}

func mapWebReadBindingAuthPersistenceError(err error) (error, bool) {
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
