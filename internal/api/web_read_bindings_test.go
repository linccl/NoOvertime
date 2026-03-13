package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebReadBindingsRoutePaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(`{
		"pairing_code":"12345678",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"web_device_name":"Chrome@Local"
	}`))
	req.Header.Set(requestIDHeader, "req-web-read-bindings-paused")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertFeaturePausedResponse(t, rec, "req-web-read-bindings-paused")
}
