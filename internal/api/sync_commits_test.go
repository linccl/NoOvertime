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

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const validSyncCommitPayload = `{
  "user_id": "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362",
  "device_id": "0b854f80-0213-4cb1-b5d0-95af02f137f3",
  "writer_epoch": 12,
  "sync_id": "bb5166cb-13ed-47a0-9fb5-58e2062a3559",
  "payload_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "punch_records": [
    {
      "id": "4acb45c8-65cb-4e20-9602-2ac3609d5c28",
      "local_date": "2026-02-12",
      "type": "START",
      "at_utc": "2026-02-12T01:10:00Z",
      "timezone_id": "Asia/Shanghai",
      "minute_of_day": 550,
      "source": "MANUAL",
      "deleted_at": null,
      "version": 3
    }
  ],
  "leave_records": [
    {
      "id": "1fc35956-0015-4aa7-a0aa-3ef6576fc423",
      "local_date": "2026-02-11",
      "leave_type": "AM",
      "deleted_at": null,
      "version": 2
    }
  ],
  "day_summaries": [
    {
      "id": "3cf42a4f-8107-49dd-96bd-1cd7ea6f3f54",
      "local_date": "2026-02-12",
      "start_at_utc": "2026-02-12T01:10:00Z",
      "end_at_utc": "2026-02-12T10:20:00Z",
      "is_leave_day": false,
      "leave_type": null,
      "is_late": true,
      "work_minutes": 550,
      "adjust_minutes": 0,
      "status": "COMPUTED",
      "version": 5,
      "updated_at": "2026-02-12T10:21:00Z"
    }
  ],
  "month_summaries": [
    {
      "id": "445f1f36-cf1c-4f90-9fd0-b56438e2df2e",
      "month_start": "2026-02-01",
      "work_minutes_total": 6120,
      "adjust_minutes_balance": 120,
      "version": 5,
      "updated_at": "2026-02-12T10:21:00Z"
    }
  ]
}`

const (
	testUserID      = "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362"
	testDeviceID    = "0b854f80-0213-4cb1-b5d0-95af02f137f3"
	testWriterEpoch = int64(12)
)

func TestGeneratePayloadHashStableAcrossArrayOrder(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	second := request.PunchRecords[0]
	second.ID = "5acb45c8-65cb-4e20-9602-2ac3609d5c29"
	second.Version = 4
	second.MinuteOfDay = 560
	second.AtUTC = "2026-02-12T01:20:00Z"

	request.PunchRecords = append(request.PunchRecords, second)
	request.PunchRecords[0], request.PunchRecords[1] = request.PunchRecords[1], request.PunchRecords[0]

	leftInput := mustConvertRequest(t, request)
	leftHash, err := generatePayloadHash(leftInput)
	if err != nil {
		t.Fatalf("generatePayloadHash(left) error = %v", err)
	}

	request.PunchRecords[0], request.PunchRecords[1] = request.PunchRecords[1], request.PunchRecords[0]
	rightInput := mustConvertRequest(t, request)
	rightHash, err := generatePayloadHash(rightInput)
	if err != nil {
		t.Fatalf("generatePayloadHash(right) error = %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("hash mismatch left=%s right=%s", leftHash, rightHash)
	}
}

func TestParseSyncCommitInputSuccess(t *testing.T) {
	payload := mustBuildPayloadWithComputedHash(t, nil)

	input, err := parseSyncCommitInput(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("parseSyncCommitInput() error = %v", err)
	}

	if input.UserID != "8d3c4d78-6c2b-4b56-a430-1e6b97f5b362" {
		t.Fatalf("user_id = %q", input.UserID)
	}
	if input.WriterEpoch != 12 {
		t.Fatalf("writer_epoch = %d", input.WriterEpoch)
	}
	if input.ComputedPayloadHash == "" {
		t.Fatal("computed payload hash is empty")
	}
	if input.PayloadHash != input.ComputedPayloadHash {
		t.Fatalf("payload hashes mismatch %s %s", input.PayloadHash, input.ComputedPayloadHash)
	}
}

func TestParseSyncCommitInputUnknownField(t *testing.T) {
	payload := mustBuildPayloadWithComputedHash(t, nil)
	payload = strings.Replace(payload, `{"user_id":`, `{"unknown_field":"x","user_id":`, 1)

	_, err := parseSyncCommitInput(strings.NewReader(payload))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != unknownFieldCode {
		t.Fatalf("error_code = %q", apiErr.Code)
	}
}

func TestParseSyncCommitInputInvalidArgument(t *testing.T) {
	payload := mustBuildPayloadWithComputedHash(t, nil)
	payload = strings.Replace(payload, `"writer_epoch":12`, `"writer_epoch":0`, 1)

	_, err := parseSyncCommitInput(strings.NewReader(payload))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != invalidArgumentCode {
		t.Fatalf("error_code = %q", apiErr.Code)
	}
}

func TestParseSyncCommitInputPayloadHashMismatch(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	input := mustConvertRequest(t, request)
	hash, err := generatePayloadHash(input)
	if err != nil {
		t.Fatalf("generatePayloadHash() error = %v", err)
	}

	request.PayloadHash = strings.Repeat("b", 64)
	if request.PayloadHash == hash {
		request.PayloadHash = strings.Repeat("c", 64)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	_, err = parseSyncCommitInput(strings.NewReader(string(raw)))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var apiErr apperrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != invalidArgumentCode {
		t.Fatalf("error_code = %q", apiErr.Code)
	}
}

func TestSyncCommitsRouteAppliedAndReplayNoop(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	now := time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC)
	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return now }
	requestPayload := mustBuildPayloadWithComputedHash(t, nil)

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(requestPayload))
	firstReq.Header.Set(requestIDHeader, "req-sync-apply")
	server.httpServer.Handler.ServeHTTP(first, firstReq)

	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}

	var firstBody syncCommitResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstBody.GateResult != gateResultApplied || firstBody.GateReason != gateReasonAppliedWrite {
		t.Fatalf("first gate = %s/%s", firstBody.GateResult, firstBody.GateReason)
	}
	firstCreatedAt := firstBody.SyncCommit.CreatedAt
	if db.syncCommitCount() != 1 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
	countSnapshot := db.snapshotCounts()
	versionSnapshot := db.snapshotBusinessVersions()

	now = now.Add(2 * time.Hour)
	restart := NewServer("127.0.0.1:0", db)
	restart.now = func() time.Time { return now }

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(requestPayload))
	secondReq.Header.Set(requestIDHeader, "req-sync-replay")
	restart.httpServer.Handler.ServeHTTP(second, secondReq)

	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	var secondBody syncCommitResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if secondBody.GateResult != gateResultNoop || secondBody.GateReason != gateReasonReplayNoop {
		t.Fatalf("second gate = %s/%s", secondBody.GateResult, secondBody.GateReason)
	}
	if secondBody.SyncCommit.CreatedAt != firstCreatedAt {
		t.Fatalf("created_at mismatch first=%q second=%q", firstCreatedAt, secondBody.SyncCommit.CreatedAt)
	}
	if db.syncCommitCount() != 1 {
		t.Fatalf("sync_commits count after replay = %d", db.syncCommitCount())
	}
	if diff := countSnapshot.diff(db.snapshotCounts()); diff != "" {
		t.Fatalf("replay must not write any table, got diff: %s", diff)
	}
	if diff := versionSnapshot.diff(db.snapshotBusinessVersions()); diff != "" {
		t.Fatalf("replay must not write business tables, got diff: %s", diff)
	}
}

