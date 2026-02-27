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

const validWebReadBindingsAuthPayload = `{
	"binding_token":"wrb_valid_binding_token",
	"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226"
}`

func TestWebReadBindingsAuthRouteSuccess(t *testing.T) {
	db := &fakeWebReadBindingsAuthDB{
		credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
		snapshot: webReadBindingsAuthSnapshot{
			BindingID:                 "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4",
			UserID:                    "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			Status:                    webBindingStatusActive,
			BindingPairingCodeVersion: 4,
			CurrentPairingCodeVersion: 4,
		},
		lastSeenAt: time.Date(2026, 2, 13, 18, 0, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(validWebReadBindingsAuthPayload))
	req.Header.Set(requestIDHeader, "req-web-auth-success")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body webReadBindingsAuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BindingID != "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4" {
		t.Fatalf("binding_id = %q", body.BindingID)
	}
	if body.UserID != "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362" {
		t.Fatalf("user_id = %q", body.UserID)
	}
	if body.Status != webBindingStatusActive {
		t.Fatalf("status = %q", body.Status)
	}
	if body.PairingCodeVersion != 4 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.LastSeenAt != "2026-02-13T18:00:00Z" {
		t.Fatalf("last_seen_at = %q", body.LastSeenAt)
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.lastTouchBindingID != "6f9c8306-5f7f-45d5-bf84-0a31f7066bd4" {
		t.Fatalf("lastTouchBindingID = %q", db.lastTouchBindingID)
	}
}

func TestWebReadBindingsAuthRouteUnauthorizedWebToken(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		db      *fakeWebReadBindingsAuthDB
	}{
		{
			name: "token format invalid",
			payload: `{
				"binding_token":"invalid-token",
				"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226"
			}`,
			db: &fakeWebReadBindingsAuthDB{},
		},
		{
			name:    "token not found",
			payload: validWebReadBindingsAuthPayload,
			db: &fakeWebReadBindingsAuthDB{
				credentialHash: hashWebBindingCredential("wrb_other_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
		{
			name: "token fingerprint mismatch",
			payload: `{
				"binding_token":"wrb_valid_binding_token",
				"client_fingerprint":"ffffffffffffffffffffffffffffffff"
			}`,
			db: &fakeWebReadBindingsAuthDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(tc.payload))
			req.Header.Set(requestIDHeader, "req-web-auth-unauthorized")
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedWebTokenCode, "req-web-auth-unauthorized")
		})
	}
}

func TestWebReadBindingsAuthRouteBusinessConflicts(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   webReadBindingsAuthSnapshot
		wantCode   string
		wantStatus int
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
			wantCode:   webBindingReactivateDeniedCode,
			wantStatus: http.StatusConflict,
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
			wantCode:   webBindingVersionMismatchCode,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeWebReadBindingsAuthDB{
				credentialHash: hashWebBindingCredential("wrb_valid_binding_token", "9cfce7bcd5d6dfac2697fdf1f5b9f226"),
				snapshot:       tc.snapshot,
			}
			server := NewServer("127.0.0.1:0", db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(validWebReadBindingsAuthPayload))
			req.Header.Set(requestIDHeader, "req-web-auth-conflict")
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, tc.wantStatus, tc.wantCode, "req-web-auth-conflict")
		})
	}
}

func TestWebReadBindingsAuthRouteRateLimitBlocked(t *testing.T) {
	db := &fakeWebReadBindingsAuthDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(10),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
		rateLimitScenePairingReset:     testPolicy(10),
		rateLimitSceneWebPairBind:      testPolicy(0),
	}, time.Date(2026, 2, 13, 18, 10, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(validWebReadBindingsAuthPayload))
	req.Header.Set(requestIDHeader, "req-web-auth-rate-limit")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-web-auth-rate-limit")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestWebReadBindingsAuthRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, webReadBindingsAuthPath, strings.NewReader(`{
		"binding_token":"wrb_valid_binding_token",
		"client_fingerprint":"9cfce7bcd5d6dfac2697fdf1f5b9f226",
		"unknown":"x"
	}`))
	req.Header.Set(requestIDHeader, "req-web-auth-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-web-auth-unknown")
}

type fakeWebReadBindingsAuthDB struct {
	credentialHash     string
	snapshot           webReadBindingsAuthSnapshot
	loadErr            error
	touchErr           error
	lastSeenAt         time.Time
	withTxCalls        int
	lastLookupHash     string
	lastTouchBindingID string
}

func (f *fakeWebReadBindingsAuthDB) Health(context.Context) error {
	return nil
}

func (f *fakeWebReadBindingsAuthDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakeWebReadBindingsAuthTx{db: f})
}

type fakeWebReadBindingsAuthTx struct {
	db *fakeWebReadBindingsAuthDB
}

type fakeWebReadBindingsAuthRow struct {
	scanFn func(dest ...any) error
}

func (r fakeWebReadBindingsAuthRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakeWebReadBindingsAuthTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeWebReadBindingsAuthTx) Commit(context.Context) error   { return nil }
func (f *fakeWebReadBindingsAuthTx) Rollback(context.Context) error { return nil }
func (f *fakeWebReadBindingsAuthTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeWebReadBindingsAuthTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeWebReadBindingsAuthTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeWebReadBindingsAuthTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeWebReadBindingsAuthTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeWebReadBindingsAuthTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeWebReadBindingsAuthTx) Conn() *pgx.Conn { return nil }

func (f *fakeWebReadBindingsAuthTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM web_read_bindings b") && strings.Contains(query, "WHERE b.token_hash = $1"):
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
			bindingIDPtr, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			userIDPtr, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			statusPtr, ok := dest[2].(*string)
			if !ok {
				return errors.New("dest[2] must be *string")
			}
			bindingVersionPtr, ok := dest[3].(*int64)
			if !ok {
				return errors.New("dest[3] must be *int64")
			}
			currentVersionPtr, ok := dest[4].(*int64)
			if !ok {
				return errors.New("dest[4] must be *int64")
			}

			*bindingIDPtr = f.db.snapshot.BindingID
			*userIDPtr = f.db.snapshot.UserID
			*statusPtr = f.db.snapshot.Status
			*bindingVersionPtr = f.db.snapshot.BindingPairingCodeVersion
			*currentVersionPtr = f.db.snapshot.CurrentPairingCodeVersion
			return nil
		}}
	case strings.Contains(query, "UPDATE web_read_bindings") && strings.Contains(query, "SET last_seen_at = now()"):
		return fakeWebReadBindingsAuthRow{scanFn: func(dest ...any) error {
			if f.db.touchErr != nil {
				return f.db.touchErr
			}
			if len(args) >= 1 {
				if bindingID, ok := args[0].(string); ok {
					f.db.lastTouchBindingID = bindingID
				}
			}
			if len(dest) != 1 {
				return errors.New("invalid destination fields for last_seen_at")
			}
			lastSeenAtPtr, ok := dest[0].(*time.Time)
			if !ok {
				return errors.New("dest[0] must be *time.Time")
			}
			*lastSeenAtPtr = f.db.lastSeenAt
			return nil
		}}
	default:
		return fakeWebReadBindingsAuthRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}
