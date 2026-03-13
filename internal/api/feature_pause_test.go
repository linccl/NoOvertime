package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "noovertime/internal/errors"
)

func assertFeaturePausedResponse(t *testing.T, rec *httptest.ResponseRecorder, reqID string) {
	t.Helper()

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != featurePausedCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if !strings.Contains(payload.Message, "paused") {
		t.Fatalf("message = %q", payload.Message)
	}
	if reqID != "" && payload.RequestID != reqID {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
}
