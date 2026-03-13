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
			name: "web_month_summaries_query",
			path: webMonthSummariesQueryPath,
			body: `{"year":2026,"binding_token":"legacy","unknown":"x"}`,
		},
		{
			name: "web_day_summaries_query",
			path: webDaySummariesQueryPath,
			body: `{"month_start":"2026-02-01","client_fingerprint":"legacy","unknown":"x"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			reqID := "req-batch2-unknown-" + tc.name
			req.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(req, testSyncToken)
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, reqID)
		})
	}
}
