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

const validWebMonthSummariesQueryPayload = `{
	"year":2026
}`

func TestWebMonthSummariesQueryRouteSuccess(t *testing.T) {
	db := &fakeWebMonthSummariesQueryDB{
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
	setSyncAuthHeader(req, testSyncToken)
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
	if db.lastResolvedToken != testSyncToken {
		t.Fatalf("lastResolvedToken = %q", db.lastResolvedToken)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.lastMonthQueryArgsLen != 3 {
		t.Fatalf("month query args len = %d", db.lastMonthQueryArgsLen)
	}
	if db.lastMonthQueryUserID != testUserID {
		t.Fatalf("month query user_id = %q", db.lastMonthQueryUserID)
	}
	if db.lastMonthQueryStart != "2026-01-01" || db.lastMonthQueryEnd != "2027-01-01" {
		t.Fatalf("month query range = [%s, %s)", db.lastMonthQueryStart, db.lastMonthQueryEnd)
	}
}

func TestWebMonthSummariesQueryRouteEmpty(t *testing.T) {
	db := &fakeWebMonthSummariesQueryDB{}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-month-empty")
	setSyncAuthHeader(req, testSyncToken)
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
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(`{}`))
	req.Header.Set(requestIDHeader, "req-web-month-invalid")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-web-month-invalid")
}

func TestWebMonthSummariesQueryRouteUnauthorizedMobileToken(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-month-unauthorized")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedMobileTokenCode, "req-web-month-unauthorized")
}

func TestWebMonthSummariesQueryRouteUserIDNotReady(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(validWebMonthSummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-month-user-not-ready")
	setSyncAuthHeader(req, testAnonymousSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusConflict, userIDNotReadyCode, "req-web-month-user-not-ready")
}

func TestWebMonthSummariesQueryRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(`{
		"year":2026,
		"binding_token":"legacy-token"
	}`))
	req.Header.Set(requestIDHeader, "req-web-month-unknown")
	setSyncAuthHeader(req, testSyncToken)
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
	authErr     error
	authContext mobileAuthContext
	monthRows   [][]any
	queryErr    error

	withTxCalls           int
	lastResolvedToken     string
	lastMonthQueryArgsLen int
	lastMonthQueryUserID  string
	lastMonthQueryStart   string
	lastMonthQueryEnd     string
}

func (f *fakeWebMonthSummariesQueryDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebMonthSummariesQueryDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	f.lastResolvedToken = header.Token
	if f.authErr != nil {
		return mobileAuthContext{}, f.authErr
	}
	if f.authContext.Token != "" || f.authContext.UserID != "" || f.authContext.TokenStatus != "" {
		return f.authContext, nil
	}
	return testMobileAuthContextForToken(header.Token), nil
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
	if !strings.Contains(query, "FROM month_summaries") {
		return nil, errors.New("unexpected query: " + query)
	}
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

func (f *fakeWebMonthSummariesQueryTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakePGXRow{scanFn: func(dest ...any) error {
		return errors.New("unexpected query row call")
	}}
}

func (f *fakeWebMonthSummariesQueryTx) Conn() *pgx.Conn { return nil }
