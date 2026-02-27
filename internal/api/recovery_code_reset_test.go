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

const validRecoveryCodeResetForcePayload = `{"force_reset":true}`
const validRecoveryCodeResetVerifyPayload = `{"old_recovery_code":"ab12cd34ef56gh78","force_reset":false}`

func TestRecoveryCodeResetRouteSuccessForceReset(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$existinghash",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
		updatedAt: time.Date(2026, 2, 13, 16, 0, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateRecoveryCode
	generateRecoveryCode = func() (string, error) { return "K9PQ41MS77TX8N2D", nil }
	defer func() { generateRecoveryCode = original }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetForcePayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-force")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body recoveryCodeResetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RecoveryCode != "K9PQ41MS77TX8N2D" {
		t.Fatalf("recovery_code = %q", body.RecoveryCode)
	}
	if body.RecoveryCodeMasked != "K9PQ********8N2D" {
		t.Fatalf("recovery_code_masked = %q", body.RecoveryCodeMasked)
	}
	if !body.ShownOnce {
		t.Fatal("shown_once = false, want true")
	}
	if body.UpdatedAt != "2026-02-13T16:00:00Z" {
		t.Fatalf("updated_at = %q", body.UpdatedAt)
	}
	if db.verifyCalls != 0 {
		t.Fatalf("verifyCalls = %d, want 0", db.verifyCalls)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeResetRouteSuccessVerifyOldCodeCaseInsensitive(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$existinghash",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
		verifyMatched: true,
		updatedAt:     time.Date(2026, 2, 13, 16, 1, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateRecoveryCode
	generateRecoveryCode = func() (string, error) { return "AB12CD34EF56GH78", nil }
	defer func() { generateRecoveryCode = original }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetVerifyPayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-verify")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if db.verifyCalls != 1 {
		t.Fatalf("verifyCalls = %d", db.verifyCalls)
	}
	if db.lastVerifyCode != "AB12CD34EF56GH78" {
		t.Fatalf("lastVerifyCode = %q", db.lastVerifyCode)
	}
}

func TestRecoveryCodeResetRouteOldCodeInvalid(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$existinghash",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
		verifyMatched: false,
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetVerifyPayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-invalid-old")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, recoveryCodeInvalidCode, "req-recovery-reset-invalid-old")
}

func TestRecoveryCodeResetRouteUnauthorizedDevice(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetForcePayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-unauthorized")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedDeviceCode, "req-recovery-reset-unauthorized")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeResetRouteRateLimitBlocked(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(0),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(10),
	}, time.Date(2026, 2, 13, 16, 2, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetForcePayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-rate-limit")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-recovery-reset-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestRecoveryCodeResetRouteInvalidArgumentWhenForceResetFalseWithoutOldCode(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(`{"force_reset":false}`))
	req.Header.Set(requestIDHeader, "req-recovery-reset-missing-old")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-recovery-reset-missing-old")
}

func TestRecoveryCodeResetRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(`{"force_reset":true,"unknown":"x"}`))
	req.Header.Set(requestIDHeader, "req-recovery-reset-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-recovery-reset-unknown")
}

func TestRecoveryCodeResetRouteStaleWriterRejected(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$existinghash",
			WriterDeviceID:   "ffffffff-ffff-ffff-ffff-ffffffffffff",
			WriterEpoch:      99,
			DeviceAuthorized: true,
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetForcePayload))
	req.Header.Set(requestIDHeader, "req-recovery-reset-stale")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, staleWriterRejectedCode, "req-recovery-reset-stale")
}

