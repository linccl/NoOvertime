package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperrors "noovertime/internal/errors"
)

func TestParseMigrationRequestCreateInputSuccess(t *testing.T) {
	input, err := parseMigrationRequestCreateInput(strings.NewReader(`{
		"user_id":"8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
		"from_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c",
		"mode":"NORMAL",
		"expires_at":"2026-02-12T11:00:00Z"
	}`))
	if err != nil {
		t.Fatalf("parseMigrationRequestCreateInput() error = %v", err)
	}

	if input.Mode != "NORMAL" {
		t.Fatalf("mode = %q", input.Mode)
	}
	if input.UserID != "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362" {
		t.Fatalf("user_id = %q", input.UserID)
	}
	if !input.ExpiresAt.Equal(time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires_at = %s", input.ExpiresAt.Format(time.RFC3339))
	}
}

func TestParseMigrationRequestCreateInputUnknownField(t *testing.T) {
	_, err := parseMigrationRequestCreateInput(strings.NewReader(`{
		"user_id":"8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
		"from_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c",
		"mode":"NORMAL",
		"expires_at":"2026-02-12T11:00:00Z",
		"unknown_field":"x"
	}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	assertAPIError(t, err, http.StatusBadRequest, unknownFieldCode)
}

func TestParseMigrationRequestCreateInputInvalidArgument(t *testing.T) {
	_, err := parseMigrationRequestCreateInput(strings.NewReader(`{
		"user_id":"8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
		"from_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c",
		"mode":"INVALID",
		"expires_at":"2026-02-12T11:00:00Z"
	}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	assertAPIError(t, err, http.StatusBadRequest, invalidArgumentCode)
}

func TestParseMigrationConfirmInputSuccess(t *testing.T) {
	input, err := parseMigrationConfirmInput(
		"f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60",
		strings.NewReader(`{"action":"CONFIRM","operator_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3"}`),
	)
	if err != nil {
		t.Fatalf("parseMigrationConfirmInput() error = %v", err)
	}

	if input.Action != "CONFIRM" {
		t.Fatalf("action = %q", input.Action)
	}
	if input.MigrationRequestID != "f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60" {
		t.Fatalf("migration_request_id = %q", input.MigrationRequestID)
	}
}

func TestParseMigrationConfirmInputInvalidArgument(t *testing.T) {
	_, err := parseMigrationConfirmInput(
		"not-a-uuid",
		strings.NewReader(`{"action":"CONFIRM","operator_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3"}`),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	assertAPIError(t, err, http.StatusBadRequest, invalidArgumentCode)
}

func TestParseMigrationForcedTakeoverInputSuccess(t *testing.T) {
	input, err := parseMigrationForcedTakeoverInput(strings.NewReader(`{
		"pairing_code":"39481726",
		"recovery_code":"ab12cd34ef56gh78",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c"
	}`))
	if err != nil {
		t.Fatalf("parseMigrationForcedTakeoverInput() error = %v", err)
	}

	if input.PairingCode != "39481726" {
		t.Fatalf("pairing_code = %q", input.PairingCode)
	}
	if input.RecoveryCode != "AB12CD34EF56GH78" {
		t.Fatalf("recovery_code = %q", input.RecoveryCode)
	}
}

func TestParseMigrationForcedTakeoverInputInvalidArgument(t *testing.T) {
	_, err := parseMigrationForcedTakeoverInput(strings.NewReader(`{
		"pairing_code":"1234",
		"recovery_code":"AB12CD34EF56GH78",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c"
	}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	assertAPIError(t, err, http.StatusBadRequest, pairingCodeFormatInvalidCode)
}

func TestMigrationParseErrorUsesStandardErrorEnvelope(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.handle("/internal/migrations-parse-probe", func(w http.ResponseWriter, r *http.Request) error {
		_, err := parseMigrationRequestCreateInput(r.Body)
		return err
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations-parse-probe", strings.NewReader(`{
		"user_id":"8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
		"from_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c",
		"mode":"NORMAL",
		"expires_at":"2026-02-12T11:00:00Z",
		"unknown":"x"
	}`))
	request.Header.Set(requestIDHeader, "req-migration-parse-001")

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}

	var response apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ErrorCode != unknownFieldCode {
		t.Fatalf("error_code = %q", response.ErrorCode)
	}
	if strings.TrimSpace(response.Message) == "" {
		t.Fatal("message is empty")
	}
	if response.RequestID != "req-migration-parse-001" {
		t.Fatalf("request_id = %q", response.RequestID)
	}
}

func assertAPIError(t *testing.T, err error, expectedStatus int, expectedCode string) {
	t.Helper()

	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode() != expectedStatus {
		t.Fatalf("status = %d", apiErr.StatusCode())
	}
	if apiErr.Code != expectedCode {
		t.Fatalf("error_code = %q", apiErr.Code)
	}
}