func TestSyncCommitsRouteConflictOnSameSyncIDDifferentHash(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }
	firstPayload := mustBuildPayloadWithComputedHash(t, nil)
	secondPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.PunchRecords[0].MinuteOfDay = 551
		req.PunchRecords[0].AtUTC = "2026-02-12T01:11:00Z"
	})

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(firstPayload))
	firstReq.Header.Set(requestIDHeader, "req-sync-first")
	server.httpServer.Handler.ServeHTTP(first, firstReq)

	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	snapshot := db.snapshotCounts()

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(secondPayload))
	secondReq.Header.Set(requestIDHeader, "req-sync-conflict")
	server.httpServer.Handler.ServeHTTP(second, secondReq)

	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	var conflict syncCommitConflictResponse
	if err := json.Unmarshal(second.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if conflict.ErrorCode != syncIDConflictCode {
		t.Fatalf("error_code = %q", conflict.ErrorCode)
	}
	if conflict.GateResult != gateResultRejected || conflict.GateReason != gateReasonSyncIDConflict {
		t.Fatalf("gate = %s/%s", conflict.GateResult, conflict.GateReason)
	}
	if conflict.RequestID != "req-sync-conflict" {
		t.Fatalf("request_id = %q", conflict.RequestID)
	}
	if diff := snapshot.diff(db.snapshotCounts()); diff != "" {
		t.Fatalf("conflict must not write any table, got diff: %s", diff)
	}
}

func TestSyncCommitsRouteStaleWriterRejectedByDeviceID(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	secondPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "ab5166cb-13ed-47a0-9fb5-58e2062a3558"
		req.DeviceID = "9b854f80-0213-4cb1-b5d0-95af02f137f9"
		req.PunchRecords[0].Version = 6
		req.DaySummaries[0].Version = 6
		req.MonthSummaries[0].Version = 6
		req.MonthSummaries[0].WorkMinutesTotal = 6150
	})

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(secondPayload))
	secondReq.Header.Set(requestIDHeader, "req-stale-device")
	server.httpServer.Handler.ServeHTTP(second, secondReq)

	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != staleWriterRejectedCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.RequestID != "req-stale-device" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func TestSyncCommitsRouteStaleWriterRejectedByEpoch(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	secondPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "db5166cb-13ed-47a0-9fb5-58e2062a3556"
		req.WriterEpoch = 13
		req.PunchRecords[0].Version = 7
		req.DaySummaries[0].Version = 7
		req.MonthSummaries[0].Version = 7
		req.MonthSummaries[0].WorkMinutesTotal = 6180
	})

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(secondPayload))
	secondReq.Header.Set(requestIDHeader, "req-stale-epoch")
	server.httpServer.Handler.ServeHTTP(second, secondReq)

	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != staleWriterRejectedCode {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if payload.RequestID != "req-stale-epoch" {
		t.Fatalf("request_id = %q", payload.RequestID)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func TestSyncCommitsRouteVersionGateLowOrEqualNoopAndHighVersionApplied(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	now := time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC)
	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return now }

	firstPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "eb5166cb-13ed-47a0-9fb5-58e2062a3555"
	})
	lowPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "fb5166cb-13ed-47a0-9fb5-58e2062a3554"
	})
	highPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "ab6166cb-13ed-47a0-9fb5-58e2062a3553"
		req.PunchRecords[0].Version = 6
		req.DaySummaries[0].Version = 6
		req.MonthSummaries[0].Version = 6
		req.MonthSummaries[0].WorkMinutesTotal = 6200
	})

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(firstPayload))
	server.httpServer.Handler.ServeHTTP(first, firstReq)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	if db.syncCommitCount() != 1 {
		t.Fatalf("sync_commits count after first = %d", db.syncCommitCount())
	}
	businessSnapshot := db.snapshotBusinessVersions()

	now = now.Add(15 * time.Minute)
	low := httptest.NewRecorder()
	lowReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(lowPayload))
	server.httpServer.Handler.ServeHTTP(low, lowReq)
	if low.Code != http.StatusOK {
		t.Fatalf("low status = %d body=%s", low.Code, low.Body.String())
	}
	var lowBody syncCommitResponse
	if err := json.Unmarshal(low.Body.Bytes(), &lowBody); err != nil {
		t.Fatalf("decode low response: %v", err)
	}
	if lowBody.GateResult != gateResultNoop || lowBody.GateReason != gateReasonLowOrEqual {
		t.Fatalf("low gate = %s/%s", lowBody.GateResult, lowBody.GateReason)
	}
	if db.syncCommitCount() != 2 {
		t.Fatalf("sync_commits count after low/noop = %d", db.syncCommitCount())
	}
	if diff := businessSnapshot.diff(db.snapshotBusinessVersions()); diff != "" {
		t.Fatalf("LOW_OR_EQUAL_VERSION must not write business tables, got diff: %s", diff)
	}

	now = now.Add(15 * time.Minute)
	high := httptest.NewRecorder()
	highReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(highPayload))
	server.httpServer.Handler.ServeHTTP(high, highReq)
	if high.Code != http.StatusOK {
		t.Fatalf("high status = %d body=%s", high.Code, high.Body.String())
	}
	var highBody syncCommitResponse
	if err := json.Unmarshal(high.Body.Bytes(), &highBody); err != nil {
		t.Fatalf("decode high response: %v", err)
	}
	if highBody.GateResult != gateResultApplied || highBody.GateReason != gateReasonAppliedWrite {
		t.Fatalf("high gate = %s/%s", highBody.GateResult, highBody.GateReason)
	}
}

