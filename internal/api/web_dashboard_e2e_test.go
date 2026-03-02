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

func TestWebDashboardReadOnlyE2E(t *testing.T) {
	const (
		pairingCode       = "12345678"
		clientFingerprint = "9cfce7bcd5d6dfac2697fdf1f5b9f226"
	)

	userID := "00000000-0000-0000-0000-000000000001"
	bindingID := "60000000-0000-0000-0000-000000000001"
	db := &fakeWebDashboardE2EDB{
		userSnapshot: webReadBindingUserSnapshot{
			UserID:             userID,
			PairingCodeVersion: 4,
		},
		bindingID:       bindingID,
		insertCreatedAt: time.Date(2026, 2, 13, 17, 0, 0, 0, time.UTC),
		lastSeenAt:      time.Date(2026, 2, 13, 18, 0, 0, 0, time.UTC),
		monthRows: [][]any{
			{
				"40000000-0000-0000-0000-000000000001",
				time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
				540,
				0,
				int64(1),
				time.Date(2026, 2, 12, 10, 21, 0, 0, time.UTC),
			},
		},
		dayRows: [][]any{
			{
				"30000000-0000-0000-0000-000000000001",
				time.Date(2026, 2, 12, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 2, 12, 1, 10, 0, 0, time.UTC),
				time.Date(2026, 2, 12, 10, 10, 0, 0, time.UTC),
				false,
				nil,
				false,
				540,
				0,
				"COMPUTED",
				int64(1),
				time.Date(2026, 2, 12, 10, 21, 0, 0, time.UTC),
			},
		},
	}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneWebPairBind: testPolicy(100),
	}, time.Date(2026, 2, 13, 18, 0, 0, 0, time.UTC))

	original := generateWebBindingToken
	generateWebBindingToken = func() (string, error) { return "wrb_e2e_binding_token", nil }
	defer func() { generateWebBindingToken = original }()

	// 1) bind
	bindPayload, err := json.Marshal(webReadBindingsRequest{
		PairingCode:       pairingCode,
		ClientFingerprint: clientFingerprint,
		WebDeviceName:     "Chrome@E2E",
	})
	if err != nil {
		t.Fatalf("encode bind payload: %v", err)
	}
	bindRec := httptest.NewRecorder()
	bindReq := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(string(bindPayload)))
	bindReq.Header.Set(requestIDHeader, "req-web-e2e-bind")
	server.httpServer.Handler.ServeHTTP(bindRec, bindReq)
	if bindRec.Code != http.StatusOK {
		t.Fatalf("bind status = %d body=%s", bindRec.Code, bindRec.Body.String())
	}
	var bindBody webReadBindingsResponse
	if err := json.Unmarshal(bindRec.Body.Bytes(), &bindBody); err != nil {
		t.Fatalf("decode bind response: %v", err)
	}
	if bindBody.BindingToken != "wrb_e2e_binding_token" {
		t.Fatalf("binding_token = %q", bindBody.BindingToken)
	}
	if db.tokenHash != hashWebBindingCredential(bindBody.BindingToken, clientFingerprint) {
		t.Fatalf("token hash mismatch")
	}

	// 2) auth
	authPayload, err := json.Marshal(webReadBindingsAuthRequest{
		BindingToken:      bindBody.BindingToken,
		ClientFingerprint: clientFingerprint,
	})
	if err != nil {
		t.Fatalf("encode auth payload: %v", err)
	}
	authRec := httptest.NewRecorder()
	authReq := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(string(authPayload)))
	authReq.Header.Set(requestIDHeader, "req-web-e2e-auth")
	server.httpServer.Handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("auth status = %d body=%s", authRec.Code, authRec.Body.String())
	}
	var authBody webReadBindingsAuthResponse
	if err := json.Unmarshal(authRec.Body.Bytes(), &authBody); err != nil {
		t.Fatalf("decode auth response: %v", err)
	}
	if authBody.BindingID != bindingID {
		t.Fatalf("auth binding_id = %q", authBody.BindingID)
	}
	if authBody.LastSeenAt != "2026-02-13T18:00:00Z" {
		t.Fatalf("auth last_seen_at = %q", authBody.LastSeenAt)
	}

	// 3) month summaries
	year := 2026
	monthPayload, err := json.Marshal(webMonthSummariesQueryRequest{
		BindingToken:      bindBody.BindingToken,
		ClientFingerprint: clientFingerprint,
		Year:              &year,
	})
	if err != nil {
		t.Fatalf("encode month payload: %v", err)
	}
	monthRec := httptest.NewRecorder()
	monthReq := httptest.NewRequest(http.MethodPost, webMonthSummariesQueryPath, strings.NewReader(string(monthPayload)))
	monthReq.Header.Set(requestIDHeader, "req-web-e2e-month")
	server.httpServer.Handler.ServeHTTP(monthRec, monthReq)
	if monthRec.Code != http.StatusOK {
		t.Fatalf("month status = %d body=%s", monthRec.Code, monthRec.Body.String())
	}
	var monthBody webMonthSummariesQueryResponse
	if err := json.Unmarshal(monthRec.Body.Bytes(), &monthBody); err != nil {
		t.Fatalf("decode month response: %v", err)
	}
	if len(monthBody.MonthSummaries) != 1 {
		t.Fatalf("month_summaries size = %d", len(monthBody.MonthSummaries))
	}
	if monthBody.MonthSummaries[0].MonthStart != "2026-02-01" {
		t.Fatalf("month_start = %q", monthBody.MonthSummaries[0].MonthStart)
	}

	// 4) day summaries
	dayPayload, err := json.Marshal(webDaySummariesQueryRequest{
		BindingToken:      bindBody.BindingToken,
		ClientFingerprint: clientFingerprint,
		MonthStart:        "2026-02-01",
	})
	if err != nil {
		t.Fatalf("encode day payload: %v", err)
	}
	dayRec := httptest.NewRecorder()
	dayReq := httptest.NewRequest(http.MethodPost, webDaySummariesQueryPath, strings.NewReader(string(dayPayload)))
	dayReq.Header.Set(requestIDHeader, "req-web-e2e-day")
	server.httpServer.Handler.ServeHTTP(dayRec, dayReq)
	if dayRec.Code != http.StatusOK {
		t.Fatalf("day status = %d body=%s", dayRec.Code, dayRec.Body.String())
	}
	var dayBody webDaySummariesQueryResponse
	if err := json.Unmarshal(dayRec.Body.Bytes(), &dayBody); err != nil {
		t.Fatalf("decode day response: %v", err)
	}
	if len(dayBody.DaySummaries) != 1 {
		t.Fatalf("day_summaries size = %d", len(dayBody.DaySummaries))
	}
	if dayBody.DaySummaries[0].LocalDate != "2026-02-12" {
		t.Fatalf("local_date = %q", dayBody.DaySummaries[0].LocalDate)
	}

	if db.touchCalls != 3 {
		t.Fatalf("touchCalls = %d", db.touchCalls)
	}
	if db.lastTouchBindingID != bindingID {
		t.Fatalf("lastTouchBindingID = %q", db.lastTouchBindingID)
	}
}

