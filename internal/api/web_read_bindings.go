package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	webBindingStatusActive   = "ACTIVE"
	webBindingTokenPrefix    = "wrb_"
	webBindingTokenByteCount = 32
)

var generateWebBindingToken = randomWebBindingToken

type webReadBindingsRequest struct {
	PairingCode       string `json:"pairing_code"`
	ClientFingerprint string `json:"client_fingerprint"`
	WebDeviceName     string `json:"web_device_name"`
}

type webReadBindingsInput struct {
	PairingCode       string
	ClientFingerprint string
}

type webReadBindingsResponse struct {
	BindingID          string `json:"binding_id"`
	BindingToken       string `json:"binding_token"`
	PairingCodeVersion int64  `json:"pairing_code_version"`
	Status             string `json:"status"`
	CreatedAt          string `json:"created_at"`
}

type webReadBindingUserSnapshot struct {
	UserID             string
	PairingCodeVersion int64
}

type webReadBindingRecord struct {
	BindingID string
	Status    string
	CreatedAt time.Time
}

type webReadBindingsTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) webReadBindingsHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	input, err := parseWebReadBindingsBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	if err := s.checkWebPairBindRateLimit(input.PairingCode, input.ClientFingerprint); err != nil {
		return err
	}

	response, err := persistWebReadBinding(r.Context(), s.db, input)
	if err != nil {
		if mappedErr, ok := mapWebReadBindingPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseWebReadBindingsBody(reader io.Reader) (webReadBindingsInput, error) {
	var body webReadBindingsRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return webReadBindingsInput{}, err
	}

	pairingCode, err := parsePairingCode(body.PairingCode)
	if err != nil {
		return webReadBindingsInput{}, err
	}

	clientFingerprint := strings.TrimSpace(body.ClientFingerprint)
	if clientFingerprint == "" {
		return webReadBindingsInput{}, invalidArgument("client_fingerprint is required")
	}

	webDeviceName := strings.TrimSpace(body.WebDeviceName)
	if webDeviceName == "" {
		return webReadBindingsInput{}, invalidArgument("web_device_name is required")
	}

	return webReadBindingsInput{
		PairingCode:       pairingCode,
		ClientFingerprint: clientFingerprint,
	}, nil
}

func persistWebReadBinding(ctx context.Context, db HealthChecker, input webReadBindingsInput) (webReadBindingsResponse, error) {
	txDB, ok := db.(webReadBindingsTxDB)
	if !ok {
		return webReadBindingsResponse{}, errors.New("database transaction is not available")
	}

	var response webReadBindingsResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		snapshot, err := loadWebReadBindingUserByPairingCode(ctx, tx, input.PairingCode)
		if err != nil {
			return err
		}

		for attempt := 0; attempt < 4; attempt++ {
			token, err := generateWebBindingToken()
			if err != nil {
				return err
			}
			tokenHash := hashWebBindingCredential(token, input.ClientFingerprint)

			record, err := upsertWebReadBindingRecord(ctx, tx, snapshot, tokenHash)
			if err == nil {
				response = webReadBindingsResponse{
					BindingID:          record.BindingID,
					BindingToken:       token,
					PairingCodeVersion: snapshot.PairingCodeVersion,
					Status:             record.Status,
					CreatedAt:          record.CreatedAt.UTC().Format(time.RFC3339),
				}
				return nil
			}
			if isWebBindingTokenHashConflict(err) {
				continue
			}
			return err
		}

		return errors.New("failed to issue unique web binding token")
	})
	if err != nil {
		return webReadBindingsResponse{}, err
	}

	return response, nil
}

func loadWebReadBindingUserByPairingCode(ctx context.Context, tx pgx.Tx, pairingCode string) (webReadBindingUserSnapshot, error) {
	const query = `
SELECT user_id, pairing_code_version
  FROM users
 WHERE pairing_code = $1
   AND pairing_code <> ''
`
	var snapshot webReadBindingUserSnapshot
	if err := tx.QueryRow(ctx, query, pairingCode).Scan(&snapshot.UserID, &snapshot.PairingCodeVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return webReadBindingUserSnapshot{}, apperrors.New(http.StatusConflict, pairingCodeInvalidCode, "pairing code is invalid")
		}
		return webReadBindingUserSnapshot{}, err
	}
	return snapshot, nil
}

func upsertWebReadBindingRecord(
	ctx context.Context,
	tx pgx.Tx,
	snapshot webReadBindingUserSnapshot,
	tokenHash string,
) (webReadBindingRecord, error) {
	const selectActiveQuery = `
SELECT id, created_at
  FROM web_read_bindings
 WHERE user_id = $1
   AND pairing_code_version = $2
   AND status = 'ACTIVE'
 ORDER BY created_at DESC
 LIMIT 1
 FOR UPDATE
`

	var bindingID string
	var createdAt time.Time
	err := tx.QueryRow(ctx, selectActiveQuery, snapshot.UserID, snapshot.PairingCodeVersion).Scan(&bindingID, &createdAt)
	if err == nil {
		const updateQuery = `
UPDATE web_read_bindings
   SET token_hash = $2,
       last_seen_at = now()
 WHERE id = $1
 RETURNING id, status, created_at
`
		var status string
		if err := tx.QueryRow(ctx, updateQuery, bindingID, tokenHash).Scan(&bindingID, &status, &createdAt); err != nil {
			return webReadBindingRecord{}, err
		}
		return webReadBindingRecord{
			BindingID: bindingID,
			Status:    status,
			CreatedAt: createdAt,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return webReadBindingRecord{}, err
	}

	const insertQuery = `
INSERT INTO web_read_bindings (
	user_id,
	pairing_code_version,
	token_hash,
	status,
	last_seen_at
) VALUES (
	$1,
	$2,
	$3,
	'ACTIVE',
	now()
)
RETURNING id, status, created_at
`
	var status string
	if err := tx.QueryRow(ctx, insertQuery, snapshot.UserID, snapshot.PairingCodeVersion, tokenHash).Scan(&bindingID, &status, &createdAt); err != nil {
		return webReadBindingRecord{}, err
	}
	return webReadBindingRecord{
		BindingID: bindingID,
		Status:    status,
		CreatedAt: createdAt,
	}, nil
}

func randomWebBindingToken() (string, error) {
	var payload [webBindingTokenByteCount]byte
	if _, err := rand.Read(payload[:]); err != nil {
		return "", err
	}
	return webBindingTokenPrefix + base64.RawURLEncoding.EncodeToString(payload[:]), nil
}

func hashWebBindingToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func hashClientFingerprint(clientFingerprint string) string {
	normalized := strings.ToLower(strings.TrimSpace(clientFingerprint))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func hashWebBindingCredential(token, clientFingerprint string) string {
	return hashWebBindingToken(token) + ":" + hashClientFingerprint(clientFingerprint)
}

func isWebBindingTokenHashConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "uq_web_read_bindings_token_hash"
}

func mapWebReadBindingPersistenceError(err error) (error, bool) {
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
