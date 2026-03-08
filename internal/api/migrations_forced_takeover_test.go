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

const validMigrationForcedTakeoverBody = `{
	"pairing_code":"39481726",
	"recovery_code":"AB12CD34EF56GH78",
	"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c"
}`

const validMigrationTakeoverTokenOnlyBody = `{
	"pairing_code":"39481726",
	"recovery_code":"AB12CD34EF56GH78"
}`

func TestMigrationForcedTakeoverRouteSuccess(t *testing.T) {
	db := &fakeMigrationForcedTakeoverDB{
		userSnapshot: fakeForcedTakeoverUserSnapshot{
			userID:         "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
		},
		recoveryMatched:    true,
		migrationRequestID: "ac5af84e-c497-4344-8994-9fef4ec54ab0",
		writerEpoch:        14,
		completedAt:        time.Date(2026, 2, 13, 15, 0, 0, 0, time.UTC),
		mobileTokens:       newMigrationTestMobileTokens(),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(validMigrationForcedTakeoverBody))
	req.Header.Set(requestIDHeader, "req-forced-success")
	setSyncAuthHeader(req, testTargetDeviceToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body migrationForcedTakeoverResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MigrationRequestID != "ac5af84e-c497-4344-8994-9fef4ec54ab0" {
		t.Fatalf("migration_request_id = %q", body.MigrationRequestID)
	}
	if body.Status != "COMPLETED" {
		t.Fatalf("status = %q", body.Status)
	}
	if body.Mode != "FORCED" {
		t.Fatalf("mode = %q", body.Mode)
	}
	if body.WriterDeviceID != "f2df11ef-7240-42b2-8ceb-623ad7711e0c" {
		t.Fatalf("writer_device_id = %q", body.WriterDeviceID)
	}
	if body.WriterEpoch != 14 {
		t.Fatalf("writer_epoch = %d", body.WriterEpoch)
	}
	if body.CompletedAt != "2026-02-13T15:00:00Z" {
		t.Fatalf("completed_at = %q", body.CompletedAt)
	}

	sourceRecord := db.mobileTokens[hashMobileToken(testSyncToken)]
	if sourceRecord.Status != mobileTokenStateRotated {
		t.Fatalf("source token status = %q", sourceRecord.Status)
	}
	targetRecord := db.mobileTokens[hashMobileToken(testTargetDeviceToken)]
	if targetRecord.UserID != db.userSnapshot.userID {
		t.Fatalf("target token user_id = %q", targetRecord.UserID)
	}
	if targetRecord.WriterEpoch != db.writerEpoch {
		t.Fatalf("target token writer_epoch = %d", targetRecord.WriterEpoch)
	}
	if targetRecord.Status != mobileTokenStateActive {
		t.Fatalf("target token status = %q", targetRecord.Status)
	}
}

func TestMigrationTakeoverRouteTokenOnlyBodySuccess(t *testing.T) {
	db := &fakeMigrationForcedTakeoverDB{
		userSnapshot: fakeForcedTakeoverUserSnapshot{
			userID:         "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
		},
		recoveryMatched:    true,
		migrationRequestID: "50d8f081-baa5-4fc0-ab40-c4236068db59",
		writerEpoch:        14,
		completedAt:        time.Date(2026, 2, 13, 15, 5, 0, 0, time.UTC),
		mobileTokens:       newMigrationTestMobileTokens(),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsTakeoverPath, strings.NewReader(validMigrationTakeoverTokenOnlyBody))
	req.Header.Set(requestIDHeader, "req-takeover-success")
	setSyncAuthHeader(req, testTargetDeviceToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	sourceRecord := db.mobileTokens[hashMobileToken(testSyncToken)]
	if sourceRecord.Status != mobileTokenStateRotated {
		t.Fatalf("source token status = %q", sourceRecord.Status)
	}
	targetRecord := db.mobileTokens[hashMobileToken(testTargetDeviceToken)]
	if targetRecord.UserID != db.userSnapshot.userID {
		t.Fatalf("target token user_id = %q", targetRecord.UserID)
	}
	if targetRecord.WriterEpoch != db.writerEpoch {
		t.Fatalf("target token writer_epoch = %d", targetRecord.WriterEpoch)
	}
}

func TestMigrationForcedTakeoverRoutePairingCodeFormatInvalid(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(`{
		"pairing_code":"1234",
		"recovery_code":"AB12CD34EF56GH78",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c"
	}`))
	req.Header.Set(requestIDHeader, "req-forced-pairing-format")
	setSyncAuthHeader(req, testTargetDeviceToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, pairingCodeFormatInvalidCode, "req-forced-pairing-format")
}

func TestMigrationForcedTakeoverRoutePairingOrRecoveryInvalid(t *testing.T) {
	tests := []struct {
		name     string
		db       *fakeMigrationForcedTakeoverDB
		wantCode string
	}{
		{
			name: "pairing code invalid",
			db: &fakeMigrationForcedTakeoverDB{
				userNotFound: true,
			},
			wantCode: pairingCodeInvalidCode,
		},
		{
			name: "recovery code invalid",
			db: &fakeMigrationForcedTakeoverDB{
				userSnapshot: fakeForcedTakeoverUserSnapshot{
					userID:         "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
					writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
				},
				recoveryMatched: false,
			},
			wantCode: recoveryCodeInvalidCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("127.0.0.1:0", tc.db)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(validMigrationForcedTakeoverBody))
			req.Header.Set(requestIDHeader, "req-forced-invalid")
			setSyncAuthHeader(req, testTargetDeviceToken)
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusConflict, tc.wantCode, "req-forced-invalid")
		})
	}
}

