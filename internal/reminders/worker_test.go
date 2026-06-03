package reminders

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendNotificationProtocolAndSuccess(t *testing.T) {
	var gotMethod, gotContentType, gotAuth, gotKey, gotBody string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-Idempotency-Key")
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	message := "下班提醒\n已接近标准工时\n记得下班打卡"
	if err := SendNotification(context.Background(), client, server.URL, "secret-token", "evt-1", message); err != nil {
		t.Fatalf("SendNotification() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotContentType != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotKey != "evt-1" {
		t.Fatalf("idempotency key = %q", gotKey)
	}
	if gotBody != message {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestSendNotificationErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		client     HTTPClient
		wantCode   string
	}{
		{name: "4xx", statusCode: http.StatusUnauthorized, wantCode: errorCodeHTTP},
		{name: "5xx", statusCode: http.StatusInternalServerError, wantCode: errorCodeHTTP},
		{name: "network", client: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial https://notify.example/hook token secret-token")
		}), wantCode: errorCodeNetwork},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.client
			cleanup := func() {}
			url := "https://notify.example/hook"
			if client == nil {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.statusCode)
				}))
				cleanup = server.Close
				url = server.URL
				client = server.Client()
			}
			defer cleanup()

			err := SendNotification(context.Background(), client, url, "secret-token", "evt-err", "body")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := errorCodeForSendError(err); got != tt.wantCode {
				t.Fatalf("error code = %q", got)
			}
		})
	}
}

func TestHTTPClientDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			redirected = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	client := NewHTTPClient(time.Second)
	client.client.Transport = server.Client().Transport
	err := SendNotification(context.Background(), client, server.URL, "secret-token", "evt-redirect", "body")
	if err == nil {
		t.Fatal("expected redirect response error, got nil")
	}
	if redirected {
		t.Fatal("client followed redirect")
	}
}

func TestWorkerProcessOutcomes(t *testing.T) {
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		target     SendTarget
		client     HTTPClient
		wantStatus string
		wantReason string
		wantCalls  int
		wantNext   bool
	}{
		{
			name:       "2xx sent",
			target:     enabledTarget(),
			client:     staticHTTPClient(http.StatusNoContent, nil),
			wantStatus: StatusSent,
			wantCalls:  1,
		},
		{
			name:       "4xx failed",
			target:     enabledTarget(),
			client:     staticHTTPClient(http.StatusBadRequest, nil),
			wantStatus: StatusFailed,
			wantCalls:  1,
			wantNext:   true,
		},
		{
			name:       "5xx failed",
			target:     enabledTarget(),
			client:     staticHTTPClient(http.StatusBadGateway, nil),
			wantStatus: StatusFailed,
			wantCalls:  1,
			wantNext:   true,
		},
		{
			name:       "timeout failed",
			target:     enabledTarget(),
			client:     staticHTTPClient(0, context.DeadlineExceeded),
			wantStatus: StatusFailed,
			wantCalls:  1,
			wantNext:   true,
		},
		{
			name:       "network failed",
			target:     enabledTarget(),
			client:     staticHTTPClient(0, errors.New("dial https://notify.example/hook Bearer secret-token")),
			wantStatus: StatusFailed,
			wantCalls:  1,
			wantNext:   true,
		},
		{
			name:       "missing config cancelled",
			target:     SendTarget{},
			client:     staticHTTPClient(http.StatusNoContent, nil),
			wantStatus: StatusCancelled,
			wantReason: cancelReasonConfigDisabled,
		},
		{
			name:       "disabled config cancelled",
			target:     SendTarget{Configured: true, Enabled: false, URL: "https://notify.example/hook", Token: "secret-token"},
			client:     staticHTTPClient(http.StatusNoContent, nil),
			wantStatus: StatusCancelled,
			wantReason: cancelReasonConfigDisabled,
		},
		{
			name:       "end before send skipped",
			target:     SendTarget{Configured: true, Enabled: true, HasEnd: true, URL: "https://notify.example/hook", Token: "secret-token"},
			client:     staticHTTPClient(http.StatusNoContent, nil),
			wantStatus: StatusSkipped,
			wantReason: cancelReasonEndSynced,
		},
		{
			name:       "invalid url cancelled",
			target:     SendTarget{Configured: true, Enabled: true, URL: "http://notify.example/hook", Token: "secret-token"},
			client:     staticHTTPClient(http.StatusNoContent, nil),
			wantStatus: StatusCancelled,
			wantReason: cancelReasonURLInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemoryStore()
			store.events["evt-1"] = memoryEvent{
				Event:  baseEvent("evt-1"),
				status: StatusPending,
				target: tt.target,
			}
			worker := NewWorker(store, tt.client, testWorkerConfig())
			worker.now = func() time.Time { return now }

			if err := worker.ScanOnce(context.Background()); err != nil {
				t.Fatalf("ScanOnce() error = %v", err)
			}

			got := store.events["evt-1"]
			if got.status != tt.wantStatus {
				t.Fatalf("status = %q", got.status)
			}
			if got.reason != tt.wantReason {
				t.Fatalf("reason = %q", got.reason)
			}
			if got.calls != tt.wantCalls {
				t.Fatalf("calls = %d", got.calls)
			}
			if (got.nextRetryAt != nil) != tt.wantNext {
				t.Fatalf("next retry present = %v", got.nextRetryAt != nil)
			}
			if strings.Contains(got.lastErrorMessage, "secret-token") || strings.Contains(got.lastErrorMessage, "https://notify.example/hook") {
				t.Fatalf("last error leaked secret: %q", got.lastErrorMessage)
			}
		})
	}
}

