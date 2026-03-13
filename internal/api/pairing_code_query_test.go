package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPairingCodeQueryRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(`{"ensure_generated":true}`))
	req.Header.Set(requestIDHeader, "req-pairing-code-query-paused")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-pairing-code-query-paused")
}
