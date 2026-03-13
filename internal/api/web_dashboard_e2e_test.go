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

func TestWebDashboardReadOnlyFlowUsesMobileToken(t *testing.T) {
	db := &fakeWebDashboardReadOnlyDB{
		monthRows: [][]any{
			{
				"445f1f36-cf1c-4f90-9fd0-b56438e2df2e",
				time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
				6120,
				120,
				int64(5),
				time.Date(2026, 2, 12, 10, 21, 0, 0, time.UTC),
			},
		},
		dayRows: [][]any{
			{
				"3cf42a4f-8107-49dd-96bd-1cd7ea6f3f54",
				time.Date(2026, 2, 12, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 2, 12, 1, 10, 0, 0, time.UTC),
				time.Date(2026, 2, 12, 10, 20, 0, 0, time.UTC),
				false,
				nil,
				true,
				550,
				0,
				"COMPUTED",
				int64(5),
				time.Date(2026, 2, 12, 10, 21, 0, 0, time.UTC),
			},
		},
	}
	server := NewServer("127.0.0.1:0", db)

	monthReq := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(`{"year":2026}`))
	monthReq.Header.Set(requestIDHeader, "req-web-dashboard-month")
	setSyncAuthHeader(monthReq, testSyncToken)
	monthRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(monthRec, monthReq)
	if monthRec.Code != http.StatusOK {
		t.Fatalf("month status = %d body=%s", monthRec.Code, monthRec.Body.String())
	}

	dayReq := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(`{"month_start":"2026-02-01"}`))
	dayReq.Header.Set(requestIDHeader, "req-web-dashboard-day")
	setSyncAuthHeader(dayReq, testSyncToken)
	dayRec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(dayRec, dayReq)
	if dayRec.Code != http.StatusOK {
		t.Fatalf("day status = %d body=%s", dayRec.Code, dayRec.Body.String())
	}

	var monthBody webMonthSummariesQueryResponse
	if err := json.Unmarshal(monthRec.Body.Bytes(), &monthBody); err != nil {
		t.Fatalf("decode month response: %v", err)
	}
	if len(monthBody.MonthSummaries) != 1 {
		t.Fatalf("month_summaries size = %d", len(monthBody.MonthSummaries))
	}

	var dayBody webDaySummariesQueryResponse
	if err := json.Unmarshal(dayRec.Body.Bytes(), &dayBody); err != nil {
		t.Fatalf("decode day response: %v", err)
	}
	if len(dayBody.DaySummaries) != 1 {
		t.Fatalf("day_summaries size = %d", len(dayBody.DaySummaries))
	}
	if db.lastResolvedToken != testSyncToken {
		t.Fatalf("lastResolvedToken = %q", db.lastResolvedToken)
	}
}

type fakeWebDashboardReadOnlyDB struct {
	lastResolvedToken string
	monthRows         [][]any
	dayRows           [][]any
}

func (f *fakeWebDashboardReadOnlyDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebDashboardReadOnlyDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	f.lastResolvedToken = header.Token
	return testMobileAuthContextForToken(header.Token), nil
}

func (f *fakeWebDashboardReadOnlyDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return fn(&fakeWebDashboardReadOnlyTx{db: f})
}

type fakeWebDashboardReadOnlyTx struct {
	db *fakeWebDashboardReadOnlyDB
}

func (f *fakeWebDashboardReadOnlyTx) Begin(context.Context) (pgx.Tx, error) { return nil, nil }
func (f *fakeWebDashboardReadOnlyTx) Commit(context.Context) error          { return nil }
func (f *fakeWebDashboardReadOnlyTx) Rollback(context.Context) error        { return nil }
func (f *fakeWebDashboardReadOnlyTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebDashboardReadOnlyTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeWebDashboardReadOnlyTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeWebDashboardReadOnlyTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebDashboardReadOnlyTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeWebDashboardReadOnlyTx) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	switch {
	case strings.Contains(query, "FROM month_summaries"):
		return newFakeRows(f.db.monthRows), nil
	case strings.Contains(query, "FROM day_summaries"):
		return newFakeRows(f.db.dayRows), nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}
func (f *fakeWebDashboardReadOnlyTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakePGXRow{scanFn: func(dest ...any) error {
		return errors.New("unexpected query row call")
	}}
}
func (f *fakeWebDashboardReadOnlyTx) Conn() *pgx.Conn { return nil }