func TestWorkerClaimSendingExpiredAndFailedRetryBoundary(t *testing.T) {
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	store.events["pending"] = memoryEvent{Event: baseEvent("pending"), status: StatusPending, scheduledAt: now.Add(-time.Minute), target: enabledTarget()}
	store.events["failed-ready"] = memoryEvent{Event: baseEvent("failed-ready"), status: StatusFailed, nextRetryAt: ptrTime(now), target: enabledTarget()}
	store.events["failed-max"] = memoryEvent{Event: baseEvent("failed-max"), status: StatusFailed, nextRetryAt: ptrTime(now), target: enabledTarget(), attemptCount: 3}
	store.events["sending-expired"] = memoryEvent{Event: baseEvent("sending-expired"), status: StatusSending, lockedUntil: ptrTime(now.Add(-time.Second)), target: enabledTarget()}
	store.events["sending-locked"] = memoryEvent{Event: baseEvent("sending-locked"), status: StatusSending, lockedUntil: ptrTime(now.Add(time.Minute)), target: enabledTarget()}

	worker := NewWorker(store, staticHTTPClient(http.StatusNoContent, nil), testWorkerConfig())
	worker.now = func() time.Time { return now }
	if err := worker.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}

	for _, id := range []string{"pending", "failed-ready", "sending-expired"} {
		if store.events[id].status != StatusSent {
			t.Fatalf("%s status = %q", id, store.events[id].status)
		}
	}
	for _, id := range []string{"failed-max", "sending-locked"} {
		if store.events[id].status == StatusSent {
			t.Fatalf("%s should not be sent", id)
		}
	}
}

