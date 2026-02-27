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

const validWebReadBindingsPayload = `{
	"pairing_code":"24069175",
	"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
	"web_device_name":"Chrome@Mac"
}`

func TestWebReadBindingsRouteSuccessCreate(t *testing.T) {
	db := &fakeWebReadBindingsDB{
		snapshot: webReadBindingUserSnapshot{
			UserID:             "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			PairingCodeVersion: 4,
		},
		insertBindingID: "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
		insertStatus:    webBindingStatusActive,
		insertCreatedAt: time.Date(2026, 2, 13, 17, 0, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateWebBindingToken
	generateWebBindingToken = func() (string, error) { return "wrb_mocked_create_token", nil }
	defer func() { generateWebBindingToken = original }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(validWebReadBindingsPayload))
	req.Header.Set(requestIDHeader, "req-web-bind-create")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webReadBindingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BindingID != "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4" {
		t.Fatalf("binding_id = %q", body.BindingID)
	}
	if body.BindingToken != "wrb_mocked_create_token" {
		t.Fatalf("binding_token = %q", body.BindingToken)
	}
	if body.PairingCodeVersion != 4 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.Status != webBindingStatusActive {
		t.Fatalf("status = %q", body.Status)
	}
	if body.CreatedAt != "2026-02-13T17:00:00Z" {
		t.Fatalf("created_at = %q", body.CreatedAt)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.insertCalls != 1 {
		t.Fatalf("insertCalls = %d", db.insertCalls)
	}
	if db.updateCalls != 0 {
		t.Fatalf("updateCalls = %d", db.updateCalls)
	}
	if db.lastTokenHash != hashWebBindingCredential("wrb_mocked_create_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226") {
		t.Fatalf("lastTokenHash mismatch")
	}
}

func TestWebReadBindingsRouteSuccessUpdateExisting(t *testing.T) {
	db := &fakeWebReadBindingsDB{
		snapshot: webReadBindingUserSnapshot{
			UserID:             "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			PairingCodeVersion: 4,
		},
		existingBindingID:        "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
		existingBindingCreatedAt: time.Date(2026, 2, 13, 16, 40, 0, 0, time.UTC),
		updateStatus:             webBindingStatusActive,
	}
	server := NewServer("127.0.0.1:0", db)

	original := generateWebBindingToken
	generateWebBindingToken = func() (string, error) { return "wrb_mocked_update_token", nil }
	defer func() { generateWebBindingToken = original }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(validWebReadBindingsPayload))
	req.Header.Set(requestIDHeader, "req-web-bind-update")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webReadBindingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BindingID != "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4" {
		t.Fatalf("binding_id = %q", body.BindingID)
	}
	if body.BindingToken != "wrb_mocked_update_token" {
		t.Fatalf("binding_token = %q", body.BindingToken)
	}
	if body.PairingCodeVersion != 4 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.Status != webBindingStatusActive {
		t.Fatalf("status = %q", body.Status)
	}
	if body.CreatedAt != "2026-02-13T16:40:00Z" {
		t.Fatalf("created_at = %q", body.CreatedAt)
	}
	if db.updateCalls != 1 {
		t.Fatalf("updateCalls = %d", db.updateCalls)
	}
	if db.insertCalls != 0 {
		t.Fatalf("insertCalls = %d", db.insertCalls)
	}
}

func TestWebReadBindingsRoutePairingCodeFormatInvalid(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(`{
		"pairing_code":"1234",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"web_device_name":"Chrome@Mac"
	}`))
	req.Header.Set(requestIDHeader, "req-web-bind-format")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, pairingCodeFormatInvalidCode, "req-web-bind-format")
}

func TestWebReadBindingsRouteBusinessConflicts(t *testing.T) {
	tests := []struct {
		name       string
		db         *fakeWebReadBindingsDB
		wantCode   string
		wantStatus int
	}{
		{
			name: "pairing code invalid",
			db: &fakeWebReadBindingsDB{
				lookupErr: pgx.ErrNoRows,
			},
			wantCode:   pairingCodeInvalidCode,
			wantStatus: http.StatusConflict,
		},
		{
			name: "binding version mismatch",
			db: &fakeWebReadBindingsDB{
				snapshot: webReadBindingUserSnapshot{
					UserID:             "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
					PairingCodeVersion: 4,
				},
				insertErr: &pgconn.PgError{
					Code:    "P0001",
					Message: "[error_key=WEB_BINDING_VERSION_MISMATCH] invalid active binding version",
				},
			},
			wantCode:   webBindingVersionMismatchCode,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			original := generateWebBindingToken
			generateWebBindingToken = func() (string, error) { return "wrb_mocked_conflict_token", nil }
			defer func() { generateWebBindingToken = original }()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(validWebReadBindingsPayload))
			req.Header.Set(requestIDHeader, "req-web-bind-conflict")
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, tc.wantStatus, tc.wantCode, "req-web-bind-conflict")
		})
	}
}

