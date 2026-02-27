package api

import (
	"net/http"

	apperrors "noovertime/internal/errors"
)

const (
	migrationSourceMismatchCode        = "MIGRATION_SOURCE_MISMATCH"
	migrationStateInvalidCode          = "MIGRATION_STATE_INVALID"
	migrationImmutableFieldsCode       = "MIGRATION_IMMUTABLE_FIELDS"
	migrationPendingExistsCode         = "MIGRATION_PENDING_EXISTS"
	migrationExpiredCode               = "MIGRATION_EXPIRED"
	userNotFoundCode                   = "USER_NOT_FOUND"
	pairingCodeGenerateFailedCode      = "PAIRING_CODE_GENERATE_FAILED"
	recoveryCodeAlreadyInitializedCode = "RECOVERY_CODE_ALREADY_INITIALIZED"
	webBindingReactivateDeniedCode     = "WEB_BINDING_REACTIVATE_DENIED"
	webBindingVersionMismatchCode      = "WEB_BINDING_VERSION_MISMATCH"
	webBindingVersionImmutableCode     = "WEB_BINDING_VERSION_IMMUTABLE"
	webBindingUserImmutableCode        = "WEB_BINDING_USER_IMMUTABLE"
	unauthorizedDeviceCode             = "UNAUTHORIZED_DEVICE"
	unauthorizedWebTokenCode           = "UNAUTHORIZED_WEB_TOKEN"
)

// mapRuleErrorKeyToAPIError maps database P0001 error_key values to API errors.
// It is shared by sync and migration write paths to keep error mapping consistent.
func mapRuleErrorKeyToAPIError(errorKey string) (apperrors.APIError, bool) {
	switch errorKey {
	case "PUNCH_END_REQUIRES_START":
		return apperrors.New(http.StatusConflict, punchEndRequiresStart, "END requires START"), true
	case "PUNCH_END_NOT_AFTER_START":
		return apperrors.New(http.StatusConflict, punchEndNotAfterStart, "END must be later than START"), true
	case "AUTO_PUNCH_ON_FULL_DAY_LEAVE", "FULL_DAY_LEAVE_WITH_AUTO_PUNCH":
		return apperrors.New(http.StatusConflict, autoPunchLeaveConflict, "AUTO punch conflicts with FULL_DAY leave"), true
	case "SYNC_COMMIT_STALE_WRITER":
		return apperrors.New(http.StatusConflict, staleWriterRejectedCode, "device_id or writer_epoch does not match current writer"), true
	case "SYNC_COMMIT_USER_NOT_FOUND", "MIGRATION_USER_NOT_FOUND", "WEB_BINDING_USER_NOT_FOUND", "ROTATE_PAIRING_USER_NOT_FOUND":
		return apperrors.New(http.StatusConflict, userNotFoundCode, "user not found"), true
	case "MIGRATION_SOURCE_MISMATCH":
		return apperrors.New(http.StatusConflict, migrationSourceMismatchCode, "migration source mismatch"), true
	case "MIGRATION_TRANSITION_INVALID":
		return apperrors.New(http.StatusConflict, migrationStateInvalidCode, "migration state invalid"), true
	case "MIGRATION_IMMUTABLE_FIELDS":
		return apperrors.New(http.StatusConflict, migrationImmutableFieldsCode, "migration immutable fields conflict"), true
	case "WEB_BINDING_REACTIVATE_DENIED":
		return apperrors.New(http.StatusConflict, webBindingReactivateDeniedCode, "revoked web binding cannot be re-activated"), true
	case "WEB_BINDING_VERSION_MISMATCH":
		return apperrors.New(http.StatusConflict, webBindingVersionMismatchCode, "web binding version mismatch"), true
	case "WEB_BINDING_VERSION_IMMUTABLE":
		return apperrors.New(http.StatusConflict, webBindingVersionImmutableCode, "web binding pairing_code_version is immutable"), true
	case "WEB_BINDING_USER_ID_IMMUTABLE":
		return apperrors.New(http.StatusConflict, webBindingUserImmutableCode, "web binding user_id is immutable"), true
	case "RULE_PAIRING_CODE_LOOKUP_MISS_AFTER_RESET":
		return apperrors.New(http.StatusConflict, pairingCodeInvalidCode, "pairing code is invalid"), true
	case "RULE_RECOVERY_CODE_HASH_MISMATCH_AFTER_RESET":
		return apperrors.New(http.StatusConflict, recoveryCodeInvalidCode, "recovery code is invalid"), true
	default:
		return apperrors.APIError{}, false
	}
}
