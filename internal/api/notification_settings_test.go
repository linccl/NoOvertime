package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const notificationSettingsToken = "tok_notification_bound"

func TestNotificationSettingsGetDefaultWhenMissing(t *testing.T) {
	db := newFakeNotificationSettingsDB()
	db.seedToken(notificationSettingsToken, testUserID, testDeviceID, testWriterEpoch, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, notificationSettingsPath, nil)
	req.Header.Set(requestIDHeader, "req-notify-get-default")
	req.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body notificationSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ServerEndReminderEnabled || body.NotificationConfigured || body.NotificationURLMasked != "" {
		t.Fatalf("unexpected default response: %+v", body)
	}
}

func TestNotificationSettingsRejectsUnauthenticated(t *testing.T) {
	db := newFakeNotificationSettingsDB()
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, notificationSettingsPath, nil)
	req.Header.Set(requestIDHeader, "req-notify-unauth")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedMobileTokenCode, "req-notify-unauth")
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestNotificationSettingsRejectsUnboundToken(t *testing.T) {
	db := newFakeNotificationSettingsDB()
	db.seedToken(notificationSettingsToken, "", testDeviceID, testWriterEpoch, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, notificationSettingsPath, nil)
	req.Header.Set(requestIDHeader, "req-notify-unbound")
	req.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusConflict, userIDNotReadyCode, "req-notify-unbound")
}

func TestNotificationSettingsPutRejectsNonHTTPSWithoutLeaking(t *testing.T) {
	db := newFakeNotificationSettingsDB()
	db.seedToken(notificationSettingsToken, testUserID, testDeviceID, testWriterEpoch, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)
	rawURL := "http://example.com/notify?token=secret-token-value"
	rawToken := "secret-token-value"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, notificationSettingsPath, strings.NewReader(`{
		"server_end_reminder_enabled": true,
		"notification_url": "`+rawURL+`",
		"notification_token": "`+rawToken+`"
	}`))
	req.Header.Set(requestIDHeader, "req-notify-put-http")
	req.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, invalidArgumentCode, "req-notify-put-http")
	if strings.Contains(rec.Body.String(), rawURL) || strings.Contains(rec.Body.String(), rawToken) {
		t.Fatalf("response leaked secret: %s", rec.Body.String())
	}
}

func TestNotificationSettingsPutGetDeleteUsesBearerBoundUser(t *testing.T) {
	boundUserID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	forgedUserID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	db := newFakeNotificationSettingsDB()
	db.seedToken(notificationSettingsToken, boundUserID, testDeviceID, testWriterEpoch, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)

	put := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, notificationSettingsPath, strings.NewReader(`{
		"server_end_reminder_enabled": false,
		"notification_url": "https://example.com/notify?token=secret-token-value",
		"notification_token": "secret-token-value"
	}`))
	putReq.Header.Set(requestIDHeader, "req-notify-put")
	putReq.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	putReq.Header.Set("X-User-ID", forgedUserID)
	server.httpServer.Handler.ServeHTTP(put, putReq)

	if put.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", put.Code, put.Body.String())
	}
	if db.lastSettingsUserID != boundUserID {
		t.Fatalf("last settings user_id = %q", db.lastSettingsUserID)
	}
	if db.cancelCalls != 1 {
		t.Fatalf("cancelCalls = %d", db.cancelCalls)
	}
	assertNoNotificationSecretLeak(t, put.Body.String())

	get := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, notificationSettingsPath, nil)
	getReq.Header.Set(requestIDHeader, "req-notify-get")
	getReq.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	getReq.Header.Set("X-User-ID", forgedUserID)
	server.httpServer.Handler.ServeHTTP(get, getReq)

	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	assertNoNotificationSecretLeak(t, get.Body.String())
	if !strings.Contains(get.Body.String(), "https://example.com/...") {
		t.Fatalf("response missing masked URL: %s", get.Body.String())
	}

	del := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, notificationSettingsPath, nil)
	delReq.Header.Set(requestIDHeader, "req-notify-delete")
	delReq.Header.Set(authorizationHeader, "Bearer "+notificationSettingsToken)
	server.httpServer.Handler.ServeHTTP(del, delReq)

	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", del.Code, del.Body.String())
	}
	if db.cancelCalls != 2 {
		t.Fatalf("cancelCalls after delete = %d", db.cancelCalls)
	}
}

func TestNotificationSettingsRejectsForgedBearerToken(t *testing.T) {
	db := newFakeNotificationSettingsDB()
	db.seedToken(notificationSettingsToken, testUserID, testDeviceID, testWriterEpoch, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, notificationSettingsPath, nil)
	req.Header.Set(requestIDHeader, "req-notify-forged-bearer")
	req.Header.Set(authorizationHeader, "Bearer forged-token")
	server.httpServer.Handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, unauthorizedMobileTokenCode, "req-notify-forged-bearer")
}

func TestCancelUnsentReminderEventsSQLKeepsSent(t *testing.T) {
	tx := &fakeNotificationSettingsTx{}
	if err := cancelUnsentReminderEvents(context.Background(), tx, testUserID); err != nil {
		t.Fatalf("cancelUnsentReminderEvents() error = %v", err)
	}
	if !strings.Contains(tx.lastExecQuery, "status IN ('PENDING', 'SENDING', 'FAILED')") {
		t.Fatalf("cancel query does not limit unsent statuses: %s", tx.lastExecQuery)
	}
	if strings.Contains(tx.lastExecQuery, "'SENT'") {
		t.Fatalf("cancel query touches SENT: %s", tx.lastExecQuery)
	}
	if tx.lastExecArgs[1] != notificationSettingsCancelReason {
		t.Fatalf("cancel reason = %v", tx.lastExecArgs[1])
	}
}

func assertNoNotificationSecretLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"https://example.com/notify?token=secret-token-value",
		"secret-token-value",
		notificationSettingsToken,
		"Authorization",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

type fakeNotificationSettingsDB struct {
	tokens             map[string]fakeMobileTokenRecord
	settings           map[string]notificationSettingsSnapshot
	cancelCalls        int
	cancelUserID       string
	withTxCalls        int
	lastSettingsUserID string
}

func newFakeNotificationSettingsDB() *fakeNotificationSettingsDB {
	return &fakeNotificationSettingsDB{
		tokens:   make(map[string]fakeMobileTokenRecord),
		settings: make(map[string]notificationSettingsSnapshot),
	}
}

func (f *fakeNotificationSettingsDB) Health(context.Context) error {
	return nil
}

func (f *fakeNotificationSettingsDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	tx := &fakeNotificationSettingsTx{db: f}
	return fn(tx)
}

func (f *fakeNotificationSettingsDB) seedToken(token, userID, deviceID string, writerEpoch int64, status, fingerprintHash string) {
	f.tokens[hashMobileToken(token)] = fakeMobileTokenRecord{
		UserID:          userID,
		DeviceID:        deviceID,
		WriterEpoch:     writerEpoch,
		Status:          status,
		FingerprintHash: fingerprintHash,
	}
}

type fakeNotificationSettingsTx struct {
	db            *fakeNotificationSettingsDB
	lastExecQuery string
	lastExecArgs  []any
}

func (f *fakeNotificationSettingsTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeNotificationSettingsTx) Commit(context.Context) error   { return nil }
func (f *fakeNotificationSettingsTx) Rollback(context.Context) error { return nil }
func (f *fakeNotificationSettingsTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeNotificationSettingsTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (f *fakeNotificationSettingsTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }
func (f *fakeNotificationSettingsTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (f *fakeNotificationSettingsTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	f.lastExecQuery = query
	f.lastExecArgs = args
	if f.db != nil && strings.Contains(query, "UPDATE punch_reminder_events") {
		f.db.cancelCalls++
		f.db.cancelUserID = args[0].(string)
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeNotificationSettingsTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeNotificationSettingsTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	switch {
	case strings.Contains(query, "FROM mobile_tokens"):
		return f.queryRowMobileToken(args)
	case strings.Contains(query, "FROM user_notification_settings"):
		return f.queryRowNotificationSettings(args)
	case strings.Contains(query, "INSERT INTO user_notification_settings"):
		return f.queryRowUpsertNotificationSettings(args)
	case strings.Contains(query, "UPDATE user_notification_settings"):
		return f.queryRowDisableNotificationSettings(args)
	default:
		return fakeNotificationSettingsRow{err: fmt.Errorf("unexpected query: %s", query)}
	}
}

func (f *fakeNotificationSettingsTx) Conn() *pgx.Conn { return nil }

func (f *fakeNotificationSettingsTx) queryRowMobileToken(args []any) pgx.Row {
	if len(args) != 1 {
		return fakeNotificationSettingsRow{err: fmt.Errorf("select token args = %d", len(args))}
	}
	record, ok := f.db.tokens[args[0].(string)]
	if !ok {
		return fakeNotificationSettingsRow{err: pgx.ErrNoRows}
	}
	return fakeNotificationSettingsRow{values: []any{
		record.UserID,
		record.DeviceID,
		record.WriterEpoch,
		record.Status,
		record.FingerprintHash,
	}}
}

func (f *fakeNotificationSettingsTx) queryRowNotificationSettings(args []any) pgx.Row {
	userID := args[0].(string)
	f.db.lastSettingsUserID = userID
	snapshot, ok := f.db.settings[userID]
	if !ok {
		return fakeNotificationSettingsRow{err: pgx.ErrNoRows}
	}
	return rowForNotificationSettings(snapshot)
}

func (f *fakeNotificationSettingsTx) queryRowUpsertNotificationSettings(args []any) pgx.Row {
	userID := args[0].(string)
	f.db.lastSettingsUserID = userID
	existing := f.db.settings[userID]
	nextVersion := existing.ConfigVersion + 1
	if nextVersion <= 0 {
		nextVersion = 1
	}
	snapshot := notificationSettingsSnapshot{
		ServerEndReminderEnabled: args[1].(bool),
		NotificationURL:          args[2].(string),
		ConfigVersion:            nextVersion,
		UpdatedAt:                time.Date(2026, 6, 3, 1, 2, 3, 0, time.UTC),
		Configured:               true,
	}
	f.db.settings[userID] = snapshot
	return rowForNotificationSettings(snapshot)
}

func (f *fakeNotificationSettingsTx) queryRowDisableNotificationSettings(args []any) pgx.Row {
	userID := args[0].(string)
	f.db.lastSettingsUserID = userID
	snapshot, ok := f.db.settings[userID]
	if !ok {
		return fakeNotificationSettingsRow{err: pgx.ErrNoRows}
	}
	snapshot.ServerEndReminderEnabled = false
	snapshot.ConfigVersion++
	snapshot.UpdatedAt = time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
	f.db.settings[userID] = snapshot
	return rowForNotificationSettings(snapshot)
}

func rowForNotificationSettings(snapshot notificationSettingsSnapshot) pgx.Row {
	return fakeNotificationSettingsRow{values: []any{
		snapshot.ServerEndReminderEnabled,
		snapshot.NotificationURL,
		snapshot.ConfigVersion,
		snapshot.UpdatedAt,
	}}
}

type fakeNotificationSettingsRow struct {
	values []any
	err    error
}

func (f fakeNotificationSettingsRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != len(f.values) {
		return fmt.Errorf("scan dest=%d values=%d", len(dest), len(f.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], f.values[i]); err != nil {
			return err
		}
	}
	return nil
}
