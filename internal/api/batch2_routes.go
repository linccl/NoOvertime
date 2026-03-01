package api

import (
	"net/http"

	apperrors "noovertime/internal/errors"
)

const (
	pairingCodeQueryPath      = "/api/v1/pairing-code/query"
	pairingCodeResetPath      = "/api/v1/pairing-code/reset"
	recoveryCodeGeneratePath  = "/api/v1/recovery-code/generate"
	recoveryCodeResetPath     = "/api/v1/recovery-code/reset"
	webReadBindingsPath       = "/api/v1/web/read-bindings"
	webReadBindingsAuthPath   = "/api/v1/web/read-bindings/auth"
	webMonthSummariesQueryPath = "/api/v1/web/month-summaries/query"
	webDaySummariesQueryPath   = "/api/v1/web/day-summaries/query"
	batch2NotImplementedError = "endpoint is registered but not implemented"
)

func ensurePostMethod(r *http.Request) error {
	if r.Method != http.MethodPost {
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}
	return nil
}
