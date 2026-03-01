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

const validWebMonthSummariesQueryPayload = `{
	"binding_token":"wrb_valid_binding_token",
	"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
	"year":2026
}`

func TestWebMonthSummariesQueryRouteSuccess(t *testing.T) {
	userID := "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362"
	db := &fakeWebMonthSummariesQueryDB{
		credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
		snapshot: webReadBindingsAuthSnapshot{
			BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
			UserID:                    userID,
			Status:                    webBindingStatusActive,
			BindingPairingCodeVersion: 4,
			CurrentPairingCodeVersion: 4,
		},
		monthRows: [][]any{
			{
				"445f1f36-cf1c-4f90-9fd0-b56438e2df2e",
				time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
				6120,
				120,
				int64(5),
				time.Date(2026, 2, 12, 10, 21, 0, 0, time.UTC),
			},
			{
				"f1c2074b-3a6e-4b8b-aefb-3fcbb2b5988b",
				time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
				123,
				0,
				int64(1),
				time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC),
			},
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-month-success")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webMonthSummariesQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.MonthSummaries) != 2 {
		t.Fatalf("month_summaries size = %d", len(body.MonthSummaries))
	}
	if body.MonthSummaries[0].MonthStart != "2026-02-01" {
		t.Fatalf("month_start[0] = %q", body.MonthSummaries[0].MonthStart)
	}
	if body.MonthSummaries[0].UpdatedAt != "2026-02-12T10:21:00Z" {
		t.Fatalf("updated_at[0] = %q", body.MonthSummaries[0].UpdatedAt)
	}
	if body.MonthSummaries[1].MonthStart != "2026-03-01" {
		t.Fatalf("month_start[1] = %q", body.MonthSummaries[1].MonthStart)
	}

	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.lastMonthQueryArgsLen != 3 {
		t.Fatalf("month query args len = %d", db.lastMonthQueryArgsLen)
	}
	if db.lastMonthQueryUserID != userID {
		t.Fatalf("month query user_id = %q", db.lastMonthQueryUserID)
	}
	if db.lastMonthQueryStart != "2026-01-01" || db.lastMonthQueryEnd != "2027-01-01" {
		t.Fatalf("month query range = [%s, %s)", db.lastMonthQueryStart, db.lastMonthQueryEnd)
	}
}

func TestWebMonthSummariesQueryRouteEmpty(t *testing.T) {
	db := &fakeWebMonthSummariesQueryDB{
		credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
		snapshot: webReadBindingsAuthSnapshot{
			BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
			UserID:                    "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			Status:                    webBindingStatusActive,
			BindingPairingCodeVersion: 4,
			CurrentPairingCodeVersion: 4,
		},
		monthRows: [][]any{},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-month-empty")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webMonthSummariesQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MonthSummaries == nil {
		t.Fatal("month_summaries is nil")
	}
	if len(body.MonthSummaries) != 0 {
		t.Fatalf("month_summaries size = %d", len(body.MonthSummaries))
	}
}

func TestWebMonthSummariesQueryRouteInvalid(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(`{
		"binding_token":"wrb_valid_binding_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226"
	}`))
	req.Header.Set(requestIDHeader, "req-web-month-invalid")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-web-month-invalid")
}

func TestWebMonthSummariesQueryRouteUnauthorizedWebToken(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		db      *fakeWebMonthSummariesQueryDB
	}{
		{
			name: "token format invalid",
			payload: `{
				"binding_token":"invalid-token",
				"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
				"year":2026
			}`,
			db: &fakeWebMonthSummariesQueryDB{},
		},
		{
			name:    "token not found",
			payload: validWebMonthSummariesQueryPayload,
			db: &fakeWebMonthSummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_other_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
		{
			name: "token fingerprint mismatch",
			payload: `{
				"binding_token":"wrb_valid_binding_token",
				"client_fingerprint":"ffffffffffffffffffffffffffffffff",
				"year":2026
			}`,
			db: &fakeWebMonthSummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(tc.payload))
			req.Header.Set(requestIDHeader, "req-web-month-unauthorized")
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedWebTokenCode, "req-web-month-unauthorized")
		})
	}
}

func TestWebMonthSummariesQueryRouteConflicts(t *testing.T) {
	tests := []struct {
		name     string
		snapshot webReadBindingsAuthSnapshot
		wantCode string
	}{
		{
			name: "reactivate denied",
			snapshot: webReadBindingsAuthSnapshot{
				BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
				UserID:                    "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
				Status:                    "REVOKED",
				BindingPairingCodeVersion: 3,
				CurrentPairingCodeVersion: 4,
			},
			wantCode: webBindingReactivateDeniedCode,
		},
		{
			name: "version mismatch",
			snapshot: webReadBindingsAuthSnapshot{
				BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
				UserID:                    "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
				Status:                    webBindingStatusActive,
				BindingPairingCodeVersion: 3,
				CurrentPairingCodeVersion: 4,
			},
			wantCode: webBindingVersionMismatchCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeWebMonthSummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
				snapshot:       tc.snapshot,
			}
			server := NewServer("127.0.0.1:0", db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
			req.Header.Set(requestIDHeader, "req-web-month-conflict")
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusConflict, tc.wantCode, "req-web-month-conflict")
		})
	}
}

func TestWebMonthSummariesQueryRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(`{
		"binding_token":"wrb_valid_binding_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"year":2026,
		"unknown":"x"
	}`))
	req.Header.Set(requestIDHeader, "req-web-month-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-web-month-unknown")
}

func TestWebMonthSummariesQueryRouteMethodNotAllowed(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, webMonthSummariesQueryPath, nil)
	req.Header.Set(requestIDHeader, "req-web-month-method-not-allowed")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusMethodNotAllowed, methodNotAllowedCode, "req-web-month-method-not-allowed")
}

type fakeWebMonthSummariesQueryDB struct {
	credentialHash string
	snapshot       webReadBindingsAuthSnapshot
	monthRows      [][]any
	loadErr        error
	queryErr       error

	withTxCalls           int
	lastLookupHash        string
	lastMonthQueryArgsLen int
	lastMonthQueryUserID  string
	lastMonthQueryStart   string
	lastMonthQueryEnd     string
}

func (f *fakeWebMonthSummariesQueryDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebMonthSummariesQueryDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeWebMonthSummariesQueryTx{db: f})
}

type fakeWebMonthSummariesQueryTx struct {
	db *fakeWebMonthSummariesQueryDB
}

func (f *fakeWebMonthSummariesQueryTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeWebMonthSummariesQueryTx) Commit(context.Context) error   { return nil }
func (f *fakeWebMonthSummariesQueryTx) Rollback(context.Context) error { return nil }
func (f *fakeWebMonthSummariesQueryTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebMonthSummariesQueryTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeWebMonthSummariesQueryTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeWebMonthSummariesQueryTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebMonthSummariesQueryTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeWebMonthSummariesQueryTx) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	if strings.Contains(query, "FROM month_summaries") {
		if !strings.Contains(query, "ORDER BY month_start ASC") {
			return nil, errors.New("month summary query missing ORDER BY month_start ASC")
		}
		if f.db.queryErr != nil {
			return nil, f.db.queryErr
		}
		f.db.lastMonthQueryArgsLen = len(args)
		if len(args) >= 3 {
			if v, ok := args[0].(string); ok {
				f.db.lastMonthQueryUserID = v
			}
			if v, ok := args[1].(string); ok {
				f.db.lastMonthQueryStart = v
			}
			if v, ok := args[2].(string); ok {
				f.db.lastMonthQueryEnd = v
			}
		}
		return newFakeRows(f.db.monthRows), nil
	}
	return nil, errors.New("unexpected query: " + query)
}

func (f *fakeWebMonthSummariesQueryTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "FROM web_read_bindings b") && strings.Contains(query, "WHERE b.token_hash = $1") {
		return fakeWebReadBindingsAuthRow{scanFn: func(dest ...any) error {
			if f.db.loadErr != nil {
				return f.db.loadErr
			}
			if len(args) >= 1 {
				if tokenHash, ok := args[0].(string); ok {
					f.db.lastLookupHash = tokenHash
					if f.db.credentialHash != "" && tokenHash != f.db.credentialHash {
						return pgx.ErrNoRows
					}
				}
			}
			if len(dest) != 5 {
				return errors.New("invalid destination fields for auth snapshot")
			}
			*dest[0].(*string) = f.db.snapshot.BindingID
			*dest[1].(*string) = f.db.snapshot.UserID
			*dest[2].(*string) = f.db.snapshot.Status
			*dest[3].(*int64) = f.db.snapshot.BindingPairingCodeVersion
			*dest[4].(*int64) = f.db.snapshot.CurrentPairingCodeVersion
			return nil
		}}
	}
	return fakeWebReadBindingsAuthRow{scanFn: func(dest ...any) error {
		return errors.New("unexpected query: " + query)
	}}
}

func (f *fakeWebMonthSummariesQueryTx) Conn() *pgx.Conn { return nil }

func assertAPIError(t *testing.T, err error, wantStatus int, wantCode string) {
	t.Helper()
	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode() != wantStatus {
		t.Fatalf("status = %d", apiErr.StatusCode())
	}
	if apiErr.Code != wantCode {
		t.Fatalf("code = %q", apiErr.Code)
	}
}
