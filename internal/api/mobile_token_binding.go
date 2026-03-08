package api

import (
	"context"
	"errors"
	"net/http"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const mobileTokenBindingRetries = 8

type mobileAuthResolverDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type mobileAuthDirectResolver interface {
	resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error)
}

func resolveMobileAuthContext(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
	requireBound bool,
) (mobileAuthContext, error) {
	if resolver, ok := db.(mobileAuthDirectResolver); ok {
		auth, err := resolver.resolveMobileAuthContextDirect(header)
		if err != nil {
			return mobileAuthContext{}, err
		}
		if requireBound {
			if err := requireBoundMobileUser(auth); err != nil {
				return mobileAuthContext{}, err
			}
		}
		return auth, nil
	}

	txDB, ok := db.(mobileAuthResolverDB)
	if !ok {
		return mobileAuthContext{}, errors.New("database transaction is not available")
	}

	var auth mobileAuthContext
	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		loaded, err := loadMobileAuthContext(ctx, tx, header, false)
		if err != nil {
			return err
		}
		if requireBound {
			if err := requireBoundMobileUser(loaded); err != nil {
				return err
			}
		}
		auth = loaded
		return nil
	})
	if err != nil {
		return mobileAuthContext{}, err
	}
	return auth, nil
}

func bindAnonymousMobileToken(ctx context.Context, tx pgx.Tx, auth mobileAuthContext) (mobileAuthContext, error) {
	if auth.UserID != "" {
		return auth, nil
	}

	userID, err := createCompatibleMobileUser(ctx, tx, auth.DeviceID, auth.WriterEpoch)
	if err != nil {
		return mobileAuthContext{}, err
	}
	if err := bindMobileTokenUser(ctx, tx, auth.Token, userID); err != nil {
		return mobileAuthContext{}, err
	}

	auth.UserID = userID
	auth.TokenStatus = mobileTokenBound
	return auth, nil
}

func createCompatibleMobileUser(ctx context.Context, tx pgx.Tx, deviceID string, writerEpoch int64) (string, error) {
	for attempt := 0; attempt < mobileTokenBindingRetries; attempt++ {
		userID, err := generateInternalUUID()
		if err != nil {
			return "", err
		}
		pairingCode, err := randomPairingCode()
		if err != nil {
			return "", err
		}
		if err := insertMobileUser(ctx, tx, userID, pairingCode, writerEpoch); err != nil {
			if isPairingCodeConflict(err) {
				continue
			}
			return "", err
		}
		if err := ensureUserDeviceExists(ctx, tx, userID, deviceID); err != nil {
			return "", err
		}
		if err := setUserWriterState(ctx, tx, userID, deviceID, writerEpoch); err != nil {
			return "", err
		}
		return userID, nil
	}

	return "", apperrors.New(http.StatusConflict, pairingCodeGenerateFailedCode, "pairing code generate failed")
}

func insertMobileUser(ctx context.Context, tx pgx.Tx, userID, pairingCode string, writerEpoch int64) error {
	const query = `
INSERT INTO users (
	user_id,
	pairing_code,
	recovery_code_hash,
	writer_epoch
) VALUES (
	$1::uuid,
	$2,
	'',
	$3
)
`
	_, err := tx.Exec(ctx, query, userID, pairingCode, writerEpoch)
	return err
}

func ensureUserDeviceExists(ctx context.Context, tx pgx.Tx, userID, deviceID string) error {
	const query = `
INSERT INTO devices (
	device_id,
	user_id,
	status,
	created_at,
	updated_at
) VALUES (
	$1::uuid,
	$2::uuid,
	'ACTIVE',
	now(),
	now()
)
ON CONFLICT (device_id) DO UPDATE
SET status = 'ACTIVE',
    revoked_at = NULL,
    updated_at = now()
WHERE devices.user_id = EXCLUDED.user_id
`
	_, err := tx.Exec(ctx, query, deviceID, userID)
	return err
}

func setUserWriterState(ctx context.Context, tx pgx.Tx, userID, deviceID string, writerEpoch int64) error {
	const query = `
UPDATE users
   SET writer_device_id = $2::uuid,
       writer_epoch = $3,
       updated_at = now()
 WHERE user_id = $1::uuid
`
	_, err := tx.Exec(ctx, query, userID, deviceID, writerEpoch)
	return err
}

func bindMobileTokenUser(ctx context.Context, tx pgx.Tx, token, userID string) error {
	const query = `
UPDATE mobile_tokens
   SET user_id = $2::uuid
 WHERE token_hash = $1
`
	_, err := tx.Exec(ctx, query, hashMobileToken(token), userID)
	return err
}

func bindMobileTokensByDevice(ctx context.Context, tx pgx.Tx, userID, deviceID string, writerEpoch int64) error {
	const query = `
UPDATE mobile_tokens
   SET user_id = $2::uuid,
       writer_epoch = $3
 WHERE device_id = $1::uuid
   AND status = 'ACTIVE'
`
	_, err := tx.Exec(ctx, query, deviceID, userID, writerEpoch)
	return err
}

func rotateActiveMobileTokensByUser(ctx context.Context, tx pgx.Tx, userID string) error {
	const query = `
UPDATE mobile_tokens
   SET status = 'ROTATED',
       rotated_at = now()
 WHERE user_id = $1::uuid
   AND status = 'ACTIVE'
`
	_, err := tx.Exec(ctx, query, userID)
	return err
}

func isPairingCodeConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "uq_users_pairing_code"
}
