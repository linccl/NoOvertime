package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryCodeResetRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(`{"force_reset":true}`))
	req.Header.Set(requestIDHeader, "req-recovery-code-reset-paused")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-recovery-code-reset-paused")
}
