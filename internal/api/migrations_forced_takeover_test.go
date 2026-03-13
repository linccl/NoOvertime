package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMigrationTakeoverRoutesPaused(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	tests := []struct {
		name string
		path string
	}{
		{name: "takeover", path: migrationsTakeoverPath},
		{name: "forced_takeover", path: migrationsForcedTakeoverPath},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{
				"pairing_code":"12345678",
				"recovery_code":"AB12CD34EF56GH78"
			}`))
			reqID := "req-" + tc.name + "-paused"
			req.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(req, testTargetDeviceToken)
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertFeaturePausedResponse(t, rec, reqID)
		})
	}
}