func TestMigrationForcedTakeoverRouteStateConflict(t *testing.T) {
	tests := []struct {
		name         string
		overrideBody string
		failOnInsert error
	}{
		{
			name: "transition invalid from db rule",
			failOnInsert: &pgconn.PgError{
				Code:    "P0001",
				Message: "[error_key=MIGRATION_TRANSITION_INVALID] invalid transition",
			},
		},
		{
			name: "pending exists unique index",
			failOnInsert: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uk_migration_user_pending",
				Message:        "duplicate key",
			},
		},
		{
			name:         "to_device equals current writer",
			overrideBody: `{"pairing_code":"39481726","recovery_code":"AB12CD34EF56GH78","to_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeMigrationForcedTakeoverDB{
				userSnapshot: fakeForcedTakeoverUserSnapshot{
					userID:         "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
					writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
				},
				recoveryMatched: true,
				failOnInsert:    tc.failOnInsert,
			}
			server := NewServer("127.0.0.1:0", db)
			requestBody := validMigrationForcedTakeoverBody
			if tc.overrideBody != "" {
				requestBody = tc.overrideBody
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(requestBody))
			req.Header.Set(requestIDHeader, "req-forced-state-conflict")
			token := testTargetDeviceToken
			if tc.name == "to_device equals current writer" {
				token = testSyncToken
			}
			setSyncAuthHeader(req, token)
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusConflict, migrationStateInvalidCode, "req-forced-state-conflict")
		})
	}
}

func TestMigrationForcedTakeoverRateLimitOrder(t *testing.T) {
	t.Run("recovery blocks before migration request", func(t *testing.T) {
		server := NewServer("127.0.0.1:0", &fakeMigrationForcedTakeoverDB{})
		server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
			rateLimitSceneRecoveryVerify:   testPolicy(0),
			rateLimitSceneMigrationRequest: testPolicy(0),
			rateLimitSceneMigrationConfirm: testPolicy(10),
		}, time.Date(2026, 2, 13, 16, 0, 0, 0, time.UTC))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(validMigrationForcedTakeoverBody))
		req.Header.Set(requestIDHeader, "req-forced-rate-order-1")
		setSyncAuthHeader(req, testTargetDeviceToken)
		server.httpServer.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-forced-rate-order-1")
		assertRateLimitSceneCounter(t, server.migrationRateGate.counters, rateLimitSceneRecoveryVerify, true)
		assertRateLimitSceneCounter(t, server.migrationRateGate.counters, rateLimitSceneMigrationRequest, false)
	})

	t.Run("migration request evaluated after recovery verify", func(t *testing.T) {
		server := NewServer("127.0.0.1:0", &fakeMigrationForcedTakeoverDB{})
		server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
			rateLimitSceneRecoveryVerify:   testPolicy(10),
			rateLimitSceneMigrationRequest: testPolicy(0),
			rateLimitSceneMigrationConfirm: testPolicy(10),
		}, time.Date(2026, 2, 13, 16, 1, 0, 0, time.UTC))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(validMigrationForcedTakeoverBody))
		req.Header.Set(requestIDHeader, "req-forced-rate-order-2")
		setSyncAuthHeader(req, testTargetDeviceToken)
		server.httpServer.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-forced-rate-order-2")
		assertRateLimitSceneCounter(t, server.migrationRateGate.counters, rateLimitSceneRecoveryVerify, true)
		assertRateLimitSceneCounter(t, server.migrationRateGate.counters, rateLimitSceneMigrationRequest, true)
	})
}

func TestMigrationForcedTakeoverRouteUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationsForcedTakeoverPath, strings.NewReader(`{
		"pairing_code":"39481726",
		"recovery_code":"AB12CD34EF56GH78",
		"to_device_id":"f2df11ef-7240-42b2-8ceb-623ad7711e0c",
		"unknown":"x"
	}`))
	req.Header.Set(requestIDHeader, "req-forced-unknown")
	setSyncAuthHeader(req, testTargetDeviceToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, unknownFieldCode, "req-forced-unknown")
}

func assertRateLimitSceneCounter(t *testing.T, counters map[string]*rateLimitCounter, scene string, wantExists bool) {
	t.Helper()

	exists := false
	for key := range counters {
		if strings.HasPrefix(key, scene+"|") {
			exists = true
			break
		}
	}
	if exists != wantExists {
		t.Fatalf("scene %s counters exists=%v want=%v", scene, exists, wantExists)
	}
}

type fakeMigrationForcedTakeoverDB struct {
	userSnapshot       fakeForcedTakeoverUserSnapshot
	userNotFound       bool
	recoveryMatched    bool
	migrationRequestID string
	writerEpoch        int64
	completedAt        time.Time
	failOnInsert       error
	mobileTokens       map[string]fakeMobileTokenRecord
}

type fakeForcedTakeoverUserSnapshot struct {
	userID         string
	writerDeviceID string
}

func (f *fakeMigrationForcedTakeoverDB) Health(context.Context) error {
	return nil
}

func (f *fakeMigrationForcedTakeoverDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	return testMobileAuthContextForToken(header.Token), nil
}

func (f *fakeMigrationForcedTakeoverDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx := &fakeMigrationForcedTakeoverTx{
		db:           f,
		mobileTokens: cloneFakeMobileTokenRecords(f.mobileTokens),
	}
	if err := fn(tx); err != nil {
		return err
	}
	f.mobileTokens = tx.mobileTokens
	return nil
}

type fakeMigrationForcedTakeoverTx struct {
	db           *fakeMigrationForcedTakeoverDB
	mobileTokens map[string]fakeMobileTokenRecord
}

func (f *fakeMigrationForcedTakeoverTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeMigrationForcedTakeoverTx) Commit(context.Context) error   { return nil }
func (f *fakeMigrationForcedTakeoverTx) Rollback(context.Context) error { return nil }
func (f *fakeMigrationForcedTakeoverTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeMigrationForcedTakeoverTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeMigrationForcedTakeoverTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeMigrationForcedTakeoverTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeMigrationForcedTakeoverTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "UPDATE mobile_tokens") && strings.Contains(query, "SET status = 'ROTATED'"):
		userID, _ := args[0].(string)
		rotateMigrationTestActiveMobileTokensByUser(f.mobileTokens, userID)
		return pgconn.CommandTag{}, nil
	case strings.Contains(query, "UPDATE mobile_tokens") && strings.Contains(query, "SET user_id = $2::uuid"):
		deviceID, _ := args[0].(string)
		userID, _ := args[1].(string)
		writerEpoch, _ := args[2].(int64)
		return pgconn.CommandTag{}, bindMigrationTestMobileTokensByDevice(f.mobileTokens, deviceID, userID, writerEpoch)
	default:
		return pgconn.CommandTag{}, nil
	}
}
func (f *fakeMigrationForcedTakeoverTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeMigrationForcedTakeoverTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM users") && strings.Contains(query, "WHERE pairing_code"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if f.db.userNotFound {
				return pgx.ErrNoRows
			}
			if len(dest) != 2 {
				return errors.New("invalid user snapshot destination")
			}
			userID, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			writerID, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			*userID = f.db.userSnapshot.userID
			*writerID = f.db.userSnapshot.writerDeviceID
			return nil
		}}
	case strings.Contains(query, "SELECT recovery_code_hash = crypt"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if len(dest) != 1 {
				return errors.New("invalid recovery verification destination")
			}
			matched, ok := dest[0].(*bool)
			if !ok {
				return errors.New("dest[0] must be *bool")
			}
			*matched = f.db.recoveryMatched
			return nil
		}}
	case strings.Contains(query, "INSERT INTO migration_requests") && strings.Contains(query, "RETURNING id"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if f.db.failOnInsert != nil {
				return f.db.failOnInsert
			}
			if len(dest) != 1 {
				return errors.New("invalid migration id destination")
			}
			id, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			*id = f.db.migrationRequestID
			return nil
		}}
	case strings.Contains(query, "UPDATE users") && strings.Contains(query, "RETURNING writer_epoch"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if len(dest) != 1 {
				return errors.New("invalid writer epoch destination")
			}
			writerEpoch, ok := dest[0].(*int64)
			if !ok {
				return errors.New("dest[0] must be *int64")
			}
			*writerEpoch = f.db.writerEpoch
			return nil
		}}
	case strings.Contains(query, "UPDATE migration_requests") && strings.Contains(query, "RETURNING updated_at"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if len(dest) != 1 {
				return errors.New("invalid completed_at destination")
			}
			completedAt, ok := dest[0].(*time.Time)
			if !ok {
				return errors.New("dest[0] must be *time.Time")
			}
			*completedAt = f.db.completedAt
			return nil
		}}
	default:
		return fakePGXRow{scanFn: func(...any) error {
			return errors.New("unsupported query")
		}}
	}
}
func (f *fakeMigrationForcedTakeoverTx) Conn() *pgx.Conn { return nil }
