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

const validWebDaySummariesQueryPayload = `{
	"binding_token":"wrb_valid_binding_token",
	"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
	"month_start":"2026-02-01"
}`

func TestWebDaySummariesQueryRouteSuccess(t *testing.T) {
	userID := "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362"
	db := &fakeWebDaySummariesQueryDB{
		credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
		snapshot: webReadBindingsAuthSnapshot{
			BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
			UserID:                    userID,
			Status:                    webBindingStatusActive,
			BindingPairingCodeVersion: 4,
			CurrentPairingCodeVersion: 4,
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
			{
				"b0afc2dc-e292-4217-8b2b-22a568d88e33",
				time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC),
				nil,
				nil,
				true,
				"AM",
				nil,
				nil,
				0,
				"INCOMPLETE",
				int64(1),
				time.Date(2026, 2, 13, 11, 0, 0, 0, time.UTC),
			},
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(validWebDaySummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-day-success")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webDaySummariesQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.DaySummaries) != 2 {
		t.Fatalf("day_summaries size = %d", len(body.DaySummaries))
	}
	if body.DaySummaries[0].LocalDate != "2026-02-12" {
		t.Fatalf("local_date[0] = %q", body.DaySummaries[0].LocalDate)
	}
	if body.DaySummaries[0].StartAtUTC == nil || *body.DaySummaries[0].StartAtUTC != "2026-02-12T01:10:00Z" {
		t.Fatalf("start_at_utc[0] = %v", body.DaySummaries[0].StartAtUTC)
	}
	if body.DaySummaries[0].EndAtUTC == nil || *body.DaySummaries[0].EndAtUTC != "2026-02-12T10:20:00Z" {
		t.Fatalf("end_at_utc[0] = %v", body.DaySummaries[0].EndAtUTC)
	}
	if body.DaySummaries[1].StartAtUTC != nil || body.DaySummaries[1].EndAtUTC != nil {
		t.Fatalf("expected null start/end for second row, got %v %v", body.DaySummaries[1].StartAtUTC, body.DaySummaries[1].EndAtUTC)
	}
	if body.DaySummaries[1].LeaveType == nil || *body.DaySummaries[1].LeaveType != "AM" {
		t.Fatalf("leave_type[1] = %v", body.DaySummaries[1].LeaveType)
	}

	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.lastDayQueryArgsLen != 3 {
		t.Fatalf("day query args len = %d", db.lastDayQueryArgsLen)
	}
	if db.lastDayQueryUserID != userID {
		t.Fatalf("day query user_id = %q", db.lastDayQueryUserID)
	}
	if db.lastDayQueryStart != "2026-02-01" || db.lastDayQueryEnd != "2026-03-01" {
		t.Fatalf("day query range = [%s, %s)", db.lastDayQueryStart, db.lastDayQueryEnd)
	}
}

func TestWebDaySummariesQueryRouteEmpty(t *testing.T) {
	db := &fakeWebDaySummariesQueryDB{
		credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
		snapshot: webReadBindingsAuthSnapshot{
			BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
			UserID:                    "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			Status:                    webBindingStatusActive,
			BindingPairingCodeVersion: 4,
			CurrentPairingCodeVersion: 4,
		},
		dayRows: [][]any{},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(validWebDaySummariesQueryPayload))
	req.Header.Set(requestIDHeader, "req-web-day-empty")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webDaySummariesQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.DaySummaries == nil {
		t.Fatal("day_summaries is nil")
	}
	if len(body.DaySummaries) != 0 {
		t.Fatalf("day_summaries size = %d", len(body.DaySummaries))
	}
}

func TestWebDaySummariesQueryRouteInvalid(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(`{
		"binding_token":"wrb_valid_binding_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"month_start":"2026-02-02"
	}`))
	req.Header.Set(requestIDHeader, "req-web-day-invalid")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-web-day-invalid")
}

