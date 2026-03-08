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

const validPairingCodeResetPayload = `{"reason":"USER_INITIATED"}`

func TestPairingCodeResetRouteSuccess(t *testing.T) {
	db := &fakePairingCodeResetDB{
		snapshot: pairingCodeQuerySnapshot{
			PairingCode:        "39481726",
			PairingCodeVersion: 3,
			PairingCodeAt:      time.Date(2026, 2, 13, 13, 0, 0, 0, time.UTC),
			WriterDeviceID:     "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:        12,
			DeviceAuthorized:   true,
		},
		activeBindings: 2,
		currentCode:    "24069175",
		currentVersion: 4,
		currentAt:      time.Date(2026, 2, 13, 13, 5, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(validPairingCodeResetPayload))
	req.Header.Set(requestIDHeader, "req-pairing-reset-success")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body pairingCodeResetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PairingCode != "24069175" {
		t.Fatalf("pairing_code = %q", body.PairingCode)
	}
	if body.PairingCodeVersion != 4 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.PairingCodeUpdatedAt != "2026-02-13T13:05:00Z" {
		t.Fatalf("pairing_code_updated_at = %q", body.PairingCodeUpdatedAt)
	}
	if body.RevokedBindingsCount != 2 {
		t.Fatalf("revoked_bindings_count = %d", body.RevokedBindingsCount)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.rotateCalls != 1 {
		t.Fatalf("rotateCalls = %d", db.rotateCalls)
	}
}

func TestPairingCodeResetRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(`{"reason":"USER_INITIATED","unknown":"x"}`))
	req.Header.Set(requestIDHeader, "req-pairing-reset-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-pairing-reset-unknown")
}

func TestPairingCodeResetRouteInvalidArgument(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(`{"reason":"SYSTEM"}`))
	req.Header.Set(requestIDHeader, "req-pairing-reset-invalid")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-pairing-reset-invalid")
}

func TestPairingCodeResetRouteUnauthorizedDevice(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(validPairingCodeResetPayload))
	req.Header.Set(requestIDHeader, "req-pairing-reset-unauthorized")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedMobileTokenCode, "req-pairing-reset-unauthorized")
}

func TestPairingCodeResetRouteRateLimitBlocked(t *testing.T) {
	db := &fakePairingCodeResetDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(0),
		rateLimitSceneWebPairBind:      testPolicy(10),
	}, time.Date(2026, 2, 13, 14, 11, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(validPairingCodeResetPayload))
	req.Header.Set(requestIDHeader, "req-pairing-reset-rate-limit")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-pairing-reset-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestPairingCodeResetRouteConflictMappings(t *testing.T) {
	tests := []struct {
		name       string
		db         *fakePairingCodeResetDB
		wantCode   string
		wantStatus int
	}{
		{
			name: "stale writer rejected",
			db: &fakePairingCodeResetDB{
				snapshot: pairingCodeQuerySnapshot{
					PairingCode:      "39481726",
					WriterDeviceID:   "ffffffff-ffff-ffff-ffff-ffffffffffff",
					WriterEpoch:      99,
					DeviceAuthorized: true,
				},
			},
			wantCode:   staleWriterRejectedCode,
			wantStatus: http.StatusConflict,
		},
		{
			name: "user not found",
			db: &fakePairingCodeResetDB{
				loadErr: pgx.ErrNoRows,
			},
			wantCode:   userNotFoundCode,
			wantStatus: http.StatusConflict,
		},
		{
			name: "pairing code generate failed",
			db: &fakePairingCodeResetDB{
				snapshot: pairingCodeQuerySnapshot{
					PairingCode:      "39481726",
					WriterDeviceID:   "0b854f80-0213-4cb1-b5d0-95af02f137f3",
					WriterEpoch:      12,
					DeviceAuthorized: true,
				},
				rotateErrs: repeatedUniquePairingErrors(pairingCodeGenerateMaxRetrys),
			},
			wantCode:   pairingCodeGenerateFailedCode,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, pairingCodeResetPath, strings.NewReader(validPairingCodeResetPayload))
			req.Header.Set(requestIDHeader, "req-pairing-reset-conflict")
			setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, tc.wantStatus, tc.wantCode, "req-pairing-reset-conflict")
		})
	}
}

func repeatedUniquePairingErrors(count int) []error {
	result := make([]error, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, &pgconn.PgError{
			Code:           "23505",
			ConstraintName: "uq_users_pairing_code",
			Message:        "duplicate key value violates unique constraint",
		})
	}
	return result
}

