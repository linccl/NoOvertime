package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const validPairingCodeQueryPayload = `{"ensure_generated":true}`

func TestPairingCodeQueryRouteSuccess(t *testing.T) {
	db := &fakePairingCodeQueryDB{
		snapshot: pairingCodeQuerySnapshot{
			PairingCode:        "39481726",
			PairingCodeVersion: 3,
			PairingCodeAt:      time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC),
			WriterDeviceID:     "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:        12,
			DeviceAuthorized:   true,
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(validPairingCodeQueryPayload))
	req.Header.Set(requestIDHeader, "req-pairing-query-success")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body pairingCodeQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PairingCode != "39481726" {
		t.Fatalf("pairing_code = %q", body.PairingCode)
	}
	if body.PairingCodeVersion != 3 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.PairingCodeUpdatedAt != "2026-02-13T12:00:00Z" {
		t.Fatalf("pairing_code_updated_at = %q", body.PairingCodeUpdatedAt)
	}
	if body.IsNewlyGenerated {
		t.Fatal("is_newly_generated = true, want false")
	}
	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestPairingCodeQueryRouteGenerateFirstTime(t *testing.T) {
	db := &fakePairingCodeQueryDB{
		snapshot: pairingCodeQuerySnapshot{
			PairingCode:        "",
			PairingCodeVersion: 1,
			PairingCodeAt:      time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC),
			WriterDeviceID:     "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			WriterEpoch:        12,
			DeviceAuthorized:   true,
		},
		generatedCode:    "24069175",
		generatedVersion: 1,
		generatedAt:      time.Date(2026, 2, 13, 12, 10, 0, 0, time.UTC),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(validPairingCodeQueryPayload))
	req.Header.Set(requestIDHeader, "req-pairing-query-generate")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body pairingCodeQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PairingCode != "24069175" {
		t.Fatalf("pairing_code = %q", body.PairingCode)
	}
	if body.PairingCodeVersion != 1 {
		t.Fatalf("pairing_code_version = %d", body.PairingCodeVersion)
	}
	if body.PairingCodeUpdatedAt != "2026-02-13T12:10:00Z" {
		t.Fatalf("pairing_code_updated_at = %q", body.PairingCodeUpdatedAt)
	}
	if !body.IsNewlyGenerated {
		t.Fatal("is_newly_generated = false, want true")
	}
}

func TestPairingCodeQueryRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(`{"ensure_generated":true,"unknown":"x"}`))
	req.Header.Set(requestIDHeader, "req-pairing-query-unknown")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-pairing-query-unknown")
}

func TestPairingCodeQueryRouteInvalidArgument(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(`{}`))
	req.Header.Set(requestIDHeader, "req-pairing-query-invalid")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-pairing-query-invalid")
}

func TestPairingCodeQueryRouteUnauthorizedDevice(t *testing.T) {
	db := &fakePairingCodeQueryDB{}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(validPairingCodeQueryPayload))
	req.Header.Set(requestIDHeader, "req-pairing-query-unauthorized")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedDeviceCode, "req-pairing-query-unauthorized")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestPairingCodeQueryRouteStaleWriterRejected(t *testing.T) {
	db := &fakePairingCodeQueryDB{
		snapshot: pairingCodeQuerySnapshot{
			PairingCode:        "39481726",
			PairingCodeVersion: 3,
			PairingCodeAt:      time.Date(2026, 2, 13, 12, 0, 0, 0, time.UTC),
			WriterDeviceID:     "ffffffff-ffff-ffff-ffff-ffffffffffff",
			WriterEpoch:        99,
			DeviceAuthorized:   true,
		},
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(validPairingCodeQueryPayload))
	req.Header.Set(requestIDHeader, "req-pairing-query-stale")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, staleWriterRejectedCode, "req-pairing-query-stale")
}

func TestPairingCodeQueryRouteUserNotFound(t *testing.T) {
	db := &fakePairingCodeQueryDB{
		loadErr: pgx.ErrNoRows,
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, pairingCodeQueryPath, strings.NewReader(validPairingCodeQueryPayload))
	req.Header.Set(requestIDHeader, "req-pairing-query-user-not-found")
	setPairingQueryAuthHeaders(req, "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362", "0b854f80-0213-4cb1-b5d0-95af02f137f3", 12)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusConflict, userNotFoundCode, "req-pairing-query-user-not-found")
}

func setPairingQueryAuthHeaders(req *http.Request, userID, deviceID string, writerEpoch int64) {
	req.Header.Set(deviceAuthHeader, "Bearer mock-device-token")
	req.Header.Set(deviceAuthUserIDHeader, userID)
	req.Header.Set(deviceAuthDeviceIDHeader, deviceID)
	req.Header.Set(deviceAuthWriterEpochHeader, strconv.FormatInt(writerEpoch, 10))
}

type fakePairingCodeQueryDB struct {
	snapshot         pairingCodeQuerySnapshot
	loadErr          error
	generatedCode    string
	generatedVersion int64
	generatedAt      time.Time
	generateErr      error
	currentCode      string
	currentVersion   int64
	currentAt        time.Time
	currentErr       error
	withTxCalls      int
}

func (f *fakePairingCodeQueryDB) Health(context.Context) error {
	return nil
}

func (f *fakePairingCodeQueryDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	return fn(&fakePairingCodeQueryTx{db: f})
}

type fakePairingCodeQueryTx struct {
	db *fakePairingCodeQueryDB
}

type fakePairingQueryRow struct {
	scanFn func(dest ...any) error
}

func (r fakePairingQueryRow) Scan(dest ...any) error {
	return r.scanFn(dest...)
}

func (f *fakePairingCodeQueryTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakePairingCodeQueryTx) Commit(context.Context) error   { return nil }
func (f *fakePairingCodeQueryTx) Rollback(context.Context) error { return nil }
func (f *fakePairingCodeQueryTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakePairingCodeQueryTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakePairingCodeQueryTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakePairingCodeQueryTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakePairingCodeQueryTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakePairingCodeQueryTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakePairingCodeQueryTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM users u"):
		return fakePairingQueryRow{scanFn: func(dest ...any) error {
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
	case strings.Contains(query, "UPDATE users") && strings.Contains(query, "AND pairing_code = ''"):
		return fakePairingQueryRow{scanFn: func(dest ...any) error {
			if f.db.generateErr != nil {
				return f.db.generateErr
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for generated row")
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

			*codePtr = f.db.generatedCode
			*versionPtr = f.db.generatedVersion
			*atPtr = f.db.generatedAt
			return nil
		}}
	case strings.Contains(query, "SELECT pairing_code, pairing_code_version, pairing_code_updated_at"):
		return fakePairingQueryRow{scanFn: func(dest ...any) error {
			if f.db.currentErr != nil {
				return f.db.currentErr
			}
			if len(dest) != 3 {
				return errors.New("invalid destination fields for current row")
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
		return fakePairingQueryRow{scanFn: func(dest ...any) error {
			return errors.New("unexpected query: " + query)
		}}
	}
}

func (f *fakePairingCodeQueryTx) Conn() *pgx.Conn { return nil }