func TestWebDaySummariesQueryRouteUnauthorizedWebToken(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		db      *fakeWebDaySummariesQueryDB
	}{
		{
			name: "token format invalid",
			payload: `{
				"binding_token":"invalid-token",
				"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
				"month_start":"2026-02-01"
			}`,
			db: &fakeWebDaySummariesQueryDB{},
		},
		{
			name:    "token not found",
			payload: validWebDaySummariesQueryPayload,
			db: &fakeWebDaySummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_other_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
		{
			name: "token fingerprint mismatch",
			payload: `{
				"binding_token":"wrb_valid_binding_token",
				"client_fingerprint":"ffffffffffffffffffffffffffffffff",
				"month_start":"2026-02-01"
			}`,
			db: &fakeWebDaySummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(tc.payload))
			req.Header.Set(requestIDHeader, "req-web-day-unauthorized")
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedWebTokenCode, "req-web-day-unauthorized")
		})
	}
}

func TestWebDaySummariesQueryRouteConflicts(t *testing.T) {
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
			db := &fakeWebDaySummariesQueryDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
				snapshot:       tc.snapshot,
			}
			server := NewServer("127.0.0.1:0", db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(validWebDaySummariesQueryPayload))
			req.Header.Set(requestIDHeader, "req-web-day-conflict")
			server.httpServer.Handler.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusConflict, tc.wantCode, "req-web-day-conflict")
		})
	}
}

func TestWebDaySummariesQueryRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(`{
		"binding_token":"wrb_valid_binding_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"month_start":"2026-02-01",
		"unknown":"x"
	}`))
	req.Header.Set(requestIDHeader, "req-web-day-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-web-day-unknown")
}

func TestWebDaySummariesQueryRouteMethodNotAllowed(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, webDaySummariesQueryPath, nil)
	req.Header.Set(requestIDHeader, "req-web-day-method-not-allowed")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusMethodNotAllowed, methodNotAllowedCode, "req-web-day-method-not-allowed")
}

type fakeWebDaySummariesQueryDB struct {
	credentialHash string
	snapshot       webReadBindingsAuthSnapshot
	dayRows        [][]any
	loadErr        error
	queryErr       error

	withTxCalls         int
	lastLookupHash      string
	lastDayQueryArgsLen int
	lastDayQueryUserID  string
	lastDayQueryStart   string
	lastDayQueryEnd     string
}

func (f *fakeWebDaySummariesQueryDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebDaySummariesQueryDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeWebDaySummariesQueryTx{db: f})
}

type fakeWebDaySummariesQueryTx struct {
	db *fakeWebDaySummariesQueryDB
}

func (f *fakeWebDaySummariesQueryTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeWebDaySummariesQueryTx) Commit(context.Context) error   { return nil }
func (f *fakeWebDaySummariesQueryTx) Rollback(context.Context) error { return nil }
func (f *fakeWebDaySummariesQueryTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebDaySummariesQueryTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeWebDaySummariesQueryTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeWebDaySummariesQueryTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebDaySummariesQueryTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeWebDaySummariesQueryTx) Query(_ context.Context, query string, args ...any) (pgx.Rows, error) {
	if strings.Contains(query, "FROM day_summaries") {
		if !strings.Contains(query, "ORDER BY local_date ASC") {
			return nil, errors.New("day summary query missing ORDER BY local_date ASC")
		}
		if f.db.queryErr != nil {
			return nil, f.db.queryErr
		}
		f.db.lastDayQueryArgsLen = len(args)
		if len(args) >= 3 {
			if v, ok := args[0].(string); ok {
				f.db.lastDayQueryUserID = v
			}
			if v, ok := args[1].(string); ok {
				f.db.lastDayQueryStart = v
			}
			if v, ok := args[2].(string); ok {
				f.db.lastDayQueryEnd = v
			}
		}
		return newFakeRows(f.db.dayRows), nil
	}
	return nil, errors.New("unexpected query: " + query)
}

func (f *fakeWebDaySummariesQueryTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
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

func (f *fakeWebDaySummariesQueryTx) Conn() *pgx.Conn { return nil }