type fakePairingCodeResetDB struct {
	snapshot       pairingCodeQuerySnapshot
	loadErr        error
	activeBindings int64
	countErr       error
	rotateErr      error
	rotateErrs     []error
	currentCode    string
	currentVersion int64
	currentAt      time.Time
	currentErr     error
	withTxCalls    int
	rotateCalls    int
}

func (f *fakePairingCodeResetDB) Health(context.Context) error {
	return nil
}

func (f *fakePairingCodeResetDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	return testMobileAuthContextForToken(header.Token), nil
}

func (f *fakePairingCodeResetDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakePairingCodeResetTx{db: f})
}

type fakePairingCodeResetTx struct {
	db *fakePairingCodeResetDB
}

type fakePairingResetRow struct {
	scanFn func(dest ...any) error
}

func (r fakePairingResetRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakePairingCodeResetTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakePairingCodeResetTx) Commit(context.Context) error   { return nil }
func (f *fakePairingCodeResetTx) Rollback(context.Context) error { return nil }
func (f *fakePairingCodeResetTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakePairingCodeResetTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakePairingCodeResetTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakePairingCodeResetTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakePairingCodeResetTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakePairingCodeResetTx) Conn() *pgx.Conn { return nil }

func (f *fakePairingCodeResetTx) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(query, "SELECT rotate_pairing_code") {
		f.db.rotateCalls++
		if len(f.db.rotateErrs) > 0 {
			err := f.db.rotateErrs[0]
			f.db.rotateErrs = f.db.rotateErrs[1:]
			return pgconn.CommandTag{}, err
		}
		if f.db.rotateErr != nil {
			return pgconn.CommandTag{}, f.db.rotateErr
		}
		return pgconn.CommandTag{}, nil
	}
	return pgconn.CommandTag{}, errors.New("unexpected exec query: " + query)
}

func (f *fakePairingCodeResetTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM users u"):
		return fakePairingResetRow{scanFn: func(dest ...any) error {
			if f.db.loadErr != nil {
				return f.db.loadErr
			}
			if len(dest) != 6 {
				return errors.New("invalid destination fields for snapshot")
			}

			codePtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			versionPtr, ok := dest[1].(*int64)
			if !ok {
				return errors.New("dest[1] must be *int64")
			}
			atPtr, ok := dest[2].(*time.Time)
			if !ok {
				return errors.New("dest[2] must be *time.Time")
			}
			writerDevicePtr, ok := dest[3].(*string)
			if !ok {
				return errors.New("dest[3] must be *string")
			}
			writerEpochPtr, ok := dest[4].(*int64)
			if !ok {
				return errors.New("dest[4] must be *int64")
			}
			authorizedPtr, ok := dest[5].(*bool)
			if !ok {
				return errors.New("dest[5] must be *bool")
			}

			*codePtr = f.db.snapshot.PairingCode
			*versionPtr = f.db.snapshot.PairingCodeVersion
			*atPtr = f.db.snapshot.PairingCodeAt
			*writerDevicePtr = f.db.snapshot.WriterDeviceID
			*writerEpochPtr = f.db.snapshot.WriterEpoch
			*authorizedPtr = f.db.snapshot.DeviceAuthorized
			return nil
		}}
	case strings.Contains(query, "FROM web_read_bindings"):
		return fakePairingResetRow{scanFn: func(dest ...any) error {
			if f.db.countErr != nil {
				return f.db.countErr
			}
			if len(dest) != 1 {
				return errors.New("invalid destination fields for active bindings")
			}
			countPtr, ok := dest[0].(*int64)
			if !ok {
				return errors.New("dest[0] must be *int64")
			}
			*countPtr = f.db.activeBindings
			return nil
		}}
	case strings.Contains(query, "SELECT pairing_code, pairing_code_version, pairing_code_updated_at"):
		return fakePairingResetRow{scanFn: func(dest ...any) error {
			if f.db.currentErr != nil {
				return f.db.currentErr
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for current pairing")
			}
			codePtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			versionPtr, ok := dest[1].(*int64)
			if !ok {
				return errors.New("dest[1] must be *int64")
			}
			atPtr, ok := dest[2].(*time.Time)
			if !ok {
				return errors.New("dest[2] must be *time.Time")
			}

			*codePtr = f.db.currentCode
			*versionPtr = f.db.currentVersion
			*atPtr = f.db.currentAt
			return nil
		}}
	default:
		return fakePairingResetRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