func TestRecoveryCodeResetRouteOldCodeInvalidAfterSuccessfulReset(t *testing.T) {
	db := &fakeRecoveryCodeResetDB{
		snapshot: recoveryCodeGenerateSnapshot{
			RecoveryCodeHash: "$2a$10$existinghash",
			WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:      12,
			DeviceAuthorized: true,
		},
		verifyUseCurrentCode: true,
		currentRecoveryCode:  "AB12CD34EF56GH78",
		updatedAt:            time.Date(2026, 2, 13, 16, 3, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateRecoveryCode
	generateRecoveryCode = func() (string, error) { return "ZZ99YY88XX77WW66", nil }
	defer func() { generateRecoveryCode = original }()

	firstRec := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetVerifyPayload))
	firstReq.Header.Set(requestIDHeader, "req-recovery-reset-first-success")
	setPairingQueryAuthHeaders(firstReq, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", firstRec.Code, firstRec.Body.String())
	}

	secondRec := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, recoveryCodeResetPath, strings.NewReader(validRecoveryCodeResetVerifyPayload))
	secondReq.Header.Set(requestIDHeader, "req-recovery-reset-old-after-rotated")
	setPairingQueryAuthHeaders(secondReq, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s", secondRec.Code, secondRec.Body.String())
	}
	assertErrorEnvelope(t, secondRec, http.StatusConflict, recoveryCodeInvalidCode, "req-recovery-reset-old-after-rotated")
}

type fakeRecoveryCodeResetDB struct {
	snapshot             recoveryCodeGenerateSnapshot
	loadErr              error
	verifyMatched        bool
	verifyErr            error
	updateErr            error
	updatedAt            time.Time
	withTxCalls          int
	verifyCalls          int
	lastVerifyCode       string
	verifyUseCurrentCode bool
	currentRecoveryCode  string
}

func (f *fakeRecoveryCodeResetDB) Health(context.Context) error {
	return nil
}

func (f *fakeRecoveryCodeResetDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeRecoveryCodeResetTx{db: f})
}

type fakeRecoveryCodeResetTx struct {
	db *fakeRecoveryCodeResetDB
}

type fakeRecoveryResetRow struct {
	scanFn func(dest ...any) error
}

func (r fakeRecoveryResetRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeRecoveryCodeResetTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRecoveryCodeResetTx) Commit(context.Context) error   { return nil }
func (f *fakeRecoveryCodeResetTx) Rollback(context.Context) error { return nil }
func (f *fakeRecoveryCodeResetTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeRecoveryCodeResetTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeRecoveryCodeResetTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeRecoveryCodeResetTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeRecoveryCodeResetTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeRecoveryCodeResetTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeRecoveryCodeResetTx) Conn() *pgx.Conn { return nil }

func (f *fakeRecoveryCodeResetTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch {
	case strings.Contains(query, "SELECT u.recovery_code_hash"):
		return fakeRecoveryResetRow{scanFn: func(dest ...any) error {
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
	case strings.Contains(query, "SELECT CASE"):
		return fakeRecoveryResetRow{scanFn: func(dest ...any) error {
			if f.db.verifyErr != nil {
				return f.db.verifyErr
			}
			if len(dest) != 1 {
				return errors.New("invalid destination fields for verify")
			}
			matchedPtr, ok := dest[0].(*bool)
			if !ok {
				return errors.New("dest[0] must be *bool")
			}
			f.db.verifyCalls++
			if len(args) >= 2 {
				if code, ok := args[1].(string); ok {
					f.db.lastVerifyCode = code
					if f.db.verifyUseCurrentCode {
						*matchedPtr = code == f.db.currentRecoveryCode
						return nil
					}
				}
			}
			*matchedPtr = f.db.verifyMatched
			return nil
		}}
	case strings.Contains(query, "UPDATE users") && strings.Contains(query, "recovery_code_hash = crypt"):
		return fakeRecoveryResetRow{scanFn: func(dest ...any) error {
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
			if f.db.verifyUseCurrentCode && len(args) >= 2 {
				if code, ok := args[1].(string); ok {
					f.db.currentRecoveryCode = code
				}
			}
			*updatedAtPtr = f.db.updatedAt
			return nil
		}}
	default:
		return fakeRecoveryResetRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
