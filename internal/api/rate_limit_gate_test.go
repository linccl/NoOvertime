package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperrors "noovertime/internal/errors"
)

func TestMigrationRateGateScenesCanTriggerIndependently(t *testing.T) {
	now := time.Date(2026, 2, 13, 14, 0, 0, 0, time.UTC)
	gate := newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(1),
		rateLimitSceneMigrationConfirm: testPolicy(1),
		rateLimitSceneRecoveryVerify:   testPolicy(1),
		rateLimitScenePairingReset:     testPolicy(1),
		rateLimitSceneWebPairBind:      testPolicy(1),
	}, now)

	if err := gate.Check(rateLimitSceneMigrationRequest, "subject-A", "fp-A"); err != nil {
		t.Fatalf("request first call error = %v", err)
	}
	assertRateLimitBlockedError(t, gate.Check(rateLimitSceneMigrationRequest, "subject-A", "fp-A"))

	if err := gate.Check(rateLimitSceneMigrationConfirm, "subject-A", "fp-A"); err != nil {
		t.Fatalf("confirm first call error = %v", err)
	}
	assertRateLimitBlockedError(t, gate.Check(rateLimitSceneMigrationConfirm, "subject-A", "fp-A"))

	if err := gate.Check(rateLimitSceneRecoveryVerify, "subject-A", "fp-A"); err != nil {
		t.Fatalf("recovery first call error = %v", err)
	}
	assertRateLimitBlockedError(t, gate.Check(rateLimitSceneRecoveryVerify, "subject-A", "fp-A"))

	if err := gate.Check(rateLimitScenePairingReset, "subject-A", "fp-A"); err != nil {
		t.Fatalf("pairing reset first call error = %v", err)
	}
	assertRateLimitBlockedError(t, gate.Check(rateLimitScenePairingReset, "subject-A", "fp-A"))

	if err := gate.Check(rateLimitSceneWebPairBind, "subject-A", "fp-A"); err != nil {
		t.Fatalf("web bind first call error = %v", err)
	}
	assertRateLimitBlockedError(t, gate.Check(rateLimitSceneWebPairBind, "subject-A", "fp-A"))
}

func TestMigrationRateGateDefaultPoliciesMatchContract(t *testing.T) {
	gate := newMigrationRateGate()
	tests := []struct {
		scene            string
		wantWindow       time.Duration
		wantFineLimit    int
		wantSubjectLimit int
		wantClientLimit  int
		wantFineBlock    time.Duration
		wantGlobalBlock  time.Duration
	}{
		{
			scene:            rateLimitScenePairingReset,
			wantWindow:       24 * time.Hour,
			wantFineLimit:    3,
			wantSubjectLimit: 5,
			wantClientLimit:  5,
			wantFineBlock:    24 * time.Hour,
			wantGlobalBlock:  72 * time.Hour,
		},
		{
			scene:            rateLimitSceneRecoveryVerify,
			wantWindow:       24 * time.Hour,
			wantFineLimit:    3,
			wantSubjectLimit: 5,
			wantClientLimit:  5,
			wantFineBlock:    72 * time.Hour,
			wantGlobalBlock:  7 * 24 * time.Hour,
		},
		{
			scene:            rateLimitSceneWebPairBind,
			wantWindow:       10 * time.Minute,
			wantFineLimit:    5,
			wantSubjectLimit: 15,
			wantClientLimit:  15,
			wantFineBlock:    30 * time.Minute,
			wantGlobalBlock:  2 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.scene, func(t *testing.T) {
			policy, ok := gate.policies[tc.scene]
			if !ok {
				t.Fatalf("missing policy for scene %s", tc.scene)
			}
			if policy.Window != tc.wantWindow {
				t.Fatalf("window = %s, want %s", policy.Window, tc.wantWindow)
			}
			if policy.FineLimit != tc.wantFineLimit {
				t.Fatalf("fine_limit = %d, want %d", policy.FineLimit, tc.wantFineLimit)
			}
			if policy.SubjectGlobalLimit != tc.wantSubjectLimit {
				t.Fatalf("subject_limit = %d, want %d", policy.SubjectGlobalLimit, tc.wantSubjectLimit)
			}
			if policy.ClientGlobalLimit != tc.wantClientLimit {
				t.Fatalf("client_limit = %d, want %d", policy.ClientGlobalLimit, tc.wantClientLimit)
			}
			if policy.FineBlock != tc.wantFineBlock {
				t.Fatalf("fine_block = %s, want %s", policy.FineBlock, tc.wantFineBlock)
			}
			if policy.GlobalBlock != tc.wantGlobalBlock {
				t.Fatalf("global_block = %s, want %s", policy.GlobalBlock, tc.wantGlobalBlock)
			}
		})
	}
}