type fakeWebDashboardE2EDB struct {
	userSnapshot    webReadBindingUserSnapshot
	bindingID       string
	tokenHash       string
	insertCreatedAt time.Time
	lastSeenAt      time.Time
	monthRows       [][]any
	dayRows         [][]any

	withTxCalls        int
	insertCalls        int
	touchCalls         int
	lastTouchBindingID string
}

func (f *fakeWebDashboardE2EDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebDashboardE2EDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeWebDashboardE2ETx{db: f})
}

type fakeWebDashboardE2ETx struct {
	db *fakeWebDashboardE2EDB
}

type fakeWebDashboardE2ERow struct {
	scanFn func(dest ...any) error
}

func (r fakeWebDashboardE2ERow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeWebDashboardE2ETx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeWebDashboardE2ETx) Commit(context.Context) error   { return nil }
func (f *fakeWebDashboardE2ETx) Rollback(context.Context) error { return nil }
func (f *fakeWebDashboardE2ETx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebDashboardE2ETx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeWebDashboardE2ETx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeWebDashboardE2ETx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebDashboardE2ETx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeWebDashboardE2ETx) Conn() *pgx.Conn { return nil }

func (f *fakeWebDashboardE2ETx) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	switch {
	case strings.Contains(query, "FROM month_summaries"):
		return newFakeRows(f.db.monthRows), nil
	case strings.Contains(query, "FROM day_summaries"):
		return newFakeRows(f.db.dayRows), nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

func (f *fakeWebDashboardE2ETx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM users") && strings.Contains(query, "WHERE pairing_code = $1"):
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			if len(dest) != 2 {
				return errors.New("invalid destination fields for user snapshot")
			}
			*dest[0].(*string) = f.db.userSnapshot.UserID
			*dest[1].(*int64) = f.db.userSnapshot.PairingCodeVersion
			return nil
		}}
	case strings.Contains(query, "FROM web_read_bindings") && strings.Contains(query, "ORDER BY created_at DESC"):
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			return pgx.ErrNoRows
		}}
	case strings.Contains(query, "INSERT INTO web_read_bindings"):
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			f.db.insertCalls++
			if len(args) >= 3 {
				if tokenHash, ok := args[2].(string); ok {
					f.db.tokenHash = tokenHash
				}
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for inserted binding")
			}
			*dest[0].(*string) = f.db.bindingID
			*dest[1].(*string) = webBindingStatusActive
			*dest[2].(*time.Time) = f.db.insertCreatedAt
			return nil
		}}
	case strings.Contains(query, "FROM web_read_bindings b") && strings.Contains(query, "WHERE b.token_hash = $1"):
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			if len(args) >= 1 {
				if tokenHash, ok := args[0].(string); ok {
					if strings.TrimSpace(f.db.tokenHash) != "" && tokenHash != f.db.tokenHash {
						return pgx.ErrNoRows
					}
				}
			}
			if len(dest) != 5 {
				return errors.New("invalid destination fields for auth snapshot")
			}
			*dest[0].(*string) = f.db.bindingID
			*dest[1].(*string) = f.db.userSnapshot.UserID
			*dest[2].(*string) = webBindingStatusActive
			*dest[3].(*int64) = f.db.userSnapshot.PairingCodeVersion
			*dest[4].(*int64) = f.db.userSnapshot.PairingCodeVersion
			return nil
		}}
	case strings.Contains(query, "UPDATE web_read_bindings") && strings.Contains(query, "SET last_seen_at = now()"):
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			f.db.touchCalls++
			if len(args) >= 1 {
				if bindingID, ok := args[0].(string); ok {
					f.db.lastTouchBindingID = bindingID
				}
			}
			if len(dest) != 1 {
				return errors.New("invalid destination fields for last_seen_at")
			}
			*dest[0].(*time.Time) = f.db.lastSeenAt
			return nil
		}}
	default:
		return fakeWebDashboardE2ERow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
