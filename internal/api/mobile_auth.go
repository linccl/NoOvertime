package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
)

const (
	authorizationHeader     = "Authorization"
	bearerPrefix            = "Bearer "
	clientFingerprintHeader = "X-Client-Fingerprint"
	mobileTokenStateActive  = "ACTIVE"
	mobileTokenStateRotated = "ROTATED"
	mobileTokenAnonymous    = "ANONYMOUS"
	mobileTokenBound        = "BOUND"
)

var errInvalidBearerToken = errors.New("invalid bearer token")

type mobileTokenHeader struct {
	Token             string
	ClientFingerprint string
}

type mobileAuthContext struct {
	Token                 string
	ClientFingerprint     string
	StoredFingerprintHash string
	UserID                string
	DeviceID              string
	WriterEpoch           int64
	TokenStatus           string
	MembershipTier        string
	MembershipExpiresAt   *time.Time
}

// parseBearerToken extracts a standard Authorization Bearer token.
func parseBearerToken(r *http.Request) (string, error) {
	authHeader := strings.TrimSpace(r.Header.Get(authorizationHeader))
	if len(authHeader) <= len(bearerPrefix) {
		return "", errInvalidBearerToken
	}
	if !strings.EqualFold(authHeader[:len(bearerPrefix)], bearerPrefix) {
		return "", errInvalidBearerToken
	}

	token := strings.TrimSpace(authHeader[len(bearerPrefix):])
	if token == "" {
		return "", errInvalidBearerToken
	}
	return token, nil
}

// parseMobileTokenHeaders normalizes Bearer token and optional client fingerprint.
func parseMobileTokenHeaders(r *http.Request) (mobileTokenHeader, error) {
	token, err := parseBearerToken(r)
	if err != nil {
		return mobileTokenHeader{}, unauthorizedMobileToken()
	}

	return mobileTokenHeader{
		Token:             token,
		ClientFingerprint: strings.TrimSpace(r.Header.Get(clientFingerprintHeader)),
	}, nil
}

// loadMobileAuthContext loads the current mobile token identity for future protected routes.
func loadMobileAuthContext(ctx context.Context, tx pgx.Tx, header mobileTokenHeader, forUpdate bool) (mobileAuthContext, error) {
	query := `
SELECT COALESCE(user_id::text, ''),
       device_id::text,
       writer_epoch,
       status,
       COALESCE(client_fingerprint_hash, '')
  FROM mobile_tokens
 WHERE token_hash = $1
`
	if forUpdate {
		query += " FOR UPDATE"
	}

	var userID string
	var deviceID string
	var writerEpoch int64
	var tokenState string
	var storedFingerprintHash string
	if err := tx.QueryRow(ctx, query, hashMobileToken(header.Token)).Scan(
		&userID,
		&deviceID,
		&writerEpoch,
		&tokenState,
		&storedFingerprintHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mobileAuthContext{}, unauthorizedMobileToken()
		}
		return mobileAuthContext{}, err
	}
	if tokenState != mobileTokenStateActive {
		return mobileAuthContext{}, unauthorizedMobileToken()
	}

	return mobileAuthContext{
		Token:                 header.Token,
		ClientFingerprint:     header.ClientFingerprint,
		StoredFingerprintHash: storedFingerprintHash,
		UserID:                strings.ToLower(strings.TrimSpace(userID)),
		DeviceID:              strings.ToLower(strings.TrimSpace(deviceID)),
		WriterEpoch:           writerEpoch,
		TokenStatus:           mobileTokenStatus(userID),
	}, nil
}

func mobileTokenStatus(userID string) string {
	if strings.TrimSpace(userID) == "" {
		return mobileTokenAnonymous
	}
	return mobileTokenBound
}

func unauthorizedMobileToken() error {
	return apperrors.New(http.StatusUnauthorized, unauthorizedMobileTokenCode, "unauthorized mobile token")
}

func userIDNotReady() error {
	return apperrors.New(http.StatusConflict, userIDNotReadyCode, "user_id will be created after the first successful sync")
}

func requireBoundMobileUser(auth mobileAuthContext) error {
	if auth.UserID == "" {
		return userIDNotReady()
	}
	return nil
}