func TestWebReadBindingsRouteRateLimitBlocked(t *testing.T) {
	db := &fakeWebReadBindingsDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(0),
	}, time.Date(2026, 2, 13, 17, 10, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(validWebReadBindingsPayload))
	req.Header.Set(requestIDHeader, "req-web-bind-rate-limit")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-web-bind-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestWebReadBindingsRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsPath, strings.NewReader(`{
		"pairing_code":"24069175",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"web_device_name":"Chrome@Mac",
		"unknown":"x"
	}`))
	req.Header.Set(requestIDHeader, "req-web-bind-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-web-bind-unknown")
}

type fakeWebReadBindingsDB struct {
	snapshot                 webReadBindingUserSnapshot
	lookupErr                error
	selectExistingErr        error
	existingBindingID        string
	existingBindingCreatedAt time.Time
	updateErr                error
	updateStatus             string
	insertErr                error
	insertBindingID          string
	insertStatus             string
	insertCreatedAt          time.Time
	withTxCalls              int
	selectCalls              int
	updateCalls              int
	insertCalls              int
	lastTokenHash            string
}

func (f *fakeWebReadBindingsDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebReadBindingsDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeWebReadBindingsTx{db: f})
}

type fakeWebReadBindingsTx struct {
	db *fakeWebReadBindingsDB
}

type fakeWebReadBindingRow struct {
	scanFn func(dest ...any) error
}

func (r fakeWebReadBindingRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeWebReadBindingsTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeWebReadBindingsTx) Commit(context.Context) error   { return nil }
func (f *fakeWebReadBindingsTx) Rollback(context.Context) error { return nil }
func (f *fakeWebReadBindingsTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebReadBindingsTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeWebReadBindingsTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeWebReadBindingsTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebReadBindingsTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeWebReadBindingsTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeWebReadBindingsTx) Conn() *pgx.Conn { return nil }

func (f *fakeWebReadBindingsTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM users") && strings.Contains(query, "WHERE pairing_code = $1"):
		return fakeWebReadBindingRow{scanFn: func(dest ...any) error {
			if f.db.lookupErr != nil {
				return f.db.lookupErr
			}
			if len(dest) != 2 {
				return errors.New("invalid destination fields for user snapshot")
			}
			userIDPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			versionPtr, ok := dest[1].(*int64)
			if !ok {
				return errors.New("dest[1] must be *int64")
			}
			*userIDPtr = f.db.snapshot.UserID
			*versionPtr = f.db.snapshot.PairingCodeVersion
			return nil
		}}
	case strings.Contains(query, "FROM web_read_bindings") && strings.Contains(query, "ORDER BY created_at DESC"):
		return fakeWebReadBindingRow{scanFn: func(dest ...any) error {
			f.db.selectCalls++
			if f.db.selectExistingErr != nil {
				return f.db.selectExistingErr
			}
			if strings.TrimSpace(f.db.existingBindingID) == "" {
				return pgx.ErrNoRows
			}
			if len(dest) != 2 {
				return errors.New("invalid destination fields for existing binding")
			}
			idPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			createdAtPtr, ok := dest[1].(*time.Time)
			if !ok {
				return errors.New("dest[1] must be *time.Time")
			}
			*idPtr = f.db.existingBindingID
			*createdAtPtr = f.db.existingBindingCreatedAt
			return nil
		}}
	case strings.Contains(query, "UPDATE web_read_bindings") && strings.Contains(query, "SET token_hash = $2"):
		return fakeWebReadBindingRow{scanFn: func(dest ...any) error {
			f.db.updateCalls++
			if f.db.updateErr != nil {
				return f.db.updateErr
			}
			if len(args) >= 2 {
				if tokenHash, ok := args[1].(string); ok {
					f.db.lastTokenHash = tokenHash
				}
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for updated binding")
			}
			idPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			statusPtr, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			createdAtPtr, ok := dest[2].(*time.Time)
			if !ok {
				return errors.New("dest[2] must be *time.Time")
			}
			*idPtr = f.db.existingBindingID
			if strings.TrimSpace(f.db.updateStatus) == "" {
				*statusPtr = webBindingStatusActive
			} else {
				*statusPtr = f.db.updateStatus
			}
			*createdAtPtr = f.db.existingBindingCreatedAt
			return nil
		}}
	case strings.Contains(query, "INSERT INTO web_read_bindings"):
		return fakeWebReadBindingRow{scanFn: func(dest ...any) error {
			f.db.insertCalls++
			if f.db.insertErr != nil {
				return f.db.insertErr
			}
			if len(args) >= 3 {
				if tokenHash, ok := args[2].(string); ok {
					f.db.lastTokenHash = tokenHash
				}
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for inserted binding")
			}
			idPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			statusPtr, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			createdAtPtr, ok := dest[2].(*time.Time)
			if !ok {
				return errors.New("dest[2] must be *time.Time")
			}
			*idPtr = f.db.insertBindingID
			if strings.TrimSpace(f.db.insertStatus) == "" {
				*statusPtr = webBindingStatusActive
			} else {
				*statusPtr = f.db.insertStatus
			}
			*createdAtPtr = f.db.insertCreatedAt
			return nil
		}}
	default:
		return fakeWebReadBindingRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
