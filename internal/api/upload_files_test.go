package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	apperrors "noovertime/internal/errors"
	"noovertime/internal/storage"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPunchPhotoUploadStoresObjectAndReturnsMetadata(t *testing.T) {
	auth := mobileAuthContext{
		Token:       "tok_test",
		UserID:      "11111111-1111-1111-1111-111111111111",
		DeviceID:    "22222222-2222-2222-2222-222222222222",
		WriterEpoch: 1,
		TokenStatus: mobileTokenBound,
	}
	db := &fakeUploadDB{
		auth: auth,
		tx:   &fakeUploadTx{},
	}
	store := &fakeObjectStore{}
	server := NewServer("127.0.0.1:0", db, WithObjectStore(store))
	server.now = func() time.Time { return time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC) }

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("punch_record_id", "33333333-3333-3333-3333-333333333333")
	_ = writer.WriteField("local_date", "2026-03-15")
	_ = writer.WriteField("punch_type", "START")
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="file"; filename="start.jpg"`},
		"Content-Type":        []string{"image/jpeg"},
	})
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write([]byte("jpeg-bytes")); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, punchPhotoUploadPath, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(requestIDHeader, "req-upload-photo")
	setSyncAuthHeader(req, auth.Token)
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.puts) != 1 {
		t.Fatalf("put count = %d", len(store.puts))
	}
	if store.puts[0].ContentType != "image/jpeg" {
		t.Fatalf("content type = %q", store.puts[0].ContentType)
	}
	if string(store.putBodies[0]) != "jpeg-bytes" {
		t.Fatalf("stored body = %q", string(store.putBodies[0]))
	}
	if len(db.tx.execCalls) == 0 || !strings.Contains(db.tx.execCalls[0].query, "INSERT INTO punch_photo_uploads") {
		t.Fatalf("first exec query = %q", db.tx.firstQuery())
	}

	var payload uploadFileResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.RequestID != "req-upload-photo" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
	if payload.ObjectKey != "punch-photos/11111111-1111-1111-1111-111111111111/2026-03-15/33333333-3333-3333-3333-333333333333.jpg" {
		t.Fatalf("object_key = %q", payload.ObjectKey)
	}
	if payload.URL != "https://uploads.example.com/"+payload.ObjectKey {
		t.Fatalf("url = %q", payload.URL)
	}
	if payload.ExpiresAt != "2026-05-14T10:00:00Z" {
		t.Fatalf("expires_at = %q", payload.ExpiresAt)
	}
}

func TestLogUploadRejectsNonTextFile(t *testing.T) {
	auth := mobileAuthContext{
		Token:       "tok_test",
		UserID:      "11111111-1111-1111-1111-111111111111",
		DeviceID:    "22222222-2222-2222-2222-222222222222",
		WriterEpoch: 1,
		TokenStatus: mobileTokenBound,
	}
	server := NewServer("127.0.0.1:0", &fakeUploadDB{
		auth: auth,
		tx:   &fakeUploadTx{},
	}, WithObjectStore(&fakeObjectStore{}))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("log_date", "2026-03-15")
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="file"; filename="bad.png"`},
		"Content-Type":        []string{"image/png"},
	})
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write([]byte("not-a-log")); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, logUploadPath, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	setSyncAuthHeader(req, auth.Token)
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != invalidArgumentCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
}

func TestPunchPhotoUploadDeletesPreviousObjectWhenKeyChanges(t *testing.T) {
	auth := mobileAuthContext{
		Token:       "tok_test",
		UserID:      "11111111-1111-1111-1111-111111111111",
		DeviceID:    "22222222-2222-2222-2222-222222222222",
		WriterEpoch: 1,
		TokenStatus: mobileTokenBound,
	}
	oldObjectKey := "punch-photos/11111111-1111-1111-1111-111111111111/2026-03-15/33333333-3333-3333-3333-333333333333.png"
	db := &fakeUploadDB{
		auth: auth,
		tx: &fakeUploadTx{
			queryResults: [][][]any{
				{{oldObjectKey}},
				{},
			},
		},
	}
	store := &fakeObjectStore{}
	server := NewServer("127.0.0.1:0", db, WithObjectStore(store))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("punch_record_id", "33333333-3333-3333-3333-333333333333")
	_ = writer.WriteField("local_date", "2026-03-15")
	_ = writer.WriteField("punch_type", "START")
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="file"; filename="start.jpg"`},
		"Content-Type":        []string{"image/jpeg"},
	})
	if err != nil {
		t.Fatalf("CreatePart() error = %v", err)
	}
	if _, err := part.Write([]byte("jpeg-bytes")); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, punchPhotoUploadPath, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	setSyncAuthHeader(req, auth.Token)
	recorder := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.deleteCalls) != 1 || store.deleteCalls[0] != oldObjectKey {
		t.Fatalf("deleteCalls = %#v", store.deleteCalls)
	}
}

type fakeUploadDB struct {
	auth mobileAuthContext
	tx   *fakeUploadTx
}

func (f *fakeUploadDB) Health(context.Context) error { return nil }

func (f *fakeUploadDB) resolveMobileAuthContextDirect(header mobileTokenHeader) (mobileAuthContext, error) {
	if header.Token != f.auth.Token {
		return mobileAuthContext{}, unauthorizedMobileToken()
	}
	return f.auth, nil
}

func (f *fakeUploadDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return fn(f.tx)
}

type fakeUploadTx struct {
	execCalls    []fakeExecCall
	queryRows    [][]any
	queryResults [][][]any
}

type fakeExecCall struct {
	query string
	args  []any
}

func (f *fakeUploadTx) firstQuery() string {
	if len(f.execCalls) == 0 {
		return ""
	}
	return f.execCalls[0].query
}

func (f *fakeUploadTx) Begin(context.Context) (pgx.Tx, error) { return nil, nil }
func (f *fakeUploadTx) Commit(context.Context) error          { return nil }
func (f *fakeUploadTx) Rollback(context.Context) error        { return nil }
func (f *fakeUploadTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeUploadTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeUploadTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeUploadTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeUploadTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	f.execCalls = append(f.execCalls, fakeExecCall{query: query, args: args})
	return pgconn.CommandTag{}, nil
}
func (f *fakeUploadTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if len(f.queryResults) > 0 {
		rows := f.queryResults[0]
		f.queryResults = f.queryResults[1:]
		return newFakeRows(rows), nil
	}
	return newFakeRows(f.queryRows), nil
}
func (f *fakeUploadTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (f *fakeUploadTx) Conn() *pgx.Conn                                  { return nil }

type fakeObjectStore struct {
	puts        []storage.PutRequest
	putBodies   [][]byte
	deleteCalls []string
}

func (f *fakeObjectStore) Put(_ context.Context, req storage.PutRequest) (storage.PutResult, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return storage.PutResult{}, err
	}
	f.puts = append(f.puts, storage.PutRequest{
		Key:         req.Key,
		ContentType: req.ContentType,
	})
	f.putBodies = append(f.putBodies, body)
	return storage.PutResult{
		Key: req.Key,
		URL: "https://uploads.example.com/" + req.Key,
	}, nil
}

func (f *fakeObjectStore) Delete(_ context.Context, key string) error {
	f.deleteCalls = append(f.deleteCalls, key)
	return nil
}