func TestSyncCommitsRouteRejectsPunchEndRequiresStart(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	badPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "0f6166cb-13ed-47a0-9fb5-58e2062a3560"
		req.PunchRecords[0].Type = "END"
	})

	bad := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(badPayload))
	badReq.Header.Set(requestIDHeader, "req-rule-end-requires-start")
	server.httpServer.Handler.ServeHTTP(bad, badReq)

	if bad.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", bad.Code, bad.Body.String())
	}

	var payload apperrors.ErrorResponse
	if err := json.Unmarshal(bad.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ErrorCode != punchEndRequiresStart {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}

	goodPayload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "0f6166cb-13ed-47a0-9fb5-58e2062a3560"
		req.PunchRecords[0].Type = "START"
	})

	good := httptest.NewRecorder()
	goodReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(goodPayload))
	server.httpServer.Handler.ServeHTTP(good, goodReq)

	if good.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", good.Code, good.Body.String())
	}
}

func TestSyncCommitsRouteRejectsPunchEndNotAfterStart(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	payload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "1f6166cb-13ed-47a0-9fb5-58e2062a3561"
		req.PunchRecords = append(req.PunchRecords, punchRecordJSON{
			ID:          "6acb45c8-65cb-4e20-9602-2ac3609d5c30",
			LocalDate:   req.PunchRecords[0].LocalDate,
			Type:        "END",
			AtUTC:       req.PunchRecords[0].AtUTC,
			TimezoneID:  req.PunchRecords[0].TimezoneID,
			MinuteOfDay: req.PunchRecords[0].MinuteOfDay,
			Source:      "MANUAL",
			DeletedAt:   nil,
			Version:     req.PunchRecords[0].Version + 1,
		})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != punchEndNotAfterStart {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func TestSyncCommitsRouteRejectsFullDayLeaveWithAutoPunch(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	payload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "2f6166cb-13ed-47a0-9fb5-58e2062a3562"
		req.PunchRecords[0].Source = "AUTO"
		req.LeaveRecords[0].LocalDate = req.PunchRecords[0].LocalDate
		req.LeaveRecords[0].LeaveType = "FULL_DAY"
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != autoPunchLeaveConflict {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func TestSyncCommitsRouteRejectsTimePrecisionInvalid(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	payload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "3f6166cb-13ed-47a0-9fb5-58e2062a3563"
		req.PunchRecords[0].AtUTC = "2026-02-12T01:10:30Z"
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != timePrecisionInvalid {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func TestSyncCommitsRouteRejectsTimeFieldsMismatch(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	server.now = func() time.Time { return time.Date(2026, 2, 13, 3, 0, 0, 0, time.UTC) }

	payload := mustBuildPayloadWithComputedHash(t, func(req *syncCommitRequest) {
		req.SyncID = "4f6166cb-13ed-47a0-9fb5-58e2062a3564"
		req.PunchRecords[0].MinuteOfDay = req.PunchRecords[0].MinuteOfDay + 1
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != timeFieldsMismatch {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if db.syncCommitCount() != 0 {
		t.Fatalf("sync_commits count = %d", db.syncCommitCount())
	}
}

func mustBuildPayloadWithComputedHash(t *testing.T, mutate func(*syncCommitRequest)) string {
	t.Helper()

	request := mustBuildSyncCommitRequest(t, mutate)
	input := mustConvertRequest(t, request)
	hash, err := generatePayloadHash(input)
	if err != nil {
		t.Fatalf("generatePayloadHash() error = %v", err)
	}
	request.PayloadHash = hash

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(raw)
}

func mustBuildSyncCommitRequest(t *testing.T, mutate func(*syncCommitRequest)) syncCommitRequest {
	t.Helper()

	var request syncCommitRequest
	if err := json.Unmarshal([]byte(validSyncCommitPayload), &request); err != nil {
		t.Fatalf("unmarshal valid payload: %v", err)
	}
	if mutate != nil {
		mutate(&request)
	}
	return request
}

func mustConvertRequest(t *testing.T, request syncCommitRequest) SyncCommitInput {
	t.Helper()
	if request.PayloadHash == "" {
		request.PayloadHash = strings.Repeat("a", 64)
	}

	input, err := convertSyncCommitRequest(request)
	if err != nil {
		t.Fatalf("convertSyncCommitRequest() error = %v", err)
	}
	return input
}

func TestSyncCommitResultCreatedAtRFC3339(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.setWriter(testUserID, testDeviceID, testWriterEpoch)

	server := NewServer("127.0.0.1:0", db)
	requestPayload := mustBuildPayloadWithComputedHash(t, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(requestPayload))
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}

	var body syncCommitResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, body.SyncCommit.CreatedAt); err != nil {
		t.Fatalf("created_at format = %q", body.SyncCommit.CreatedAt)
	}
}

func TestPersistSyncCommitTransactionAppliedWritesAllTablesInSingleTx(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	input := mustConvertRequest(t, request)
	decision := syncCommitGateDecision{
		Result: gateResultApplied,
		Reason: gateReasonAppliedWrite,
		Record: syncCommitGateRecord{CreatedAt: time.Date(2026, 2, 13, 3, 15, 0, 0, time.UTC)},
	}

	db := newFakeSyncCommitTxDB()
	err := persistSyncCommitTransaction(context.Background(), db, input, decision)
	if err != nil {
		t.Fatalf("persistSyncCommitTransaction() error = %v", err)
	}

	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.commits != 1 || db.rollbacks != 0 {
		t.Fatalf("commits/rollbacks = %d/%d", db.commits, db.rollbacks)
	}

	expectedExecs := len(input.PunchRecords) + len(input.LeaveRecords) + len(input.DaySummaries) + len(input.MonthSummaries) + 1
	if db.lastTx == nil || db.lastTx.execCalls != expectedExecs {
		t.Fatalf("exec calls = %d, expected %d", db.lastTx.execCalls, expectedExecs)
	}

	for _, table := range []string{"punch_records", "leave_records", "day_summaries", "month_summaries", "sync_commits"} {
		if db.committedTableCounts[table] == 0 {
			t.Fatalf("table %s not written", table)
		}
	}

	if len(db.lastTx.txIDs) != expectedExecs {
		t.Fatalf("tx id count = %d, expected %d", len(db.lastTx.txIDs), expectedExecs)
	}
	for _, id := range db.lastTx.txIDs {
		if id != db.lastTx.id {
			t.Fatalf("exec used multiple tx ids: %v", db.lastTx.txIDs)
		}
	}
}

func TestPersistSyncCommitTransactionRollbackOnSubWriteFailure(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	input := mustConvertRequest(t, request)
	decision := syncCommitGateDecision{
		Result: gateResultApplied,
		Reason: gateReasonAppliedWrite,
		Record: syncCommitGateRecord{CreatedAt: time.Date(2026, 2, 13, 3, 15, 0, 0, time.UTC)},
	}

	db := newFakeSyncCommitTxDB()
	db.failTable = "leave_records"

	err := persistSyncCommitTransaction(context.Background(), db, input, decision)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.commits != 0 || db.rollbacks != 1 {
		t.Fatalf("commits/rollbacks = %d/%d", db.commits, db.rollbacks)
	}
	if len(db.committedTableCounts) != 0 {
		t.Fatalf("committedTableCounts should be empty, got %v", db.committedTableCounts)
	}
}

func TestPersistSyncCommitTransactionReplayNoopSkipsTransaction(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	input := mustConvertRequest(t, request)
	decision := syncCommitGateDecision{
		Result: gateResultNoop,
		Reason: gateReasonReplayNoop,
		Record: syncCommitGateRecord{CreatedAt: time.Date(2026, 2, 13, 3, 15, 0, 0, time.UTC)},
	}

	db := newFakeSyncCommitTxDB()
	if err := persistSyncCommitTransaction(context.Background(), db, input, decision); err != nil {
		t.Fatalf("persistSyncCommitTransaction() error = %v", err)
	}
	if db.withTxCalls != 0 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
}

func TestSyncCommitsRouteTransactionFailureRollsBackGateState(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.failTable = "sync_commits"

	server := NewServer("127.0.0.1:0", db)
	payload := mustBuildPayloadWithComputedHash(t, nil)

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(first, firstReq)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	if db.commits != 0 || db.rollbacks != 1 {
		t.Fatalf("commits/rollbacks = %d/%d", db.commits, db.rollbacks)
	}
	if len(db.committedTableCounts) != 0 {
		t.Fatalf("committed table counts should be empty, got %v", db.committedTableCounts)
	}

	db.failTable = ""

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	server.httpServer.Handler.ServeHTTP(second, secondReq)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}

	var body syncCommitResponse
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.GateResult != gateResultApplied || body.GateReason != gateReasonAppliedWrite {
		t.Fatalf("second gate = %s/%s", body.GateResult, body.GateReason)
	}
}

func TestMapSyncCommitPersistenceErrorRequiredMappings(t *testing.T) {
	tests := []struct {
		name           string
		pgErr          *pgconn.PgError
		wantCode       string
		wantConflict   bool
		wantGateResult string
		wantGateReason string
	}{
		{
			name:     "error_key PUNCH_END_REQUIRES_START",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=PUNCH_END_REQUIRES_START] x"},
			wantCode: punchEndRequiresStart,
		},
		{
			name:     "error_key PUNCH_END_NOT_AFTER_START",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=PUNCH_END_NOT_AFTER_START] x"},
			wantCode: punchEndNotAfterStart,
		},
		{
			name:     "error_key AUTO_PUNCH_ON_FULL_DAY_LEAVE",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=AUTO_PUNCH_ON_FULL_DAY_LEAVE] x"},
			wantCode: autoPunchLeaveConflict,
		},
		{
			name:     "error_key FULL_DAY_LEAVE_WITH_AUTO_PUNCH",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=FULL_DAY_LEAVE_WITH_AUTO_PUNCH] x"},
			wantCode: autoPunchLeaveConflict,
		},
		{
			name:     "error_key SYNC_COMMIT_STALE_WRITER",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=SYNC_COMMIT_STALE_WRITER] x"},
			wantCode: staleWriterRejectedCode,
		},
		{
			name:     "error_key MIGRATION_SOURCE_MISMATCH",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=MIGRATION_SOURCE_MISMATCH] x"},
			wantCode: migrationSourceMismatchCode,
		},
		{
			name:     "error_key MIGRATION_TRANSITION_INVALID maps to MIGRATION_STATE_INVALID",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=MIGRATION_TRANSITION_INVALID] x"},
			wantCode: migrationStateInvalidCode,
		},
		{
			name:     "error_key MIGRATION_IMMUTABLE_FIELDS",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=MIGRATION_IMMUTABLE_FIELDS] x"},
			wantCode: migrationImmutableFieldsCode,
		},
		{
			name:     "error_key MIGRATION_USER_NOT_FOUND maps to USER_NOT_FOUND",
			pgErr:    &pgconn.PgError{Code: "P0001", Message: "[error_key=MIGRATION_USER_NOT_FOUND] x"},
			wantCode: userNotFoundCode,
		},
		{
			name:     "constraint minute precision",
			pgErr:    &pgconn.PgError{Code: "23514", ConstraintName: "ck_punch_records_at_utc_minute_precision"},
			wantCode: timePrecisionInvalid,
		},
		{
			name:     "constraint local date mismatch",
			pgErr:    &pgconn.PgError{Code: "23514", ConstraintName: "ck_punch_records_local_date_match_timezone"},
			wantCode: timeFieldsMismatch,
		},
		{
			name:           "unique sync id conflict",
			pgErr:          &pgconn.PgError{Code: "23505", ConstraintName: "uq_sync_commits_user_sync"},
			wantCode:       syncIDConflictCode,
			wantConflict:   true,
			wantGateResult: gateResultRejected,
			wantGateReason: gateReasonSyncIDConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mappedErr, mappedConflict, ok := mapSyncCommitPersistenceError(tc.pgErr, "req-map-001")
			if !ok {
				t.Fatal("expected mapping success")
			}

			if tc.wantConflict {
				if mappedConflict == nil {
					t.Fatal("expected conflict response mapping")
				}
				if mappedConflict.ErrorCode != tc.wantCode {
					t.Fatalf("conflict error_code = %q", mappedConflict.ErrorCode)
				}
				if mappedConflict.GateResult != tc.wantGateResult || mappedConflict.GateReason != tc.wantGateReason {
					t.Fatalf("conflict gate = %s/%s", mappedConflict.GateResult, mappedConflict.GateReason)
				}
				if mappedConflict.RequestID != "req-map-001" {
					t.Fatalf("request_id = %q", mappedConflict.RequestID)
				}
				return
			}

			if mappedErr == nil {
				t.Fatal("expected api error mapping")
			}
			var apiErr apperrors.APIError
			if !errors.As(mappedErr, &apiErr) {
				t.Fatalf("expected APIError, got %T", mappedErr)
			}
			if apiErr.Code != tc.wantCode {
				t.Fatalf("api error_code = %q", apiErr.Code)
			}
			if apiErr.StatusCode() != http.StatusConflict {
				t.Fatalf("status = %d", apiErr.StatusCode())
			}
		})
	}
}