func TestWorkerTerminalFailedAfterMaxRetry(t *testing.T) {
	now := time.Date(2026, 2, 12, 13, 59, 0, 0, time.UTC)
	store := newMemoryStore()
	event := baseEvent("evt-300")
	event.ReminderType = TypeAdjustReminder
	event.AdjustMinutes = 300
	event.ScheduledAfterStartMinutes = 839
	store.events[event.ID] = memoryEvent{Event: event, status: StatusFailed, scheduledAt: now.Add(-time.Minute), nextRetryAt: ptrTime(now), target: enabledTarget(), attemptCount: 2}

	worker := NewWorker(store, staticHTTPClient(http.StatusBadGateway, nil), testWorkerConfig())
	worker.now = func() time.Time { return now }
	if err := worker.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}

	got := store.events[event.ID]
	if got.status != StatusFailed || got.attemptCount != 3 {
		t.Fatalf("status/attempt = %s/%d", got.status, got.attemptCount)
	}
	if got.nextRetryAt != nil {
		t.Fatal("terminal failed should not schedule next retry")
	}
	if len(store.events) != 1 {
		t.Fatalf("unexpected new event count = %d", len(store.events))
	}
}

func TestWorkerConcurrentClaimSendsOnce(t *testing.T) {
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	store.events["evt-1"] = memoryEvent{Event: baseEvent("evt-1"), status: StatusPending, scheduledAt: now.Add(-time.Minute), target: enabledTarget()}
	client := &countingClient{statusCode: http.StatusNoContent}
	worker := NewWorker(store, client, testWorkerConfig())
	worker.now = func() time.Time { return now }

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- worker.ScanOnce(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ScanOnce() error = %v", err)
		}
	}
	if client.calls != 1 {
		t.Fatalf("send calls = %d", client.calls)
	}
	if store.events["evt-1"].status != StatusSent {
		t.Fatalf("status = %q", store.events["evt-1"].status)
	}
}

