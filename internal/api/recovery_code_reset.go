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
)

type recoveryCodeResetRequest struct {
	OldRecoveryCode *string `json:"old_recovery_code"`
	ForceReset      *bool   `json:"force_reset"`
}

type recoveryCodeResetInput struct {
	OldRecoveryCode string
	ForceReset      bool
}

type recoveryCodeResetResponse struct {
	RecoveryCode       string `json:"recovery_code"`
	RecoveryCodeMasked string `json:"recovery_code_masked"`
	ShownOnce          bool   `json:"shown_once"`
	UpdatedAt          string `json:"updated_at"`
}

type recoveryCodeResetTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) recoveryCodeResetHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	input, err := parseRecoveryCodeResetBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}
	auth, err := resolvePairingCodeQueryAuth(r.Context(), s.db, r)
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkRecoveryVerifyRateLimit(auth.UserID, fingerprint); err != nil {
		return err
	}

	response, err := persistRecoveryCodeReset(r.Context(), s.db, auth, input)
	if err != nil {
		if mappedErr, ok := mapRecoveryCodeResetPersistenceError(err); ok {
			return mappedErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseRecoveryCodeResetBody(reader io.Reader) (recoveryCodeResetInput, error) {
	var body recoveryCodeResetRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return recoveryCodeResetInput{}, err
	}
	if body.ForceReset == nil {
		return recoveryCodeResetInput{}, invalidArgument("force_reset is required")
	}

	input := recoveryCodeResetInput{
		ForceReset: *body.ForceReset,
	}

	oldRaw := ""
	if body.OldRecoveryCode != nil {
		oldRaw = strings.TrimSpace(*body.OldRecoveryCode)
	}

	if !input.ForceReset {
		if oldRaw == "" {
			return recoveryCodeResetInput{}, invalidArgument("old_recovery_code is required when force_reset is false")
		}
	}
	if oldRaw != "" {
		normalized, err := parseRecoveryCode(oldRaw)
		if err != nil {
			return recoveryCodeResetInput{}, err
		}
		input.OldRecoveryCode = normalized
	}

	return input, nil
}

func persistRecoveryCodeReset(
	ctx context.Context,
	db HealthChecker,
	auth pairingCodeQueryAuth,
	input recoveryCodeResetInput,
) (recoveryCodeResetResponse, error) {
	txDB, ok := db.(recoveryCodeResetTxDB)
	if !ok {
		return recoveryCodeResetResponse{}, errors.New("database transaction is not available")
	}

	var response recoveryCodeResetResponse
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

		if !input.ForceReset {
			matched, err := verifyRecoveryCodeCaseInsensitive(ctx, tx, auth.UserID, input.OldRecoveryCode)
			if err != nil {
				return err
			}
			if !matched {
				return apperrors.New(http.StatusConflict, recoveryCodeInvalidCode, "recovery code is invalid")
			}
		}

		newCode, err := generateRecoveryCode()
		if err != nil {
			return err
		}
		updatedAt, err := updateRecoveryCodeHash(ctx, tx, auth.UserID, newCode)
		if err != nil {
			return err
		}

		response = recoveryCodeResetResponse{
			RecoveryCode:       newCode,
			RecoveryCodeMasked: maskRecoveryCode(newCode),
			ShownOnce:          true,
			UpdatedAt:          updatedAt.UTC().Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return recoveryCodeResetResponse{}, err
	}

	return response, nil
}

func verifyRecoveryCodeCaseInsensitive(ctx context.Context, tx pgx.Tx, userID, oldCode string) (bool, error) {
	const query = `
SELECT CASE
         WHEN recovery_code_hash = '' THEN FALSE
         ELSE recovery_code_hash = crypt($2, recovery_code_hash)
       END
  FROM users
 WHERE user_id = $1
 FOR UPDATE
`
	var matched bool
	if err := tx.QueryRow(ctx, query, userID, strings.ToUpper(oldCode)).Scan(&matched); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, apperrors.New(http.StatusConflict, userNotFoundCode, "user not found")
		}
		return false, err
	}
	return matched, nil
}

func mapRecoveryCodeResetPersistenceError(err error) (error, bool) {
	var apiErr apperrors.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