func TestMigrationRateLimitBlockedResponseContainsRequestID(t *testing.T) {
	now := time.Date(2026, 2, 13, 14, 1, 0, 0, time.UTC)
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(0),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(10),
	}, now)
	server.handle("/internal/migrations/rate-limit", func(http.ResponseWriter, *http.Request) error {
		return server.checkMigrationRequestRateLimit("subject-A", "fp-A")
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/rate-limit", strings.NewReader("{}"))
	request.Header.Set(requestIDHeader, "req-rate-limit-001")
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != rateLimitBlockedCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.RequestID != "req-rate-limit-001" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
	if strings.TrimSpace(payload.Message) == "" {
		t.Fatal("message is empty")
	}
}

func TestMigrationRateLimitPassDoesNotAffectBusinessFlow(t *testing.T) {
	now := time.Date(2026, 2, 13, 14, 2, 0, 0, time.UTC)
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(10),
	}, now)
	server.handle("/internal/migrations/rate-pass", func(w http.ResponseWriter, r *http.Request) error {
		if err := server.checkMigrationConfirmRateLimit("subject-B", "fp-B"); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/migrations/rate-pass", strings.NewReader("{}"))
	request.Header.Set(requestIDHeader, "req-rate-pass-001")
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("status payload = %q", payload["status"])
	}
}

func newTestMigrationRateGate(policies map[string]migrationRateLimitPolicy, now time.Time) *migrationRateGate {
	return &migrationRateGate{
		now:      func() time.Time { return now },
		policies: policies,
		counters: make(map[string]*rateLimitCounter),
	}
}

func testPolicy(fineLimit int) migrationRateLimitPolicy {
	return migrationRateLimitPolicy{
		Window:             time.Hour,
		FineLimit:          fineLimit,
		SubjectGlobalLimit: 100,
		ClientGlobalLimit:  100,
		FineBlock:          time.Hour,
		GlobalBlock:        time.Hour,
	}
}

func assertRateLimitBlockedError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status = %d", apiErr.StatusCode())
	}
	if apiErr.Code != rateLimitBlockedCode {
		t.Fatalf("error_code = %q", apiErr.Code)
	}
}

func TestBatch2RateLimitCheckHelpersReturnBlockedError(t *testing.T) {
	now := time.Date(2026, 2, 13, 14, 3, 0, 0, time.UTC)
	server := NewServer("127.0.0.1:0", healthyDB{})
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(0),
		rateLimitSceneWebPairBind:      testPolicy(0),
	}, now)

	assertRateLimitBlockedError(t, server.checkPairingResetRateLimit("subject-C", "fp-C"))
	assertRateLimitBlockedError(t, server.checkWebPairBindRateLimit("subject-C", "fp-C"))
}

