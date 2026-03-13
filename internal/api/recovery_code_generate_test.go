package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryCodeGenerateRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(`{"require_first_time":true}`))
	req.Header.Set(requestIDHeader, "req-recovery-code-generate-paused")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-recovery-code-generate-paused")
}
