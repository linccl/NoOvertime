package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebReadBindingsAuthRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(`{
		"binding_token":"wrb_legacy_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226"
	}`))
	req.Header.Set(requestIDHeader, "req-web-read-bindings-auth-paused")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-web-read-bindings-auth-paused")
}