func TestWorkerExpiredClaimCannotOverwriteNewClaim(t *testing.T) {
	now := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore()
	store.events["evt-1"] = memoryEvent{
		Event:       baseEvent("evt-1"),
		status:      StatusSending,
		scheduledAt: now.Add(-time.Minute),
		lockedUntil: ptrTime(now.Add(-time.Second)),
		target:      enabledTarget(),
	}

	newClaims, err := store.ClaimDue(context.Background(), now, 1, 3, time.Minute)
	if err != nil {
		t.Fatalf("new ClaimDue() error = %v", err)
	}
	if len(newClaims) != 1 {
		t.Fatalf("new claims = %d", len(newClaims))
	}
	newLease := newClaims[0].ClaimLockedUntil
	oldClaim := baseEvent("evt-1")
	oldClaim.ClaimLockedUntil = now.Add(-time.Second)

	if err := store.MarkSent(context.Background(), oldClaim, now); !errors.Is(err, ErrClaimExpired) {
		t.Fatalf("old MarkSent() err = %v", err)
	}
	got := store.events["evt-1"]
	if got.status != StatusSending {
		t.Fatalf("status after old write = %q", got.status)
	}
	if got.lockedUntil == nil || !got.lockedUntil.Equal(newLease) {
		t.Fatalf("lock after old write = %v, want %v", got.lockedUntil, newLease)
	}

	if err := store.MarkFailed(context.Background(), newClaims[0], 1, 3, now.Add(time.Minute), "HTTP_STATUS", "failed", now); err != nil {
		t.Fatalf("new MarkFailed() error = %v", err)
	}
	got = store.events["evt-1"]
	if got.status != StatusFailed || got.attemptCount != 1 {
		t.Fatalf("status/attempt after new write = %s/%d", got.status, got.attemptCount)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func staticHTTPClient(status int, err error) *countingClient {
	return &countingClient{statusCode: status, err: err}
}

type countingClient struct {
	mu         sync.Mutex
	statusCode int
	err        error
	calls      int
}

func (c *countingClient) Do(*http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &http.Response{
		StatusCode: c.statusCode,
		Body:       ioNopCloser{strings.NewReader("response")},
	}, nil
}

type ioNopCloser struct {
	*strings.Reader
}

func (c ioNopCloser) Close() error { return nil }

type memoryStore struct {
	mu     sync.Mutex
	events map[string]memoryEvent
}

type memoryEvent struct {
	Event
	status           string
	reason           string
	scheduledAt      time.Time
	nextRetryAt      *time.Time
	lockedUntil      *time.Time
	target           SendTarget
	calls            int
	attemptCount     int
	lastErrorMessage string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{events: make(map[string]memoryEvent)}
}

func (s *memoryStore) ClaimDue(_ context.Context, now time.Time, limit, maxRetry int, lockFor time.Duration) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := make([]Event, 0, limit)
	for id, event := range s.events {
		if len(events) >= limit {
			break
		}
		due := (event.status == StatusPending && !event.scheduledAt.After(now)) ||
			(event.status == StatusFailed && event.nextRetryAt != nil && !event.nextRetryAt.After(now) && event.attemptCount < maxRetry) ||
			(event.status == StatusSending && event.lockedUntil != nil && !event.lockedUntil.After(now))
		if !due {
			continue
		}
		event.status = StatusSending
		lockUntil := now.Add(lockFor)
		event.lockedUntil = &lockUntil
		event.Event.AttemptCount = event.attemptCount
		event.Event.ClaimLockedUntil = lockUntil
		s.events[id] = event
		events = append(events, event.Event)
	}
	return events, nil
}

func (s *memoryStore) LoadSendTarget(_ context.Context, event Event) (SendTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[event.ID].target, nil
}

func (s *memoryStore) MarkSent(_ context.Context, event Event, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.events[event.ID]
	if stored.lockedUntil == nil || !stored.lockedUntil.Equal(event.ClaimLockedUntil) {
		return ErrClaimExpired
	}
	stored.status = StatusSent
	stored.lockedUntil = nil
	stored.calls++
	s.events[event.ID] = stored
	return nil
}

func (s *memoryStore) MarkFailed(_ context.Context, event Event, attemptCount, maxRetry int, nextRetryAt time.Time, _, message string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.events[event.ID]
	if stored.lockedUntil == nil || !stored.lockedUntil.Equal(event.ClaimLockedUntil) {
		return ErrClaimExpired
	}
	stored.status = StatusFailed
	stored.attemptCount = attemptCount
	stored.lockedUntil = nil
	stored.lastErrorMessage = message
	if attemptCount < maxRetry {
		stored.nextRetryAt = &nextRetryAt
	} else {
		stored.nextRetryAt = nil
	}
	stored.calls++
	s.events[event.ID] = stored
	return nil
}

func (s *memoryStore) MarkCancelled(_ context.Context, event Event, reason string, _ time.Time) error {
	return s.mark(event, StatusCancelled, reason)
}

func (s *memoryStore) MarkSkipped(_ context.Context, event Event, reason string, _ time.Time) error {
	return s.mark(event, StatusSkipped, reason)
}

func (s *memoryStore) mark(event Event, status, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.events[event.ID]
	if stored.lockedUntil == nil || !stored.lockedUntil.Equal(event.ClaimLockedUntil) {
		return ErrClaimExpired
	}
	stored.status = status
	stored.reason = reason
	stored.lockedUntil = nil
	s.events[event.ID] = stored
	return nil
}

func baseEvent(id string) Event {
	return Event{
		ID:                         id,
		UserID:                     "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
		LocalDate:                  "2026-02-12",
		ReminderType:               TypeEndReminder,
		AdjustMinutes:              0,
		ScheduledAfterStartMinutes: 539,
		Message:                    BuildMessage(TypeEndReminder, 0),
	}
}

func enabledTarget() SendTarget {
	return SendTarget{
		Configured: true,
		Enabled:    true,
		URL:        "https://notify.example/hook",
		Token:      "secret-token",
	}
}

func testWorkerConfig() Config {
	return Config{
		ScanInterval: time.Hour,
		BatchSize:    10,
		HTTPTimeout:  time.Second,
		MaxRetry:     3,
		RetryBackoff: time.Minute,
		LockDuration: time.Minute,
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
