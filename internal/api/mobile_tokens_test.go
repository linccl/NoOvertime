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

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const testMobileDeviceID = "0b854f80-0213-4cb1-b5d0-95af02f137f3"

func TestParseMobileTokenHeadersSuccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, tokensRotatePath, strings.NewReader(`{}`))
	request.Header.Set(authorizationHeader, "Bearer tok_test_value")
	request.Header.Set(clientFingerprintHeader, "mobile-fp-001")

	header, err := parseMobileTokenHeaders(request)
	if err != nil {
		t.Fatalf("parseMobileTokenHeaders() error = %v", err)
	}
	if header.Token != "tok_test_value" {
		t.Fatalf("token = %q", header.Token)
	}
	if header.ClientFingerprint != "mobile-fp-001" {
		t.Fatalf("client_fingerprint = %q", header.ClientFingerprint)
	}
}

func TestTokenRoutesRejectNonPostWithUnifiedError(t *testing.T) {
	server := NewServer("127.0.0.1:0", healthyDB{})
	for _, path := range []string{tokensIssuePath, tokensRotatePath} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(requestIDHeader, "req-token-method-not-allowed")
		recorder := httptest.NewRecorder()

		server.httpServer.Handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
		assertTokenErrorEnvelope(t, recorder, http.StatusMethodNotAllowed, methodNotAllowedCode, "req-token-method-not-allowed")
	}
}

func TestTokenIssueSuccess(t *testing.T) {
	db := newFakeMobileTokenDB()
	server := NewServer("127.0.0.1:0", db)
	restoreToken := stubMobileTokenGenerator(t, "tok_issue_created")
	defer restoreToken()
	restoreUUID := stubInternalUUIDGenerator(t, testMobileDeviceID)
	defer restoreUUID()

	request := httptest.NewRequest(http.MethodPost, tokensIssuePath, strings.NewReader(`{"client_fingerprint":"fp-issue"}`))
	request.Header.Set(requestIDHeader, "req-token-issue")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeMobileTokenResponse(t, recorder)
	if body.Token != "tok_issue_created" {
		t.Fatalf("token = %q", body.Token)
	}
	if body.UserID != nil {
		t.Fatalf("user_id = %v", *body.UserID)
	}
	if body.TokenStatus != mobileTokenAnonymous {
		t.Fatalf("token_status = %q", body.TokenStatus)
	}

	record := db.recordByToken("tok_issue_created")
	if record.DeviceID != testMobileDeviceID {
		t.Fatalf("device_id = %q", record.DeviceID)
	}
	if record.WriterEpoch != 1 {
		t.Fatalf("writer_epoch = %d", record.WriterEpoch)
	}
	if record.FingerprintHash != hashClientFingerprint("fp-issue") {
		t.Fatalf("fingerprint_hash = %q", record.FingerprintHash)
	}
}

func TestTokenRotateSuccessAnonymous(t *testing.T) {
	db := newFakeMobileTokenDB()
	db.seedToken("tok_old_anonymous", "", testMobileDeviceID, 1, mobileTokenStateActive, hashClientFingerprint("fp-old"))
	server := NewServer("127.0.0.1:0", db)
	restoreToken := stubMobileTokenGenerator(t, "tok_new_anonymous")
	defer restoreToken()

	request := httptest.NewRequest(http.MethodPost, tokensRotatePath, strings.NewReader(`{"client_fingerprint":"fp-new"}`))
	request.Header.Set(authorizationHeader, "Bearer tok_old_anonymous")
	request.Header.Set(requestIDHeader, "req-token-rotate-anon")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeMobileTokenResponse(t, recorder)
	if body.Token != "tok_new_anonymous" {
		t.Fatalf("token = %q", body.Token)
	}
	if body.UserID != nil {
		t.Fatalf("user_id = %v", *body.UserID)
	}
	if body.TokenStatus != mobileTokenAnonymous {
		t.Fatalf("token_status = %q", body.TokenStatus)
	}
	assertMobileTokenState(t, db, "tok_old_anonymous", mobileTokenStateRotated)
	assertMobileTokenState(t, db, "tok_new_anonymous", mobileTokenStateActive)
}

