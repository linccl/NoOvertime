package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBatch2UnknownFieldErrorMatrix(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "pairing_code_query",
			path: pairingCodeQueryPath,
			body: `{"ensure_generated":true,"unknown":"x"}`,
		},
		{
			name: "pairing_code_reset",
			path: pairingCodeResetPath,
			body: `{"reason":"USER_INITIATED","unknown":"x"}`,
		},
		{
			name: "recovery_code_generate",
			path: recoveryCodeGeneratePath,
			body: `{"require_first_time":true,"unknown":"x"}`,
		},
		{
			name: "recovery_code_reset",
			path: recoveryCodeResetPath,
			body: `{"force_reset":true,"unknown":"x"}`,
		},
		{
			name: "web_read_bindings",
			path: webReadBindingsPath,
			body: `{"pairing_code":"24069175","client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226","web_device_name":"Chrome@Mac","unknown":"x"}`,
		},
		{
			name: "web_read_bindings_auth",
			path: webReadBindingsAuthPath,
			body: `{"binding_token":"wrb_valid_binding_token","client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226","unknown":"x"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			reqID := "req-batch2-unknown-" + tc.name
			req.Header.Set(requestIDHeader, reqID)
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, reqID)
		})
	}
}
