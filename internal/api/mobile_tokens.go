package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	tokensIssuePath         = "/api/v1/tokens/issue"
	tokensRotatePath        = "/api/v1/tokens/rotate"
	mobileTokenPrefix       = "tok_"
	mobileTokenByteCount    = 32
	mobileTokenIssueRetries = 8
)

var (
	generateMobileToken  = randomMobileToken
	generateInternalUUID = randomUUIDString
)

type mobileTokenRequest struct {
	ClientFingerprint string `json:"client_fingerprint"`
}

type mobileTokenInput struct {
	ClientFingerprint string
}

type mobileTokenResponse struct {
	Token       string  `json:"token"`
	UserID      *string `json:"user_id"`
	TokenStatus string  `json:"token_status"`
}

type mobileTokenInsertInput struct {
	UserID                string
	DeviceID              string
	WriterEpoch           int64
	ClientFingerprintHash string
}

type mobileTokensTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) tokenIssueHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	input, err := parseMobileTokenBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	response, err := persistMobileTokenIssue(r.Context(), s.db, input)
	if err != nil {
		var apiErr apperrors.APIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func (s *Server) tokenRotateHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	input, err := parseMobileTokenBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}
	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}

	response, err := persistMobileTokenRotate(r.Context(), s.db, header, input)
	if err != nil {
		var apiErr apperrors.APIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseMobileTokenBody(reader io.Reader) (mobileTokenInput, error) {
	var body mobileTokenRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return mobileTokenInput{}, err
	}
	return mobileTokenInput{ClientFingerprint: strings.TrimSpace(body.ClientFingerprint)}, nil
}

func persistMobileTokenIssue(ctx context.Context, db HealthChecker, input mobileTokenInput) (mobileTokenResponse, error) {
	txDB, ok := db.(mobileTokensTxDB)
	if !ok {
		return mobileTokenResponse{}, errors.New("database transaction is not available")
	}

	response := mobileTokenResponse{TokenStatus: mobileTokenAnonymous}
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		deviceID, err := generateInternalUUID()
		if err != nil {
			return err
		}
		token, err := insertMobileTokenWithRetry(ctx, tx, mobileTokenInsertInput{
			DeviceID:              deviceID,
			WriterEpoch:           1,
			ClientFingerprintHash: fingerprintHash(input.ClientFingerprint),
		})
		if err != nil {
			return err
		}
		response.Token = token
		return nil
	})
	if err != nil {
		return mobileTokenResponse{}, err
	}
	return response, nil
}

func persistMobileTokenRotate(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
	input mobileTokenInput,
) (mobileTokenResponse, error) {
	txDB, ok := db.(mobileTokensTxDB)
	if !ok {
		return mobileTokenResponse{}, errors.New("database transaction is not available")
	}

	var response mobileTokenResponse
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		auth, err := loadMobileAuthContext(ctx, tx, header, true)
		if err != nil {
			return err
		}
		if err := rotateMobileToken(ctx, tx, header.Token); err != nil {
			return err
		}

		fingerprintHash := authFingerprintHash(auth, input)
		token, err := insertMobileTokenWithRetry(ctx, tx, mobileTokenInsertInput{
			UserID:                auth.UserID,
			DeviceID:              auth.DeviceID,
			WriterEpoch:           auth.WriterEpoch,
			ClientFingerprintHash: fingerprintHash,
		})
		if err != nil {
			return err
		}
		response = mobileTokenResponse{
			Token:       token,
			UserID:      optionalString(auth.UserID),
			TokenStatus: auth.TokenStatus,
		}
		return nil
	})
	if err != nil {
		return mobileTokenResponse{}, err
	}
	return response, nil
}

func authFingerprintHash(auth mobileAuthContext, input mobileTokenInput) string {
	if input.ClientFingerprint != "" {
		return fingerprintHash(input.ClientFingerprint)
	}
	if auth.StoredFingerprintHash != "" {
		return auth.StoredFingerprintHash
	}
	return fingerprintHash(auth.ClientFingerprint)
}

func fingerprintHash(clientFingerprint string) string {
	if strings.TrimSpace(clientFingerprint) == "" {
		return ""
	}
	return hashClientFingerprint(clientFingerprint)
}

func insertMobileTokenWithRetry(ctx context.Context, tx pgx.Tx, input mobileTokenInsertInput) (string, error) {
	for attempt := 0; attempt < mobileTokenIssueRetries; attempt++ {
		token, err := generateMobileToken()
		if err != nil {
			return "", err
		}
		if err := insertMobileToken(ctx, tx, token, input); err == nil {
			return token, nil
		} else if !isMobileTokenHashConflict(err) {
			return "", err
		}
	}
	return "", errors.New("failed to issue unique mobile token")
}

// insertMobileToken keeps token persistence isolated from future auth/binding flows.
func insertMobileToken(ctx context.Context, tx pgx.Tx, token string, input mobileTokenInsertInput) error {
	const query = `
INSERT INTO mobile_tokens (
	token_hash,
	user_id,
	device_id,
	writer_epoch,
	status,
	client_fingerprint_hash
) VALUES (
	$1,
	NULLIF($2, '')::uuid,
	$3::uuid,
	$4,
	'ACTIVE',
	NULLIF($5, '')
)
`
	_, err := tx.Exec(ctx, query,
		hashMobileToken(token),
		input.UserID,
		input.DeviceID,
		input.WriterEpoch,
		input.ClientFingerprintHash,
	)
	return err
}

func rotateMobileToken(ctx context.Context, tx pgx.Tx, token string) error {
	const query = `
UPDATE mobile_tokens
   SET status = 'ROTATED',
       rotated_at = now()
 WHERE token_hash = $1
`
	_, err := tx.Exec(ctx, query, hashMobileToken(token))
	return err
}

func randomMobileToken() (string, error) {
	var payload [mobileTokenByteCount]byte
	if _, err := rand.Read(payload[:]); err != nil {
		return "", err
	}
	return mobileTokenPrefix + base64.RawURLEncoding.EncodeToString(payload[:]), nil
}

func randomUUIDString() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func hashMobileToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func isMobileTokenHashConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "uq_mobile_tokens_token_hash"
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	normalized := value
	return &normalized
}
