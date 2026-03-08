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

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const validMigrationRequestPayload = `{
	"user_id": "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
	"from_device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3",
	"to_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
	"mode": "NORMAL",
	"expires_at": "2026-02-12T11:00:00Z"
}`

const validMigrationRequestTokenOnlyPayload = `{
	"to_device_id": "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
	"mode": "NORMAL",
	"expires_at": "2026-02-12T11:00:00Z"
}`

func TestMigrationRequestsRouteSuccess(t *testing.T) {
	db := &fakeMigrationRequestDB{
		row: fakeMigrationRequestRow{
			migrationRequestID: "f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60",
			status:             "PENDING",
			mode:               "NORMAL",
			expiresAt:          time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC),
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(validMigrationRequestPayload))
	req.Header.Set(requestIDHeader, "req-migration-request-success")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body migrationRequestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MigrationRequestID != db.row.migrationRequestID {
		t.Fatalf("migration_request_id = %q", body.MigrationRequestID)
	}
	if body.Status != "PENDING" {
		t.Fatalf("status = %q", body.Status)
	}
	if body.Mode != "NORMAL" {
		t.Fatalf("mode = %q", body.Mode)
	}
	if body.ExpiresAt != "2026-02-12T11:00:00Z" {
		t.Fatalf("expires_at = %q", body.ExpiresAt)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestMigrationRequestsRouteTokenOnlyBodySuccess(t *testing.T) {
	db := &fakeMigrationRequestDB{
		row: fakeMigrationRequestRow{
			migrationRequestID: "0a137472-577d-4af7-b8f1-d96a0d67aa8d",
			status:             "PENDING",
			mode:               "NORMAL",
			expiresAt:          time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC),
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(validMigrationRequestTokenOnlyPayload))
	req.Header.Set(requestIDHeader, "req-migration-request-token-only")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMigrationRequestsRouteUserIDNotReady(t *testing.T) {
	server := NewServer("127.0.0.1:0", &fakeMigrationRequestDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(validMigrationRequestTokenOnlyPayload))
	req.Header.Set(requestIDHeader, "req-migration-request-user-not-ready")
	setSyncAuthHeader(req, testAnonymousSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusConflict, userIDNotReadyCode, "req-migration-request-user-not-ready")
}

func TestMigrationRequestsRouteInvalidArgument(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(strings.Replace(validMigrationRequestPayload, `"mode": "NORMAL"`, `"mode": "FORCED"`, 1)))
	req.Header.Set(requestIDHeader, "req-migration-request-invalid")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-migration-request-invalid")
}

func TestMigrationRequestsRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(strings.Replace(validMigrationRequestPayload, `"mode": "NORMAL",`, `"mode": "NORMAL", "unknown_field": "x",`, 1)))
	req.Header.Set(requestIDHeader, "req-migration-request-unknown")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-migration-request-unknown")
}

func TestMigrationRequestsRouteRateLimitBlocked(t *testing.T) {
	db := &fakeMigrationRequestDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(0),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
	}, time.Date(2026, 2, 13, 14, 11, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(validMigrationRequestPayload))
	req.Header.Set(requestIDHeader, "req-migration-request-rate-limit")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-migration-request-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestMigrationRequestsRouteConflictMappings(t *testing.T) {
	tests := []struct {
		name     string
		failErr  error
		wantCode string
	}{
		{
			name: "migration source mismatch",
			failErr: &pgconn.PgError{
				Code:    "P0001",
				Message: "[error_key=MIGRATION_SOURCE_MISMATCH] mismatch",
			},
			wantCode: migrationSourceMismatchCode,
		},
		{
			name: "migration transition invalid maps to state invalid",
			failErr: &pgconn.PgError{
				Code:    "P0001",
				Message: "[error_key=MIGRATION_TRANSITION_INVALID] invalid transition",
			},
			wantCode: migrationStateInvalidCode,
		},
		{
			name: "migration immutable fields",
			failErr: &pgconn.PgError{
				Code:    "P0001",
				Message: "[error_key=MIGRATION_IMMUTABLE_FIELDS] immutable",
			},
			wantCode: migrationImmutableFieldsCode,
		},
		{
			name: "migration pending exists",
			failErr: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uk_migration_user_pending",
				Message:        "duplicate key value violates unique constraint",
			},
			wantCode: migrationPendingExistsCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeMigrationRequestDB{failErr: tc.failErr}
			server := NewServer("127.0.0.1:0", db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, migrationsRequestsPath, strings.NewReader(validMigrationRequestPayload))
			req.Header.Set(requestIDHeader, "req-migration-request-conflict")
			setSyncAuthHeader(req, testSyncToken)
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusConflict, tc.wantCode, "req-migration-request-conflict")
		})
	}
}

func assertErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder, expectedStatus int, expectedCode, expectedRequestID string) {
	t.Helper()

	if rec.Code != expectedStatus {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != expectedCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if strings.TrimSpace(payload.Message) == "" {
		t.Fatal("message is empty")
	}
	if payload.RequestID != expectedRequestID {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
}

type fakeMigrationRequestDB struct {
	row         fakeMigrationRequestRow
	failErr     error
	withTxCalls int
}

func (f *fakeMigrationRequestDB) Health(context.Context) error {
	return nil
}

func (f *fakeMigrationRequestDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	return testMobileAuthContextForToken(header.Token), nil
}

func (f *fakeMigrationRequestDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	tx := &fakeMigrationRequestTx{
		row:     f.row,
		failErr: f.failErr,
	}
	return fn(tx)
}

type fakeMigrationRequestTx struct {
	row     fakeMigrationRequestRow
	failErr error
}

type fakeMigrationRequestRow struct {
	migrationRequestID string
	status             string
	mode               string
	expiresAt          time.Time
	err                error
}

type fakePGXRow struct {
	scanFn func(dest ...any) error
}

func (r fakePGXRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeMigrationRequestTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeMigrationRequestTx) Commit(context.Context) error   { return nil }
func (f *fakeMigrationRequestTx) Rollback(context.Context) error { return nil }
func (f *fakeMigrationRequestTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeMigrationRequestTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeMigrationRequestTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeMigrationRequestTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeMigrationRequestTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeMigrationRequestTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeMigrationRequestTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakePGXRow{scanFn: func(dest ...any) error {
		if f.failErr != nil {
			return f.failErr
		}
		if len(dest) != 4 {
			return errors.New("invalid destination fields")
		}
		id, ok := dest[0].(*string)
		if !ok {
			return errors.New("dest[0] must be *string")
		}
		status, ok := dest[1].(*string)
		if !ok {
			return errors.New("dest[1] must be *string")
		}
		mode, ok := dest[2].(*string)
		if !ok {
			return errors.New("dest[2] must be *string")
		}
		expiresAt, ok := dest[3].(*time.Time)
		if !ok {
			return errors.New("dest[3] must be *time.Time")
		}

		*id = f.row.migrationRequestID
		*status = f.row.status
		*mode = f.row.mode
		*expiresAt = f.row.expiresAt
		return nil
	}}
}
func (f *fakeMigrationRequestTx) Conn() *pgx.Conn { return nil }
