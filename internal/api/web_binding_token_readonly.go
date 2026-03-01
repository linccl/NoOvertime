package api

import (
	"context"
	"errors"
	"net/http"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
)

func authenticateWebBindingReadOnly(
	ctx context.Context,
	tx pgx.Tx,
	bindingToken string,
	clientFingerprint string,
) (string, error) {
	tokenHash := hashWebBindingCredential(bindingToken, clientFingerprint)
	snapshot, err := loadWebReadBindingByTokenHashReadOnly(ctx, tx, tokenHash)
	if err != nil {
		return "", err
	}
	if snapshot.Status != webBindingStatusActive {
		return "", apperrors.New(http.StatusConflict, webBindingReactivateDeniedCode, "revoked web binding cannot be re-activated")
	}
	if snapshot.BindingPairingCodeVersion != snapshot.CurrentPairingCodeVersion {
		return "", apperrors.New(http.StatusConflict, webBindingVersionMismatchCode, "web binding version mismatch")
	}
	return snapshot.UserID, nil
}

func loadWebReadBindingByTokenHashReadOnly(ctx context.Context, tx pgx.Tx, tokenHash string) (webReadBindingsAuthSnapshot, error) {
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