func TestTokenRotateSuccessBound(t *testing.T) {
	const userID = "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362"

	db := newFakeMobileTokenDB()
	db.seedToken("tok_old_bound", userID, testMobileDeviceID, 9, mobileTokenStateActive, hashClientFingerprint("fp-old"))
	server := NewServer("127.0.0.1:0", db)
	restoreToken := stubMobileTokenGenerator(t, "tok_new_bound")
	defer restoreToken()

	request := httptest.NewRequest(http.MethodPost, tokensRotatePath, strings.NewReader(`{}`))
	request.Header.Set(authorizationHeader, "Bearer tok_old_bound")
	request.Header.Set(requestIDHeader, "req-token-rotate-bound")
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeMobileTokenResponse(t, recorder)
	if body.Token != "tok_new_bound" {
		t.Fatalf("token = %q", body.Token)
	}
	if body.UserID == nil || *body.UserID != userID {
		t.Fatalf("user_id = %v", body.UserID)
	}
	if body.TokenStatus != mobileTokenBound {
		t.Fatalf("token_status = %q", body.TokenStatus)
	}
	assertMobileTokenState(t, db, "tok_old_bound", mobileTokenStateRotated)
	assertMobileTokenState(t, db, "tok_new_bound", mobileTokenStateActive)
}

func TestTokenRotateInvalidatesOldToken(t *testing.T) {
	db := newFakeMobileTokenDB()
	db.seedToken("tok_rotate_once", "", testMobileDeviceID, 1, mobileTokenStateActive, "")
	server := NewServer("127.0.0.1:0", db)
	restoreToken := stubMobileTokenGenerator(t, "tok_rotate_twice_new")
	defer restoreToken()

	first := httptest.NewRequest(http.MethodPost, tokensRotatePath, strings.NewReader(`{}`))
	first.Header.Set(authorizationHeader, "Bearer tok_rotate_once")
	first.Header.Set(requestIDHeader, "req-token-first")
	firstRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", firstRecorder.Code, firstRecorder.Body.String())
	}

	second := httptest.NewRequest(http.MethodPost, tokensRotatePath, strings.NewReader(`{}`))
	second.Header.Set(authorizationHeader, "Bearer tok_rotate_once")
	second.Header.Set(requestIDHeader, "req-token-second")
	secondRecorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(secondRecorder, second)

	assertTokenErrorEnvelope(t, secondRecorder, http.StatusUnauthorized, unauthorizedMobileTokenCode, "req-token-second")
}

