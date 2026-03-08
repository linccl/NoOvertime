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

const validMigrationConfirmBody = `{
	"action":"CONFIRM",
	"operator_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3"
}`

func migrationConfirmPath(migrationRequestID string) string {
	return "/api/v1/migrations/" + migrationRequestID + "/confirm"
}

func TestMigrationConfirmRouteSuccess(t *testing.T) {
	now := time.Now().UTC()
	db := &fakeMigrationConfirmDB{
		migrationSnapshot: fakeMigrationConfirmSnapshot{
			userID:       "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			fromDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			toDeviceID:   "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
			status:       "PENDING",
			expiresAt:    now.Add(1 * time.Hour),
		},
		writerState: fakeWriterState{
			writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			writerEpoch:    12,
		},
		updatedWriterEpoch: 13,
		completedAt:        time.Date(2026, 2, 13, 14, 20, 0, 0, time.UTC),
		mobileTokens:       newMigrationTestMobileTokens(),
	}
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		migrationConfirmPath("f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60"),
		strings.NewReader(validMigrationConfirmBody),
	)
	req.Header.Set(requestIDHeader, "req-migration-confirm-success")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body migrationConfirmResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MigrationRequestID != "f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60" {
		t.Fatalf("migration_request_id = %q", body.MigrationRequestID)
	}
	if body.Status != "COMPLETED" {
		t.Fatalf("status = %q", body.Status)
	}
	if body.WriterDeviceID != "f2df11ef-7240-42b2-8ceb-623ad7711e0c" {
		t.Fatalf("writer_device_id = %q", body.WriterDeviceID)
	}
	if body.WriterEpoch != 13 {
		t.Fatalf("writer_epoch = %d", body.WriterEpoch)
	}
	if body.RevokedDeviceID != "0b854f80-0213-4cb1-b5d0-95af02f137f3" {
		t.Fatalf("revoked_device_id = %q", body.RevokedDeviceID)
	}
	if body.CompletedAt != "2026-02-13T14:20:00Z" {
		t.Fatalf("completed_at = %q", body.CompletedAt)
	}

	sourceRecord := db.mobileTokens[hashMobileToken(testSyncToken)]
	if sourceRecord.Status != mobileTokenStateRotated {
		t.Fatalf("source token status = %q", sourceRecord.Status)
	}
	targetRecord := db.mobileTokens[hashMobileToken(testTargetDeviceToken)]
	if targetRecord.UserID != db.migrationSnapshot.userID {
		t.Fatalf("target token user_id = %q", targetRecord.UserID)
	}
	if targetRecord.WriterEpoch != db.updatedWriterEpoch {
		t.Fatalf("target token writer_epoch = %d", targetRecord.WriterEpoch)
	}
	if targetRecord.Status != mobileTokenStateActive {
		t.Fatalf("target token status = %q", targetRecord.Status)
	}
}

func TestMigrationConfirmRoutePathIDInvalid(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationConfirmPath("not-a-uuid"), strings.NewReader(validMigrationConfirmBody))
	req.Header.Set(requestIDHeader, "req-migration-confirm-invalid-path")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-migration-confirm-invalid-path")
}