func TestMapSyncCommitPersistenceErrorUnknownRuleKeyReturnsNoMapping(t *testing.T) {
	mappedErr, mappedConflict, ok := mapSyncCommitPersistenceError(
		&pgconn.PgError{Code: "P0001", Message: "[error_key=UNKNOWN_RULE_KEY] x"},
		"req-map-unknown",
	)
	if ok {
		t.Fatalf("unexpected mapping err=%v conflict=%v", mappedErr, mappedConflict)
	}
	if mappedErr != nil || mappedConflict != nil {
		t.Fatalf("expected nil mapped results, got err=%v conflict=%v", mappedErr, mappedConflict)
	}
}

func TestMapSyncCommitPersistenceErrorUnknownSQLStateReturnsNoMapping(t *testing.T) {
	mappedErr, mappedConflict, ok := mapSyncCommitPersistenceError(
		&pgconn.PgError{Code: "99999", Message: "unknown db error"},
		"req-map-unknown-sqlstate",
	)
	if ok {
		t.Fatalf("unexpected mapping err=%v conflict=%v", mappedErr, mappedConflict)
	}
	if mappedErr != nil || mappedConflict != nil {
		t.Fatalf("expected nil mapped results, got err=%v conflict=%v", mappedErr, mappedConflict)
	}
}

