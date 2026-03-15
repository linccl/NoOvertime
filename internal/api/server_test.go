package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	apperrors "noovertime/internal/errors"
)

type healthyDB struct{}

func (healthyDB) Health(context.Context) error { return nil }

func (healthyDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	return testMobileAuthContextForToken(header.Token), nil
}

type unhealthyDB struct{}

func (unhealthyDB) Health(context.Context) error { return errors.New("db down") }

func TestRunGracefulShutdownOnSignal(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	originalNotify := notifyContext
	originalListen := listenAndServe
	originalShutdown := shutdownServer
	defer func() {
		notifyContext = originalNotify
		listenAndServe = originalListen
		shutdownServer = originalShutdown
	}()

	signalCtx, cancelSignal := context.WithCancel(context.Background())
	notifyContext = func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		return signalCtx, func() {}
	}
	listenBlocked := make(chan struct{})
	listenAndServe = func(*http.Server) error {
		<-listenBlocked
		return nil
	}

	shutdownCalled := false
	shutdownServer = func(*http.Server, context.Context) error {
		shutdownCalled = true
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- server.Run()
	}()

	time.Sleep(80 * time.Millisecond)
	cancelSignal()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if !shutdownCalled {
			t.Fatal("shutdown was not called")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not shut down in time")
	}

	close(listenBlocked)
}

func TestRequestLoggingIncludesMethodPathStatusAndRequestID(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	var logBuffer bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logBuffer)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	}()

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(requestIDHeader, "req-log-001")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(requestIDHeader); got != "req-log-001" {
		t.Fatalf("request id header = %q", got)
	}

	logLine := logBuffer.String()
	if !strings.Contains(logLine, "method=GET") {
		t.Fatalf("log missing method: %q", logLine)
	}
	if !strings.Contains(logLine, "path=/health") {
		t.Fatalf("log missing path: %q", logLine)
	}
	if !strings.Contains(logLine, "status=200") {
		t.Fatalf("log missing status: %q", logLine)
	}
	if !strings.Contains(logLine, "request_id=req-log-001") {
		t.Fatalf("log missing request_id: %q", logLine)
	}
}

func TestHealthEndpointReturnsAppAndDBStatus(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.App.Status != "ok" {
		t.Fatalf("app.status = %q", payload.App.Status)
	}
	if payload.Database.Status != "ok" {
		t.Fatalf("database.status = %q", payload.Database.Status)
	}
}

func TestHealthEndpointReturnsDegradedWhenDatabaseUnavailable(t *testing.T) {
	server := NewServer("127.0.0.1:0", unhealthyDB{})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.App.Status != "degraded" {
		t.Fatalf("app.status = %q", payload.App.Status)
	}
	if payload.Database.Status != "down" {
		t.Fatalf("database.status = %q", payload.Database.Status)
	}
	if payload.Database.Message == "" {
		t.Fatal("database.message is empty")
	}
}

func TestPanicRecoveredWithStandardErrorResponse(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.handle("/panic", func(http.ResponseWriter, *http.Request) error {
		panic("boom")
	})

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(requestIDHeader, "req-panic-001")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.ErrorCode != internalErrorCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.Message != internalErrorMessage {
		t.Fatalf("message = %q", payload.Message)
	}
	if payload.RequestID != "req-panic-001" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
}

func TestHandlerErrorReturnsStandardErrorResponse(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.handle("/bad", func(http.ResponseWriter, *http.Request) error {
		return apperrors.New(http.StatusBadRequest, "INVALID_ARGUMENT", "bad request")
	})

	request := httptest.NewRequest(http.MethodGet, "/bad", nil)
	request.Header.Set(requestIDHeader, "req-bad-001")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.ErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.Message != "bad request" {
		t.Fatalf("message = %q", payload.Message)
	}
	if payload.RequestID != "req-bad-001" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
}

func TestMigrationRoutesRejectNonPostWithUnifiedError(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	tests := []struct {
		name string
		path string
	}{
		{name: "requests", path: "/api/v1/migrations/requests"},
		{name: "confirm", path: "/api/v1/migrations/f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60/confirm"},
		{name: "takeover", path: "/api/v1/migrations/takeover"},
		{name: "forced_takeover", path: "/api/v1/migrations/forced-takeover"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqID := "req-" + tc.name + "-method-not-allowed"
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(request, testSyncToken)
			recorder := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}

			var payload apperrors.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.ErrorCode != methodNotAllowedCode {
				t.Fatalf("error_code = %q", payload.ErrorCode)
			}
			if strings.TrimSpace(payload.Message) == "" {
				t.Fatal("message is empty")
			}
			if payload.RequestID != reqID {
				t.Fatalf("request_id = %q", payload.RequestID)
			}
		})
	}
}