func TestMigrationConfirmRouteInvalidArgumentAndUnknownField(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	path := migrationConfirmPath("f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60")

	recInvalid := httptest.NewRecorder()
	reqInvalid := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Replace(validMigrationConfirmBody, `"CONFIRM"`, `"REJECT"`, 1)))
	reqInvalid.Header.Set(requestIDHeader, "req-migration-confirm-invalid")
	setSyncAuthHeader(reqInvalid, testSyncToken)
	server.httpServer.Handler.ServeHTTP(recInvalid, reqInvalid)

	if recInvalid.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recInvalid.Code, recInvalid.Body.String())
	}
	assertErrorEnvelope(t, recInvalid, http.StatusBadRequest, invalidArgumentCode, "req-migration-confirm-invalid")

	recUnknown := httptest.NewRecorder()
	reqUnknown := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"action":"CONFIRM","operator_device_id":"0b854f80-0213-4cb1-b5d0-95af02f137f3","unknown":"x"}`))
	reqUnknown.Header.Set(requestIDHeader, "req-migration-confirm-unknown")
	setSyncAuthHeader(reqUnknown, testSyncToken)
	server.httpServer.Handler.ServeHTTP(recUnknown, reqUnknown)

	if recUnknown.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recUnknown.Code, recUnknown.Body.String())
	}
	assertErrorEnvelope(t, recUnknown, http.StatusBadRequest, unknownFieldCode, "req-migration-confirm-unknown")
}

func TestMigrationConfirmRouteRateLimitBlocked(t *testing.T) {
	db := &fakeMigrationConfirmDB{}
	server := NewServer("127.0.0.1:0", db)
	server.migrationRateGate = newTestMigrationRateGate(map[string]migrationRateLimitPolicy{
		rateLimitSceneMigrationRequest: testPolicy(10),
		rateLimitSceneMigrationConfirm: testPolicy(0),
		rateLimitSceneRecoveryVerify:   testPolicy(10),
	}, time.Date(2026, 2, 13, 14, 25, 0, 0, time.UTC))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, migrationConfirmPath("f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60"), strings.NewReader(validMigrationConfirmBody))
	req.Header.Set(requestIDHeader, "req-migration-confirm-rate-limit")
	setSyncAuthHeader(req, testSyncToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, rateLimitBlockedCode, "req-migration-confirm-rate-limit")
}

func TestMigrationConfirmRouteConflictScenarios(t *testing.T) {
	now := time.Now().UTC()
	base := fakeMigrationConfirmDB{
		migrationSnapshot: fakeMigrationConfirmSnapshot{
			userID:       "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
			fromDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			toDeviceID:   "f2df11ef-7240-42b2-8ceb-623ad7711e0c",
			status:       "PENDING",
			expiresAt:    now.Add(2 * time.Hour),
		},
		writerState: fakeWriterState{
			writerDeviceID: "0b854f80-0213-4cb1-b5d0-95af02f137f3",
			writerEpoch:    8,
		},
		updatedWriterEpoch: 9,
		completedAt:        time.Date(2026, 2, 13, 14, 30, 0, 0, time.UTC),
	}

	tests := []struct {
		name         string
		prepareDB    func(db *fakeMigrationConfirmDB)
		overrideBody string
		wantCode     string
	}{
		{
			name: "state invalid",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.migrationSnapshot.status = "COMPLETED"
			},
			wantCode: migrationStateInvalidCode,
		},
		{
			name: "expired",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.migrationSnapshot.expiresAt = now.Add(-1 * time.Minute)
			},
			wantCode: migrationExpiredCode,
		},
		{
			name: "source mismatch",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.migrationSnapshot.fromDeviceID = "11111111-1111-4111-8111-111111111111"
			},
			wantCode: migrationSourceMismatchCode,
		},
		{
			name: "stale writer",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.writerState.writerDeviceID = "99999999-9999-4999-8999-999999999999"
			},
			wantCode: staleWriterRejectedCode,
		},
		{
			name: "transition invalid mapped from db error",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.failOnUpdateConfirm = &pgconn.PgError{
					Code:    "P0001",
					Message: "[error_key=MIGRATION_TRANSITION_INVALID] invalid",
				}
			},
			wantCode: migrationStateInvalidCode,
		},
		{
			name: "source mismatch mapped from db error",
			prepareDB: func(db *fakeMigrationConfirmDB) {
				db.failOnUpdateConfirm = &pgconn.PgError{
					Code:    "P0001",
					Message: "[error_key=MIGRATION_SOURCE_MISMATCH] mismatch",
				}
			},
			wantCode: migrationSourceMismatchCode,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := base
			if tc.prepareDB != nil {
				tc.prepareDB(&db)
			}
			server := NewServer("127.0.0.1:0", &db)

			body := validMigrationConfirmBody
			if tc.overrideBody != "" {
				body = tc.overrideBody
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, migrationConfirmPath("f58e8ce4-1dba-4c4c-b5e0-d71ce357eb60"), strings.NewReader(body))
			req.Header.Set(requestIDHeader, "req-migration-confirm-conflict")
			setSyncAuthHeader(req, testSyncToken)
			server.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusConflict, tc.wantCode, "req-migration-confirm-conflict")
		})
	}
}

type fakeMigrationConfirmDB struct {
	migrationSnapshot   fakeMigrationConfirmSnapshot
	writerState         fakeWriterState
	updatedWriterEpoch  int64
	completedAt         time.Time
	failOnUpdateConfirm error
	mobileTokens        map[string]fakeMobileTokenRecord
}

type fakeMigrationConfirmSnapshot struct {
	userID       string
	fromDeviceID string
	toDeviceID   string
	status       string
	expiresAt    time.Time
}

type fakeWriterState struct {
	writerDeviceID string
	writerEpoch    int64
}

func (f *fakeMigrationConfirmDB) Health(context.Context) error {
	return nil
}

func (f *fakeMigrationConfirmDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	return testMobileAuthContextForToken(header.Token), nil
}

func (f *fakeMigrationConfirmDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx := &fakeMigrationConfirmTx{
		db:           f,
		mobileTokens: cloneFakeMobileTokenRecords(f.mobileTokens),
	}
	if err := fn(tx); err != nil {
		return err
	}
	f.mobileTokens = tx.mobileTokens
	return nil
}

type fakeMigrationConfirmTx struct {
	db           *fakeMigrationConfirmDB
	mobileTokens map[string]fakeMobileTokenRecord
}

func (f *fakeMigrationConfirmTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeMigrationConfirmTx) Commit(context.Context) error   { return nil }
func (f *fakeMigrationConfirmTx) Rollback(context.Context) error { return nil }
func (f *fakeMigrationConfirmTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeMigrationConfirmTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeMigrationConfirmTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeMigrationConfirmTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeMigrationConfirmTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "UPDATE migration_requests") && strings.Contains(query, "SET status = 'CONFIRMED'"):
		if f.db.failOnUpdateConfirm != nil {
			return pgconn.CommandTag{}, f.db.failOnUpdateConfirm
		}
		return pgconn.CommandTag{}, nil
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
func (f *fakeMigrationConfirmTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (f *fakeMigrationConfirmTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM migration_requests"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if len(dest) != 5 {
				return errors.New("invalid migration snapshot destination")
			}
			userID, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			fromID, ok := dest[1].(*string)
			if !ok {
				return errors.New("dest[1] must be *string")
			}
			toID, ok := dest[2].(*string)
			if !ok {
				return errors.New("dest[2] must be *string")
			}
			status, ok := dest[3].(*string)
			if !ok {
				return errors.New("dest[3] must be *string")
			}
			expiresAt, ok := dest[4].(*time.Time)
			if !ok {
				return errors.New("dest[4] must be *time.Time")
			}
			*userID = f.db.migrationSnapshot.userID
			*fromID = f.db.migrationSnapshot.fromDeviceID
			*toID = f.db.migrationSnapshot.toDeviceID
			*status = f.db.migrationSnapshot.status
			*expiresAt = f.db.migrationSnapshot.expiresAt
			return nil
		}}
	case strings.Contains(query, "SELECT writer_device_id, writer_epoch"):
		return fakePGXRow{scanFn: func(dest ...any) error {
			if len(dest) != 2 {
				return errors.New("invalid writer destination")
			}
			writerDeviceID, ok := dest[0].(*string)
			if !ok {
				return errors.New("dest[0] must be *string")
			}
			writerEpoch, ok := dest[1].(*int64)
			if !ok {
				return errors.New("dest[1] must be *int64")
			}
			*writerDeviceID = f.db.writerState.writerDeviceID
			*writerEpoch = f.db.writerState.writerEpoch
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
			*writerEpoch = f.db.updatedWriterEpoch
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
func (f *fakeMigrationConfirmTx) Conn() *pgx.Conn { return nil }
