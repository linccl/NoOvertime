package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const validRecoveryCodeGeneratePayload = `{"require_first_time":true}`

func TestRecoveryCodeGenerateRouteSuccess(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
		updatedAt: time.Date(2026, 2, 13, 15, 0, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateRecoveryCode
	generateRecoveryCode = func() (string, error) { return "AB12CD34EF56GH78", nil }
	defer func() { generateRecoveryCode = original }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-success")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body recoveryCodeGenerateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RecoveryCode != "AB12CD34EF56GH78" {
		t.Fatalf("recovery_code = %q", body.RecoveryCode)
	}
	if body.RecoveryCodeMasked != "AB12********GH78" {
		t.Fatalf("recovery_code_masked = %q", body.RecoveryCodeMasked)
	}
	if !body.ShownOnce {
		t.Fatal("shown_once = false, want true")
	}
	if body.UpdatedAt != "2026-02-13T15:00:00Z" {
		t.Fatalf("updated_at = %q", body.UpdatedAt)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeGenerateRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(`{"require_first_time":true,"unknown":"x"}`))
	req.Header.Set(requestIDHeader, "req-recovery-generate-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-recovery-generate-unknown")
}

func TestRecoveryCodeGenerateRouteInvalidArgument(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	tests := []struct {
		name string
		body string
	}{
		{name: "missing require_first_time", body: `{}`},
		{name: "require_first_time false", body: `{"require_first_time":false}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(tc.body))
			req.Header.Set(requestIDHeader, "req-recovery-generate-invalid")
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-recovery-generate-invalid")
		})
	}
}

func TestRecoveryCodeGenerateRouteUnauthorizedDevice(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-unauthorized")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedDeviceCode, "req-recovery-generate-unauthorized")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeGenerateRouteAlreadyInitialized(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$mockedhash",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-initialized")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, recoveryCodeAlreadyInitializedCode, "req-recovery-generate-initialized")
}

func TestRecoveryCodeGenerateRouteRateLimitBlocked(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(0),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(10),
	}, time.Date(2026, 2, 13, 15, 1, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-rate-limit")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-recovery-generate-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeGenerateRouteStaleWriterRejected(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "",
			WriterDeviceID:   "ffffffff-ffff-ffff-ffff-ffffffffffff",
			WriterEpoch:      99,
			DeviceAuthorized: true,
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-stale")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, staleWriterRejectedCode, "req-recovery-generate-stale")
}

func TestRecoveryCodeGenerateRouteUserNotFound(t *testing.T) {
	db := &fakeRecoveryCodeGenerateDB{
		loadErr: pgx.ErrNoRows,
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeGeneratePath, strings.NewReader(validRecoveryCodeGeneratePayload))
	req.Header.Set(requestIDHeader, "req-recovery-generate-user-not-found")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, userNotFoundCode, "req-recovery-generate-user-not-found")
}

type fakeRecoveryCodeGenerateDB struct {
	snapshot    recoveryCodeGenerateSnapshot
	loadErr     error
	updateErr   error
	updatedAt   time.Time
	withTxCalls int
}

func (f *fakeRecoveryCodeGenerateDB) Health(context.Context) error {
	return nil
}

func (f *fakeRecoveryCodeGenerateDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeRecoveryCodeGenerateTx{db: f})
}

type fakeRecoveryCodeGenerateTx struct {
	db *fakeRecoveryCodeGenerateDB
}

type fakeRecoveryGenerateRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRecoveryGenerateRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeRecoveryCodeGenerateTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRecoveryCodeGenerateTx) Commit(context.Context) error   { return nil }
func (f *fakeRecoveryCodeGenerateTx) Rollback(context.Context) error { return nil }
func (f *fakeRecoveryCodeGenerateTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeRecoveryCodeGenerateTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeRecoveryCodeGenerateTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeRecoveryCodeGenerateTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeRecoveryCodeGenerateTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeRecoveryCodeGenerateTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeRecoveryCodeGenerateTx) Conn() *pgx.Conn { return nil }

func (f *fakeRecoveryCodeGenerateTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "SELECT u.recovery_code_hash"):
		return fakeRecoveryGenerateRow{scanFn: func(dest ...any) error {
			if f.db.loadErr != nil {
				return f.db.loadErr
			}
			if len(dest) != 4 {
				return errors.New("invalid destination fields for snapshot")
			}
			hashPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			writerDevicePtr, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			writerEpochPtr, ok := dest[2].(*int64)
			if !ok {
				return errors.New("dest[2] must be *int64")
			}
			authorizedPtr, ok := dest[3].(*bool)
			if !ok {
				return errors.New("dest[3] must be *bool")
			}

			*hashPtr = f.db.snapshot.RecoveryCodeHash
			*writerDevicePtr = f.db.snapshot.WriterDeviceID
			*writerEpochPtr = f.db.snapshot.WriterEpoch
			*authorizedPtr = f.db.snapshot.DeviceAuthorized
			return nil
		}}
	case strings.Contains(query, "UPDATE users") && strings.Contains(query, "recovery_code_hash = crypt"):
		return fakeRecoveryGenerateRow{scanFn: func(dest ...any) error {
			if f.db.updateErr != nil {
				return f.db.updateErr
			}
			if len(dest) != 1 {
				return errors.New("invalid destination fields for updated_at")
			}
			updatedAtPtr, ok := dest[0].(*time.Time)
			if !ok {
				return errors.New("dest[0] must be *time.Time")
			}
			*updatedAtPtr = f.db.updatedAt
			return nil
		}}
	default:
		return fakeRecoveryGenerateRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