func TestSyncCommitsRouteMapsDBUniqueConflictToRejectedGate(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.failTable = "sync_commits"
	db.failErr = &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "uq_sync_commits_user_sync",
		Message:        "duplicate key value violates unique constraint",
	}

	server := NewServer("127.0.0.1:0", db)
	payload := mustBuildPayloadWithComputedHash(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	req.Header.Set(requestIDHeader, "req-db-conflict")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body syncCommitConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != syncIDConflictCode {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if body.GateResult != gateResultRejected || body.GateReason != gateReasonSyncIDConflict {
		t.Fatalf("gate = %s/%s", body.GateResult, body.GateReason)
	}
	if body.RequestID != "req-db-conflict" {
		t.Fatalf("request_id = %q", body.RequestID)
	}
}

func TestSyncCommitsRouteMapsDBErrorKeyToAPIErrorWithRequestID(t *testing.T) {
	db := newFakeSyncCommitTxDB()
	db.failTable = "sync_commits"
	db.failErr = &pgconn.PgError{
		Code:    "P0001",
		Message: "[error_key=SYNC_COMMIT_STALE_WRITER] stale writer",
	}

	server := NewServer("127.0.0.1:0", db)
	payload := mustBuildPayloadWithComputedHash(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, syncCommitsPath, strings.NewReader(payload))
	req.Header.Set(requestIDHeader, "req-db-error-key")
	server.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var body apperrors.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ErrorCode != staleWriterRejectedCode {
		t.Fatalf("error_code = %q", body.ErrorCode)
	}
	if body.RequestID != "req-db-error-key" {
		t.Fatalf("request_id = %q", body.RequestID)
	}
}

func TestPersistSyncCommitTransactionLowOrEqualWritesOnlySyncCommit(t *testing.T) {
	request := mustBuildSyncCommitRequest(t, nil)
	input := mustConvertRequest(t, request)
	decision := syncCommitGateDecision{
		Result: gateResultNoop,
		Reason: gateReasonLowOrEqual,
		Record: syncCommitGateRecord{CreatedAt: time.Date(2026, 2, 13, 4, 0, 0, 0, time.UTC)},
	}

	db := newFakeSyncCommitTxDB()
	err := persistSyncCommitTransaction(context.Background(), db, input, decision)
	if err != nil {
		t.Fatalf("persistSyncCommitTransaction() error = %v", err)
	}

	if db.withTxCalls != 1 {
		t.Fatalf("withTxCalls = %d", db.withTxCalls)
	}
	if db.commits != 1 || db.rollbacks != 0 {
		t.Fatalf("commits/rollbacks = %d/%d", db.commits, db.rollbacks)
	}
	if db.lastTx.execCalls != 1 {
		t.Fatalf("exec calls = %d", db.lastTx.execCalls)
	}
	if db.committedTableCounts["sync_commits"] != 1 {
		t.Fatalf("sync_commits writes = %d", db.committedTableCounts["sync_commits"])
	}
	for _, table := range []string{"punch_records", "leave_records", "day_summaries", "month_summaries"} {
		if db.committedTableCounts[table] != 0 {
			t.Fatalf("table %s should not be written, got %d", table, db.committedTableCounts[table])
		}
	}
}

type fakeSyncCommitTxDB struct {
	failTable            string
	failErr              error
	withTxCalls          int
	commits              int
	rollbacks            int
	committedTableCounts map[string]int
	lastTx               *fakeSyncCommitTx
	writer               map[string]fakeSyncCommitWriterState
	syncCommits          map[fakeSyncCommitKey]syncCommitGateRecord
	businessVersions     map[string]map[fakeBusinessKey]int64
}

func newFakeSyncCommitTxDB() *fakeSyncCommitTxDB {
	return &fakeSyncCommitTxDB{
		committedTableCounts: make(map[string]int),
		writer:               make(map[string]fakeSyncCommitWriterState),
		syncCommits:          make(map[fakeSyncCommitKey]syncCommitGateRecord),
		businessVersions: map[string]map[fakeBusinessKey]int64{
			"punch_records":   {},
			"leave_records":   {},
			"day_summaries":   {},
			"month_summaries": {},
		},
	}
}

func (f *fakeSyncCommitTxDB) Health(context.Context) error {
	return nil
}

func (f *fakeSyncCommitTxDB) setWriter(userID, deviceID string, epoch int64) {
	f.writer[userID] = fakeSyncCommitWriterState{deviceID: deviceID, epoch: epoch}
}

func (f *fakeSyncCommitTxDB) syncCommitCount() int {
	return len(f.syncCommits)
}

func (f *fakeSyncCommitTxDB) snapshotCounts() fakeCountsSnapshot {
	return fakeCountsSnapshot{
		syncCommits:  len(f.syncCommits),
		punchRecords: len(f.businessVersions["punch_records"]),
		leaveRecords: len(f.businessVersions["leave_records"]),
		daySummaries: len(f.businessVersions["day_summaries"]),
		monthSummary: len(f.businessVersions["month_summaries"]),
	}
}

func (f *fakeSyncCommitTxDB) snapshotBusinessVersions() fakeBusinessVersionsSnapshot {
	return fakeBusinessVersionsSnapshot{
		punch: cloneBusinessVersionMap(f.businessVersions["punch_records"]),
		leave: cloneBusinessVersionMap(f.businessVersions["leave_records"]),
		day:   cloneBusinessVersionMap(f.businessVersions["day_summaries"]),
		month: cloneBusinessVersionMap(f.businessVersions["month_summaries"]),
	}
}

func (f *fakeSyncCommitTxDB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	f.withTxCalls++
	tx := &fakeSyncCommitTx{
		db:               f,
		id:               fmt.Sprintf("tx-%d", f.withTxCalls),
		failTable:        f.failTable,
		failErr:          f.failErr,
		syncCommits:      cloneSyncCommitMap(f.syncCommits),
		businessVersions: cloneBusinessVersions(f.businessVersions),
	}
	f.lastTx = tx

	if err := fn(tx); err != nil {
		f.rollbacks++
		return err
	}

	f.syncCommits = tx.syncCommits
	f.businessVersions = tx.businessVersions
	f.commits++
	for _, table := range tx.tables {
		f.committedTableCounts[table]++
	}
	return nil
}

type fakeSyncCommitTx struct {
	db               *fakeSyncCommitTxDB
	id               string
	failTable        string
	failErr          error
	execCalls        int
	tables           []string
	txIDs            []string
	syncCommits      map[fakeSyncCommitKey]syncCommitGateRecord
	businessVersions map[string]map[fakeBusinessKey]int64
}

func (f *fakeSyncCommitTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeSyncCommitTx) Commit(context.Context) error   { return nil }
func (f *fakeSyncCommitTx) Rollback(context.Context) error { return nil }
func (f *fakeSyncCommitTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeSyncCommitTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeSyncCommitTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeSyncCommitTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (f *fakeSyncCommitTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	table := detectTable(query)
	if f.failTable != "" && table == f.failTable {
		if f.failErr != nil {
			return pgconn.CommandTag{}, f.failErr
		}
		return pgconn.CommandTag{}, fmt.Errorf("forced failure on %s", table)
	}

	f.execCalls++
	f.tables = append(f.tables, table)
	f.txIDs = append(f.txIDs, f.id)
	switch table {
	case "sync_commits":
		if err := f.execInsertSyncCommit(args); err != nil {
			return pgconn.CommandTag{}, err
		}
	case "punch_records", "leave_records", "day_summaries", "month_summaries":
		if err := f.execUpsertBusiness(table, args); err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.CommandTag{}, nil
}

func (f *fakeSyncCommitTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeSyncCommitTx) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	table := detectTable(query)
	if f.failTable != "" && table == f.failTable {
		if f.failErr != nil {
			return fakeRow{err: f.failErr}
		}
		return fakeRow{err: fmt.Errorf("forced failure on %s", table)}
	}

	switch {
	case strings.Contains(query, "INSERT INTO sync_commits") && strings.Contains(query, "RETURNING payload_hash, status, created_at"):
		return f.queryRowInsertSyncCommitReturning(args)
	case strings.Contains(query, "SELECT payload_hash, status, created_at") && strings.Contains(query, "FROM sync_commits"):
		return f.queryRowLoadSyncCommit(args)
	case strings.Contains(query, "SELECT version FROM"):
		return f.queryRowSelectVersion(table, args)
	default:
		return fakeRow{err: fmt.Errorf("unsupported query: %s", query)}
	}
}
func (f *fakeSyncCommitTx) Conn() *pgx.Conn { return nil }

func detectTable(query string) string {
	switch {
	case strings.Contains(query, "INSERT INTO punch_records"):
		return "punch_records"
	case strings.Contains(query, "INSERT INTO leave_records"):
		return "leave_records"
	case strings.Contains(query, "INSERT INTO day_summaries"):
		return "day_summaries"
	case strings.Contains(query, "INSERT INTO month_summaries"):
		return "month_summaries"
	case strings.Contains(query, "INSERT INTO sync_commits"):
		return "sync_commits"
	case strings.Contains(query, "FROM punch_records"):
		return "punch_records"
	case strings.Contains(query, "FROM leave_records"):
		return "leave_records"
	case strings.Contains(query, "FROM day_summaries"):
		return "day_summaries"
	case strings.Contains(query, "FROM month_summaries"):
		return "month_summaries"
	case strings.Contains(query, "FROM sync_commits"):
		return "sync_commits"
	default:
		return "unknown"
	}
}

type fakeSyncCommitWriterState struct {
	deviceID string
	epoch    int64
}

type fakeSyncCommitKey struct {
	userID string
	syncID string
}

type fakeBusinessKey struct {
	userID string
	id     string
}

type fakeRow struct {
	values []any
	err    error
}

func (f fakeRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != len(f.values) {
		return fmt.Errorf("fake row scan mismatch dest=%d values=%d", len(dest), len(f.values))
	}
	for i, value := range f.values {
		switch target := dest[i].(type) {
		case *string:
			cast, ok := value.(string)
			if !ok {
				return fmt.Errorf("fake row expected string at %d, got %T", i, value)
			}
			*target = cast
		case *time.Time:
			cast, ok := value.(time.Time)
			if !ok {
				return fmt.Errorf("fake row expected time at %d, got %T", i, value)
			}
			*target = cast
		case *int64:
			cast, ok := value.(int64)
			if !ok {
				return fmt.Errorf("fake row expected int64 at %d, got %T", i, value)
			}
			*target = cast
		default:
			return fmt.Errorf("fake row unsupported scan target %T", dest[i])
		}
	}
	return nil
}

func (f *fakeSyncCommitTx) queryRowInsertSyncCommitReturning(args []any) pgx.Row {
	if len(args) < 7 {
		return fakeRow{err: fmt.Errorf("expected sync_commits insert args, got %d", len(args))}
	}
	userID, _ := args[0].(string)
	deviceID, _ := args[1].(string)
	writerEpoch, _ := args[2].(int64)
	syncID, _ := args[3].(string)
	payloadHash, _ := args[4].(string)
	status, _ := args[5].(string)
	createdAt, _ := args[6].(time.Time)

	if state, ok := f.db.writer[userID]; ok {
		if state.deviceID != deviceID || state.epoch != writerEpoch {
			return fakeRow{err: &pgconn.PgError{Code: "P0001", Message: "[error_key=SYNC_COMMIT_STALE_WRITER] stale writer"}}
		}
	}

	key := fakeSyncCommitKey{userID: userID, syncID: syncID}
	if _, exists := f.syncCommits[key]; exists {
		return fakeRow{err: pgx.ErrNoRows}
	}

	record := syncCommitGateRecord{
		PayloadHash: payloadHash,
		Status:      status,
		CreatedAt:   createdAt,
	}
	f.syncCommits[key] = record
	return fakeRow{values: []any{record.PayloadHash, record.Status, record.CreatedAt}}
}

func (f *fakeSyncCommitTx) queryRowLoadSyncCommit(args []any) pgx.Row {
	if len(args) < 2 {
		return fakeRow{err: fmt.Errorf("expected sync_commits select args, got %d", len(args))}
	}
	userID, _ := args[0].(string)
	syncID, _ := args[1].(string)
	key := fakeSyncCommitKey{userID: userID, syncID: syncID}
	record, ok := f.syncCommits[key]
	if !ok {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{values: []any{record.PayloadHash, record.Status, record.CreatedAt}}
}

func (f *fakeSyncCommitTx) queryRowSelectVersion(table string, args []any) pgx.Row {
	if len(args) < 2 {
		return fakeRow{err: fmt.Errorf("expected version select args, got %d", len(args))}
	}
	userID, _ := args[0].(string)
	recordID, _ := args[1].(string)
	key := fakeBusinessKey{userID: userID, id: recordID}
	version, ok := f.businessVersions[table][key]
	if !ok {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{values: []any{version}}
}

func (f *fakeSyncCommitTx) execInsertSyncCommit(args []any) error {
	if len(args) < 7 {
		return fmt.Errorf("expected sync_commits exec args, got %d", len(args))
	}
	userID, ok := args[0].(string)
	if !ok {
		return fmt.Errorf("expected userID string, got %T", args[0])
	}
	syncID, ok := args[3].(string)
	if !ok {
		return fmt.Errorf("expected syncID string, got %T", args[3])
	}
	payloadHash, _ := args[4].(string)
	status, _ := args[5].(string)
	createdAt, _ := args[6].(time.Time)

	key := fakeSyncCommitKey{userID: userID, syncID: syncID}
	if _, exists := f.syncCommits[key]; exists {
		return nil
	}
	f.syncCommits[key] = syncCommitGateRecord{
		PayloadHash: payloadHash,
		Status:      status,
		CreatedAt:   createdAt,
	}
	return nil
}

func (f *fakeSyncCommitTx) execUpsertBusiness(table string, args []any) error {
	if len(args) < 3 {
		return fmt.Errorf("expected %s exec args, got %d", table, len(args))
	}
	recordID, ok := args[0].(string)
	if !ok {
		return fmt.Errorf("expected recordID string, got %T", args[0])
	}
	userID, ok := args[1].(string)
	if !ok {
		return fmt.Errorf("expected userID string, got %T", args[1])
	}
	incomingVersion, err := businessVersionFromArgs(table, args)
	if err != nil {
		return err
	}

	key := fakeBusinessKey{userID: userID, id: recordID}
	existingVersion, ok := f.businessVersions[table][key]
	if !ok {
		f.businessVersions[table][key] = incomingVersion
		return nil
	}
	if incomingVersion > existingVersion {
		f.businessVersions[table][key] = incomingVersion
	}
	return nil
}

func businessVersionFromArgs(table string, args []any) (int64, error) {
	var index int
	switch table {
	case "punch_records":
		index = 9
	case "leave_records":
		index = 5
	case "day_summaries":
		index = 11
	case "month_summaries":
		index = 5
	default:
		return 0, fmt.Errorf("unsupported business table %q", table)
	}
	if index >= len(args) {
		return 0, fmt.Errorf("expected version arg for %s at %d, got %d args", table, index, len(args))
	}
	version, ok := args[index].(int64)
	if !ok {
		return 0, fmt.Errorf("expected %s version int64, got %T", table, args[index])
	}
	return version, nil
}

type fakeCountsSnapshot struct {
	syncCommits  int
	punchRecords int
	leaveRecords int
	daySummaries int
	monthSummary int
}

func (f fakeCountsSnapshot) diff(other fakeCountsSnapshot) string {
	var parts []string
	if f.syncCommits != other.syncCommits {
		parts = append(parts, fmt.Sprintf("sync_commits %d->%d", f.syncCommits, other.syncCommits))
	}
	if f.punchRecords != other.punchRecords {
		parts = append(parts, fmt.Sprintf("punch_records %d->%d", f.punchRecords, other.punchRecords))
	}
	if f.leaveRecords != other.leaveRecords {
		parts = append(parts, fmt.Sprintf("leave_records %d->%d", f.leaveRecords, other.leaveRecords))
	}
	if f.daySummaries != other.daySummaries {
		parts = append(parts, fmt.Sprintf("day_summaries %d->%d", f.daySummaries, other.daySummaries))
	}
	if f.monthSummary != other.monthSummary {
		parts = append(parts, fmt.Sprintf("month_summaries %d->%d", f.monthSummary, other.monthSummary))
	}
	return strings.Join(parts, ", ")
}

type fakeBusinessVersionsSnapshot struct {
	punch map[fakeBusinessKey]int64
	leave map[fakeBusinessKey]int64
	day   map[fakeBusinessKey]int64
	month map[fakeBusinessKey]int64
}

func (f fakeBusinessVersionsSnapshot) diff(other fakeBusinessVersionsSnapshot) string {
	var parts []string
	parts = appendDiffIfChanged(parts, "punch_records", f.punch, other.punch)
	parts = appendDiffIfChanged(parts, "leave_records", f.leave, other.leave)
	parts = appendDiffIfChanged(parts, "day_summaries", f.day, other.day)
	parts = appendDiffIfChanged(parts, "month_summaries", f.month, other.month)
	return strings.Join(parts, ", ")
}

func appendDiffIfChanged(parts []string, table string, left, right map[fakeBusinessKey]int64) []string {
	if len(left) != len(right) {
		return append(parts, fmt.Sprintf("%s count %d->%d", table, len(left), len(right)))
	}
	for key, leftV := range left {
		if rightV, ok := right[key]; !ok || rightV != leftV {
			return append(parts, fmt.Sprintf("%s changed", table))
		}
	}
	for key := range right {
		if _, ok := left[key]; !ok {
			return append(parts, fmt.Sprintf("%s changed", table))
		}
	}
	return parts
}

func cloneSyncCommitMap(src map[fakeSyncCommitKey]syncCommitGateRecord) map[fakeSyncCommitKey]syncCommitGateRecord {
	dst := make(map[fakeSyncCommitKey]syncCommitGateRecord, len(src))
	for key, record := range src {
		dst[key] = record
	}
	return dst
}

func cloneBusinessVersions(src map[string]map[fakeBusinessKey]int64) map[string]map[fakeBusinessKey]int64 {
	dst := make(map[string]map[fakeBusinessKey]int64, len(src))
	for table, versions := range src {
		dst[table] = cloneBusinessVersionMap(versions)
	}
	return dst
}

func cloneBusinessVersionMap(src map[fakeBusinessKey]int64) map[fakeBusinessKey]int64 {
	dst := make(map[fakeBusinessKey]int64, len(src))
	for key, version := range src {
		dst[key] = version
	}
	return dst
}