func decodeMobileTokenResponse(t *testing.T, recorder *httptest.ResponseRecorder) mobileTokenResponse {
	t.Helper()

	var body mobileTokenResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func assertMobileTokenState(t *testing.T, db *fakeMobileTokenDB, token, wantState string) {
	t.Helper()

	record := db.recordByToken(token)
	if record.Status != wantState {
		t.Fatalf("token=%s status=%q want=%q", token, record.Status, wantState)
	}
}

func stubMobileTokenGenerator(t *testing.T, tokens ...string) func() {
	t.Helper()

	original := generateMobileToken
	index := 0
	generateMobileToken = func() (string, error) {
		if index >= len(tokens) {
			return "", errors.New("no mobile token stub value")
		}
		value := tokens[index]
		index++
		return value, nil
	}
	return func() { generateMobileToken = original }
}

func stubInternalUUIDGenerator(t *testing.T, values ...string) func() {
	t.Helper()

	original := generateInternalUUID
	index := 0
	generateInternalUUID = func() (string, error) {
		if index >= len(values) {
			return "", errors.New("no uuid stub value")
		}
		value := values[index]
		index++
		return value, nil
	}
	return func() { generateInternalUUID = original }
}

type fakeMobileTokenRecord struct {
	UserID          string
	DeviceID        string
	WriterEpoch     int64
	Status          string
	FingerprintHash string
}

type fakeMobileTokenDB struct {
	tokens map[string]fakeMobileTokenRecord
}

func newFakeMobileTokenDB() *fakeMobileTokenDB {
	return &fakeMobileTokenDB{tokens: make(map[string]fakeMobileTokenRecord)}
}

func (f *fakeMobileTokenDB) Health(context.Context) error {
	return nil
}

func (f *fakeMobileTokenDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx := &fakeMobileTokenTx{tokens: cloneFakeMobileTokenRecords(f.tokens)}
	if err := fn(tx); err != nil {
		return err
	}
	f.tokens = tx.tokens
	return nil
}

func (f *fakeMobileTokenDB) seedToken(token, userID, deviceID string, writerEpoch int64, status, fingerprintHash string) {
	f.tokens[hashMobileToken(token)] = fakeMobileTokenRecord{
		UserID:          userID,
		DeviceID:        deviceID,
		WriterEpoch:     writerEpoch,
		Status:          status,
		FingerprintHash: fingerprintHash,
	}
}

func (f *fakeMobileTokenDB) recordByToken(token string) fakeMobileTokenRecord {
	record, ok := f.tokens[hashMobileToken(token)]
	if !ok {
		panic(fmt.Sprintf("token not found: %s", token))
	}
	return record
}

func cloneFakeMobileTokenRecords(input map[string]fakeMobileTokenRecord) map[string]fakeMobileTokenRecord {
	result := make(map[string]fakeMobileTokenRecord, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

type fakeMobileTokenTx struct {
	tokens map[string]fakeMobileTokenRecord
}

func (f *fakeMobileTokenTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeMobileTokenTx) Commit(context.Context) error {
	return nil
}

func (f *fakeMobileTokenTx) Rollback(context.Context) error {
	return nil
}

func (f *fakeMobileTokenTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (f *fakeMobileTokenTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (f *fakeMobileTokenTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (f *fakeMobileTokenTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (f *fakeMobileTokenTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "INSERT INTO mobile_tokens"):
		return pgconn.CommandTag{}, f.execInsertMobileToken(args)
	case strings.Contains(query, "UPDATE mobile_tokens"):
		return pgconn.CommandTag{}, f.execRotateMobileToken(args)
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unsupported exec query: %s", query)
	}
}

func (f *fakeMobileTokenTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeMobileTokenTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "FROM mobile_tokens") {
		return f.queryRowMobileToken(args)
	}
	return fakeMobileTokenRow{err: fmt.Errorf("unsupported queryrow: %s", query)}
}

func (f *fakeMobileTokenTx) Conn() *pgx.Conn {
	return nil
}

func (f *fakeMobileTokenTx) execInsertMobileToken(args []any) error {
	if len(args) != 5 {
		return fmt.Errorf("insert args = %d", len(args))
	}
	tokenHash, _ := args[0].(string)
	userID, _ := args[1].(string)
	deviceID, _ := args[2].(string)
	writerEpoch, _ := args[3].(int64)
	fingerprintHash, _ := args[4].(string)

	if _, exists := f.tokens[tokenHash]; exists {
		return &pgconn.PgError{Code: "23505", ConstraintName: "uq_mobile_tokens_token_hash"}
	}
	if userID != "" && f.hasActiveUserToken(userID) {
		return &pgconn.PgError{Code: "23505", ConstraintName: "uq_mobile_tokens_active_user"}
	}

	f.tokens[tokenHash] = fakeMobileTokenRecord{
		UserID:          userID,
		DeviceID:        deviceID,
		WriterEpoch:     writerEpoch,
		Status:          mobileTokenStateActive,
		FingerprintHash: fingerprintHash,
	}
	return nil
}

func (f *fakeMobileTokenTx) hasActiveUserToken(userID string) bool {
	for _, record := range f.tokens {
		if record.UserID == userID && record.Status == mobileTokenStateActive {
			return true
		}
	}
	return false
}

func (f *fakeMobileTokenTx) execRotateMobileToken(args []any) error {
	if len(args) != 1 {
		return fmt.Errorf("rotate args = %d", len(args))
	}
	tokenHash, _ := args[0].(string)
	record, ok := f.tokens[tokenHash]
	if !ok {
		return nil
	}
	record.Status = mobileTokenStateRotated
	f.tokens[tokenHash] = record
	return nil
}

func (f *fakeMobileTokenTx) queryRowMobileToken(args []any) pgx.Row {
	if len(args) != 1 {
		return fakeMobileTokenRow{err: fmt.Errorf("select args = %d", len(args))}
	}
	tokenHash, _ := args[0].(string)
	record, ok := f.tokens[tokenHash]
	if !ok {
		return fakeMobileTokenRow{err: pgx.ErrNoRows}
	}
	return fakeMobileTokenRow{values: []any{
		record.UserID,
		record.DeviceID,
		record.WriterEpoch,
		record.Status,
		record.FingerprintHash,
	}}
}

type fakeMobileTokenRow struct {
	values []any
	err    error
}

func (f fakeMobileTokenRow) Scan(dest ...any) error {
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

func assertTokenErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode, wantRequestID string) {
	t.Helper()

	if recorder.Code != wantStatus {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.ErrorCode != wantCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.RequestID != wantRequestID {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
}