func TestMigrationRoutesRegisteredAndUseUnifiedWriteError(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "requests",
			path:       "/api/v1/migrations/requests",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   invalidArgumentCode,
		},
		{
			name:       "confirm",
			path:       "/api/v1/migrations/f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60/confirm",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   invalidArgumentCode,
		},
		{
			name:       "takeover",
			path:       "/api/v1/migrations/takeover",
			body:       `{}`,
			wantStatus: http.StatusGone,
			wantCode:   featurePausedCode,
		},
		{
			name:       "forced_takeover",
			path:       "/api/v1/migrations/forced-takeover",
			body:       `{}`,
			wantStatus: http.StatusGone,
			wantCode:   featurePausedCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqID := "req-" + tc.name + "-route-hit"
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(request, testSyncToken)
			recorder := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}

			var payload apperrors.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.ErrorCode != tc.wantCode {
				t.Fatalf("error_code = %q", payload.ErrorCode)
			}
			if strings.TrimSpace(payload.Message) == "" {
				t.Fatal("message is empty")
			}
			if payload.RequestID != reqID {
				t.Fatalf("request_id = %q", payload.RequestID)
			}
		})
	}
}

func TestExistingHealthAndSyncRoutesUnaffectedByMigrationRouteRegistration(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(healthRec, healthReq)

	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", healthRec.Code, healthRec.Body.String())
	}
	var healthPayload healthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if healthPayload.App.Status != "ok" || healthPayload.Database.Status != "ok" {
		t.Fatalf("health payload = %+v", healthPayload)
	}

	syncReq := httptest.NewRequest(http.MethodGet, "/api/v1/sync/commits", nil)
	syncReq.Header.Set(requestIDHeader, "req-sync-method-not-allowed")
	syncRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(syncRec, syncReq)

	if syncRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("sync status = %d body=%s", syncRec.Code, syncRec.Body.String())
	}
	var syncPayload apperrors.ErrorResponse
	if err := json.Unmarshal(syncRec.Body.Bytes(), &syncPayload); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if syncPayload.ErrorCode != methodNotAllowedCode {
		t.Fatalf("sync error_code = %q", syncPayload.ErrorCode)
	}
	if syncPayload.RequestID != "req-sync-method-not-allowed" {
		t.Fatalf("sync request_id = %q", syncPayload.RequestID)
	}
}

func TestBatch2RoutesRejectNonPostWithUnifiedError(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	tests := []struct {
		name string
		path string
	}{
		{name: "pairing_query", path: pairingCodeQueryPath},
		{name: "pairing_reset", path: pairingCodeResetPath},
		{name: "recovery_generate", path: recoveryCodeGeneratePath},
		{name: "recovery_reset", path: recoveryCodeResetPath},
		{name: "web_read_bindings", path: webReadBindingsPath},
		{name: "web_read_bindings_auth", path: webReadBindingsAuthPath},
		{name: "web_month_summaries_query", path: webMonthSummariesQueryPath},
		{name: "web_day_summaries_query", path: webDaySummariesQueryPath},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqID := "req-batch2-" + tc.name + "-method-not-allowed"
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(request, testSyncToken)
			recorder := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}

			var payload apperrors.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.ErrorCode != methodNotAllowedCode {
				t.Fatalf("error_code = %q", payload.ErrorCode)
			}
			if strings.TrimSpace(payload.Message) == "" {
				t.Fatal("message is empty")
			}
			if payload.RequestID != reqID {
				t.Fatalf("request_id = %q", payload.RequestID)
			}
		})
	}
}

func TestBatch2RoutesRegisteredAndUseUnifiedWriteError(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCode   string
	}{
		{name: "pairing_query", path: pairingCodeQueryPath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "pairing_reset", path: pairingCodeResetPath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "recovery_generate", path: recoveryCodeGeneratePath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "recovery_reset", path: recoveryCodeResetPath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "web_read_bindings", path: webReadBindingsPath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "web_read_bindings_auth", path: webReadBindingsAuthPath, wantStatus: http.StatusGone, wantCode: featurePausedCode},
		{name: "web_month_summaries_query", path: webMonthSummariesQueryPath, wantStatus: http.StatusBadRequest, wantCode: invalidArgumentCode},
		{name: "web_day_summaries_query", path: webDaySummariesQueryPath, wantStatus: http.StatusBadRequest, wantCode: invalidArgumentCode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqID := "req-batch2-" + tc.name + "-route-hit"
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{}`))
			request.Header.Set(requestIDHeader, reqID)
			setSyncAuthHeader(request, testSyncToken)
			recorder := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}

			var payload apperrors.ErrorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.ErrorCode != tc.wantCode {
				t.Fatalf("error_code = %q", payload.ErrorCode)
			}
			if strings.TrimSpace(payload.Message) == "" {
				t.Fatal("message is empty")
			}
			if payload.RequestID != reqID {
				t.Fatalf("request_id = %q", payload.RequestID)
			}
		})
	}
}

func TestUploadRoutesNotRegisteredWithoutObjectStore(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	tests := []string{
		punchPhotoUploadPath,
		logUploadPath,
	}

	for _, route := range tests {
		request := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`))
		recorder := httptest.NewRecorder()

		server.httpServer.Handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("route=%s status=%d body=%s", route, recorder.Code, recorder.Body.String())
		}
	}
}

func TestUploadRoutesRegisterPerStore(t *testing.T) {
	server := NewServer(
		"127.0.0.1:0",
		healthyDB{},
		WithPunchPhotoObjectStore(&fakeObjectStore{}),
	)

	request := httptest.NewRequest(http.MethodPost, punchPhotoUploadPath, strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusNotFound {
		t.Fatalf("photo upload route should be registered")
	}

	request = httptest.NewRequest(http.MethodPost, logUploadPath, strings.NewReader(`{}`))
	recorder = httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("log upload route status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
