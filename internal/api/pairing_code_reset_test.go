package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPairingCodeResetRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(`{"reason":"USER_INITIATED"}`))
	req.Header.Set(requestIDHeader, "req-pairing-code-reset-paused")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-pairing-code-reset-paused")
}