func TestBatch2RateLimitPrioritiesByScene(t *testing.T) {
	scenes := []string{
		rateLimitScenePairingReset,
		rateLimitSceneRecoveryVerify,
		rateLimitSceneWebPairBind,
	}

	for _, scene := range scenes {
		t.Run(scene+"/fine", func(t *testing.T) {
			now := time.Date(2026, 2, 13, 14, 4, 0, 0, time.UTC)
			gate := newMigrationRateGate()
			gate.now = func() time.Time { return now }
			policy := gate.policies[scene]

			subject := "subject-fine"
			fingerprint := "fp-fine"
			for i := 0; i < policy.FineLimit; i++ {
				if err := gate.Check(scene, subject, fingerprint); err != nil {
					t.Fatalf("attempt %d unexpected error: %v", i+1, err)
				}
			}
			assertRateLimitBlockedError(t, gate.Check(scene, subject, fingerprint))

			fineKey := fmt.Sprintf("%s|%s|%s", scene, subject, fingerprint)
			subjectGlobalKey := fmt.Sprintf("%s|%s|GLOBAL", scene, subject)
			clientGlobalKey := fmt.Sprintf("%s|GLOBAL|%s", scene, fingerprint)
			assertCounterBlockedUntil(t, gate, fineKey, now.Add(policy.FineBlock))
			assertCounterNotBlocked(t, gate, subjectGlobalKey)
			assertCounterNotBlocked(t, gate, clientGlobalKey)
		})

		t.Run(scene+"/subject_global", func(t *testing.T) {
			now := time.Date(2026, 2, 13, 14, 5, 0, 0, time.UTC)
			gate := newMigrationRateGate()
			gate.now = func() time.Time { return now }
			policy := gate.policies[scene]

			subject := "subject-global"
			for i := 0; i < policy.SubjectGlobalLimit; i++ {
				fingerprint := fmt.Sprintf("fp-subject-%d", i)
				if err := gate.Check(scene, subject, fingerprint); err != nil {
					t.Fatalf("attempt %d unexpected error: %v", i+1, err)
				}
			}
			assertRateLimitBlockedError(t, gate.Check(scene, subject, fmt.Sprintf("fp-subject-%d", policy.SubjectGlobalLimit)))

			subjectGlobalKey := fmt.Sprintf("%s|%s|GLOBAL", scene, subject)
			assertCounterBlockedUntil(t, gate, subjectGlobalKey, now.Add(policy.GlobalBlock))
		})

		t.Run(scene+"/client_global", func(t *testing.T) {
			now := time.Date(2026, 2, 13, 14, 6, 0, 0, time.UTC)
			gate := newMigrationRateGate()
			gate.now = func() time.Time { return now }
			policy := gate.policies[scene]

			fingerprint := "fp-global"
			for i := 0; i < policy.ClientGlobalLimit; i++ {
				subject := fmt.Sprintf("subject-client-%d", i)
				if err := gate.Check(scene, subject, fingerprint); err != nil {
					t.Fatalf("attempt %d unexpected error: %v", i+1, err)
				}
			}
			assertRateLimitBlockedError(t, gate.Check(scene, fmt.Sprintf("subject-client-%d", policy.ClientGlobalLimit), fingerprint))

			clientGlobalKey := fmt.Sprintf("%s|GLOBAL|%s", scene, fingerprint)
			assertCounterBlockedUntil(t, gate, clientGlobalKey, now.Add(policy.GlobalBlock))
		})
	}
}

func assertCounterBlockedUntil(t *testing.T, gate *migrationRateGate, key string, want time.Time) {
	t.Helper()

	counter, ok := gate.counters[key]
	if !ok {
		t.Fatalf("counter %q not found", key)
	}
	if !counter.BlockedUntil.Equal(want) {
		t.Fatalf("counter %q blocked_until=%s want=%s", key, counter.BlockedUntil, want)
	}
}

func assertCounterNotBlocked(t *testing.T, gate *migrationRateGate, key string) {
	t.Helper()

	counter, ok := gate.counters[key]
	if !ok {
		t.Fatalf("counter %q not found", key)
	}
	if !counter.BlockedUntil.IsZero() {
		t.Fatalf("counter %q unexpectedly blocked_until=%s", key, counter.BlockedUntil)
	}
}
