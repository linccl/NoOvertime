package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	syncCommitsPath         = "/api/v1/sync/commits"
	syncCommitsBodyMaxBytes = 2 << 20

	methodNotAllowedCode    = "METHOD_NOT_ALLOWED"
	invalidArgumentCode     = "INVALID_ARGUMENT"
	unknownFieldCode        = "UNKNOWN_FIELD"
	syncIDConflictCode      = "SYNC_ID_CONFLICT"
	staleWriterRejectedCode = "STALE_WRITER_REJECTED"
	punchEndRequiresStart   = "PUNCH_END_REQUIRES_START"
	punchEndNotAfterStart   = "PUNCH_END_NOT_AFTER_START"
	autoPunchLeaveConflict  = "CONFLICT_AUTO_PUNCH_FULL_DAY_LEAVE"
	timePrecisionInvalid    = "TIME_PRECISION_INVALID"
	timeFieldsMismatch      = "TIME_FIELDS_MISMATCH"

	gateResultApplied  = "APPLIED"
	gateResultNoop     = "NOOP"
	gateResultRejected = "REJECTED"

	gateReasonAppliedWrite   = "APPLIED_WRITE"
	gateReasonReplayNoop     = "REPLAY_NOOP"
	gateReasonSyncIDConflict = "SYNC_ID_CONFLICT"
	gateReasonLowOrEqual     = "LOW_OR_EQUAL_VERSION"

	syncCommitStatusApplied = "APPLIED"
)

var (
	uuidRegex        = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	payloadHashRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errorKeyRegex    = regexp.MustCompile(`\[error_key=([A-Z0-9_]+)\]`)
)

var (
	allowedPunchTypes    = map[string]struct{}{"START": {}, "END": {}}
	allowedPunchSources  = map[string]struct{}{"AUTO": {}, "MANUAL": {}, "MAKEUP": {}, "EDIT": {}}
	allowedLeaveTypes    = map[string]struct{}{"AM": {}, "PM": {}, "FULL_DAY": {}}
	allowedSummaryStatus = map[string]struct{}{"INCOMPLETE": {}, "COMPUTED": {}}
)

type syncCommitRequest struct {
	SyncID         string             `json:"sync_id"`
	PayloadHash    string             `json:"payload_hash"`
	PunchRecords   []punchRecordJSON  `json:"punch_records"`
	LeaveRecords   []leaveRecordJSON  `json:"leave_records"`
	DaySummaries   []daySummaryJSON   `json:"day_summaries"`
	MonthSummaries []monthSummaryJSON `json:"month_summaries"`
}

type punchRecordJSON struct {
	ID          string  `json:"id"`
	LocalDate   string  `json:"local_date"`
	Type        string  `json:"type"`
	AtUTC       string  `json:"at_utc"`
	TimezoneID  string  `json:"timezone_id"`
	MinuteOfDay int     `json:"minute_of_day"`
	Source      string  `json:"source"`
	DeletedAt   *string `json:"deleted_at"`
	Version     int64   `json:"version"`
}

type leaveRecordJSON struct {
	ID        string  `json:"id"`
	LocalDate string  `json:"local_date"`
	LeaveType string  `json:"leave_type"`
	DeletedAt *string `json:"deleted_at"`
	Version   int64   `json:"version"`
}

type daySummaryJSON struct {
	ID          string  `json:"id"`
	LocalDate   string  `json:"local_date"`
	StartAtUTC  *string `json:"start_at_utc"`
	EndAtUTC    *string `json:"end_at_utc"`
	IsLeaveDay  *bool   `json:"is_leave_day"`
	LeaveType   *string `json:"leave_type"`
	IsLate      *bool   `json:"is_late"`
	WorkMinutes *int    `json:"work_minutes"`
	AdjustMins  *int    `json:"adjust_minutes"`
	Status      string  `json:"status"`
	Version     int64   `json:"version"`
	UpdatedAt   string  `json:"updated_at"`
}

type monthSummaryJSON struct {
	ID               string `json:"id"`
	MonthStart       string `json:"month_start"`
	WorkMinutesTotal int    `json:"work_minutes_total"`
	AdjustMinutesBal int    `json:"adjust_minutes_balance"`
	Version          int64  `json:"version"`
	UpdatedAt        string `json:"updated_at"`
}

// SyncCommitInput is the normalized request structure consumed by sync gate/tx layer.
type SyncCommitInput struct {
	UserID              string
	DeviceID            string
	WriterEpoch         int64
	SyncID              string
	PayloadHash         string
	ComputedPayloadHash string
	PunchRecords        []PunchRecordInput
	LeaveRecords        []LeaveRecordInput
	DaySummaries        []DaySummaryInput
	MonthSummaries      []MonthSummaryInput
}

type PunchRecordInput struct {
	ID          string
	LocalDate   time.Time
	Type        string
	AtUTC       time.Time
	TimezoneID  string
	MinuteOfDay int
	Source      string
	DeletedAt   *time.Time
	Version     int64
}

type LeaveRecordInput struct {
	ID        string
	LocalDate time.Time
	LeaveType string
	DeletedAt *time.Time
	Version   int64
}

type DaySummaryInput struct {
	ID          string
	LocalDate   time.Time
	StartAtUTC  *time.Time
	EndAtUTC    *time.Time
	IsLeaveDay  bool
	LeaveType   *string
	IsLate      *bool
	WorkMinutes *int
	AdjustMins  *int
	Status      string
	Version     int64
	UpdatedAt   time.Time
}

type MonthSummaryInput struct {
	ID               string
	MonthStart       time.Time
	WorkMinutesTotal int
	AdjustMinutesBal int
	Version          int64
	UpdatedAt        time.Time
}

type syncCommitResponse struct {
	RequestID  string               `json:"request_id"`
	GateResult string               `json:"gate_result"`
	GateReason string               `json:"gate_reason"`
	UserID     string               `json:"user_id"`
	SyncCommit syncCommitResultBody `json:"sync_commit"`
}

type syncCommitResultBody struct {
	SyncID    string `json:"sync_id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type syncCommitConflictResponse struct {
	ErrorCode  string `json:"error_code"`
	Message    string `json:"message"`
	GateResult string `json:"gate_result"`
	GateReason string `json:"gate_reason"`
	RequestID  string `json:"request_id"`
}

type syncCommitGateRecord struct {
	PayloadHash string
	Status      string
	CreatedAt   time.Time
}

type syncCommitGateDecision struct {
	Result    string
	Reason    string
	Record    syncCommitGateRecord
	ErrorCode string
	Message   string
}

func gateAndPersistSyncCommit(
	ctx context.Context,
	db HealthChecker,
	header mobileTokenHeader,
	input SyncCommitInput,
	now time.Time,
) (SyncCommitInput, syncCommitGateDecision, error) {
	txDB, ok := db.(syncCommitTxDB)
	if !ok {
		return SyncCommitInput{}, syncCommitGateDecision{}, fmt.Errorf("database does not support sync commit transactions")
	}

	resolvedInput := input
	var decision syncCommitGateDecision
	if err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		auth, err := loadMobileAuthContext(ctx, tx, header, true)
		if err != nil {
			return err
		}
		auth, err = bindAnonymousMobileToken(ctx, tx, auth)
		if err != nil {
			return err
		}

		resolvedInput.UserID = auth.UserID
		resolvedInput.DeviceID = auth.DeviceID
		resolvedInput.WriterEpoch = auth.WriterEpoch

		createdAt := now.UTC()
		record, inserted, err := tryInsertSyncCommitRecord(ctx, tx, resolvedInput, createdAt)
		if err != nil {
			return err
		}

		if !inserted {
			existing, err := loadSyncCommitRecord(ctx, tx, resolvedInput.UserID, resolvedInput.SyncID)
			if err != nil {
				return err
			}
			if existing.PayloadHash == resolvedInput.PayloadHash {
				decision = syncCommitGateDecision{
					Result: gateResultNoop,
					Reason: gateReasonReplayNoop,
					Record: existing,
				}
				return nil
			}
			decision = syncCommitGateDecision{
				Result:    gateResultRejected,
				Reason:    gateReasonSyncIDConflict,
				Record:    existing,
				ErrorCode: syncIDConflictCode,
				Message:   "same sync_id but different payload_hash",
			}
			return nil
		}

		if err := validateSyncBusinessRules(resolvedInput); err != nil {
			return err
		}

		shouldApply, err := shouldApplySyncCommit(ctx, tx, resolvedInput)
		if err != nil {
			return err
		}
		if !shouldApply {
			decision = syncCommitGateDecision{
				Result: gateResultNoop,
				Reason: gateReasonLowOrEqual,
				Record: record,
			}
			return nil
		}

		exec := pgxSyncCommitTxExecutor{tx: tx}
		if err := writePunchRecords(ctx, exec, resolvedInput); err != nil {
			return fmt.Errorf("write punch_records: %w", err)
		}
		if err := writeLeaveRecords(ctx, exec, resolvedInput); err != nil {
			return fmt.Errorf("write leave_records: %w", err)
		}
		if err := writeDaySummaries(ctx, exec, resolvedInput); err != nil {
			return fmt.Errorf("write day_summaries: %w", err)
		}
		if err := writeMonthSummaries(ctx, exec, resolvedInput); err != nil {
			return fmt.Errorf("write month_summaries: %w", err)
		}

		decision = syncCommitGateDecision{
			Result: gateResultApplied,
			Reason: gateReasonAppliedWrite,
			Record: record,
		}
		return nil
	}); err != nil {
		return SyncCommitInput{}, syncCommitGateDecision{}, err
	}

	return resolvedInput, decision, nil
}

func tryInsertSyncCommitRecord(ctx context.Context, tx pgx.Tx, input SyncCommitInput, createdAt time.Time) (syncCommitGateRecord, bool, error) {
	const query = `
INSERT INTO sync_commits (
	user_id, device_id, writer_epoch, sync_id, payload_hash, status, created_at, applied_at
) VALUES (
	$1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8
)
ON CONFLICT (user_id, sync_id) DO NOTHING
RETURNING payload_hash, status, created_at
`
	var record syncCommitGateRecord
	if err := tx.QueryRow(ctx, query,
		input.UserID,
		input.DeviceID,
		input.WriterEpoch,
		input.SyncID,
		input.PayloadHash,
		syncCommitStatusApplied,
		createdAt,
		createdAt,
	).Scan(&record.PayloadHash, &record.Status, &record.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return syncCommitGateRecord{}, false, nil
		}
		return syncCommitGateRecord{}, false, err
	}
	return record, true, nil
}

func loadSyncCommitRecord(ctx context.Context, tx pgx.Tx, userID, syncID string) (syncCommitGateRecord, error) {
	const query = `
SELECT payload_hash, status, created_at
  FROM sync_commits
 WHERE user_id = $1::uuid
   AND sync_id = $2::uuid
`
	var record syncCommitGateRecord
	if err := tx.QueryRow(ctx, query, userID, syncID).Scan(&record.PayloadHash, &record.Status, &record.CreatedAt); err != nil {
		return syncCommitGateRecord{}, err
	}
	return record, nil
}

func shouldApplySyncCommit(ctx context.Context, tx pgx.Tx, input SyncCommitInput) (bool, error) {
	for _, record := range input.PunchRecords {
		higher, err := hasHigherVersion(ctx, tx, "punch_records", input.UserID, record.ID, record.Version)
		if err != nil {
			return false, err
		}
		if higher {
			return true, nil
		}
	}
	for _, record := range input.LeaveRecords {
		higher, err := hasHigherVersion(ctx, tx, "leave_records", input.UserID, record.ID, record.Version)
		if err != nil {
			return false, err
		}
		if higher {
			return true, nil
		}
	}
	for _, record := range input.DaySummaries {
		higher, err := hasHigherVersion(ctx, tx, "day_summaries", input.UserID, record.ID, record.Version)
		if err != nil {
			return false, err
		}
		if higher {
			return true, nil
		}
	}
	for _, record := range input.MonthSummaries {
		higher, err := hasHigherVersion(ctx, tx, "month_summaries", input.UserID, record.ID, record.Version)
		if err != nil {
			return false, err
		}
		if higher {
			return true, nil
		}
	}
	return false, nil
}

func hasHigherVersion(ctx context.Context, tx pgx.Tx, table, userID, recordID string, incomingVersion int64) (bool, error) {
	var query string
	switch table {
	case "punch_records":
		query = `SELECT version FROM punch_records WHERE user_id = $1::uuid AND id = $2::uuid`
	case "leave_records":
		query = `SELECT version FROM leave_records WHERE user_id = $1::uuid AND id = $2::uuid`
	case "day_summaries":
		query = `SELECT version FROM day_summaries WHERE user_id = $1::uuid AND id = $2::uuid`
	case "month_summaries":
		query = `SELECT version FROM month_summaries WHERE user_id = $1::uuid AND id = $2::uuid`
	default:
		return false, fmt.Errorf("unsupported version gate table %q", table)
	}

	var existingVersion int64
	if err := tx.QueryRow(ctx, query, userID, recordID).Scan(&existingVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	return incomingVersion > existingVersion, nil
}

func (s *Server) syncCommitsHandler(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPost {
		return apperrors.New(http.StatusMethodNotAllowed, methodNotAllowedCode, "method not allowed")
	}

	input, err := parseSyncCommitInput(io.LimitReader(r.Body, syncCommitsBodyMaxBytes))
	if err != nil {
		return err
	}
	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}

	requestID := requestIDFromContext(r.Context())
	if requestID == "" {
		requestID = generateRequestID()
	}

	resolvedInput, decision, err := gateAndPersistSyncCommit(r.Context(), s.db, header, input, s.now())
	if err != nil {
		var apiErr apperrors.APIError
		if errors.As(err, &apiErr) {
			return apiErr
		}

		mappedErr, mappedConflict, ok := mapSyncCommitPersistenceError(err, requestID)
		if ok {
			if mappedConflict != nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				return json.NewEncoder(w).Encode(*mappedConflict)
			}
			return mappedErr
		}

		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}
	log.Printf(
		"sync_commit_gate user_id=%s sync_id=%s gate_result=%s gate_reason=%s request_id=%s",
		resolvedInput.UserID,
		resolvedInput.SyncID,
		decision.Result,
		decision.Reason,
		requestID,
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if decision.Result == gateResultRejected {
		w.WriteHeader(http.StatusConflict)
		return json.NewEncoder(w).Encode(syncCommitConflictResponse{
			ErrorCode:  syncIDConflictCode,
			Message:    decision.Message,
			GateResult: gateResultRejected,
			GateReason: gateReasonSyncIDConflict,
			RequestID:  requestID,
		})
	}

	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(syncCommitResponse{
		RequestID:  requestID,
		GateResult: decision.Result,
		GateReason: decision.Reason,
		UserID:     resolvedInput.UserID,
		SyncCommit: syncCommitResultBody{
			SyncID:    resolvedInput.SyncID,
			Status:    decision.Record.Status,
			CreatedAt: decision.Record.CreatedAt.UTC().Format(time.RFC3339),
		},
	})
}

func parseSyncCommitInput(reader io.Reader) (SyncCommitInput, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var request syncCommitRequest
	if err := decoder.Decode(&request); err != nil {
		return SyncCommitInput{}, decodeErrorToAPIError(err)
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SyncCommitInput{}, apperrors.New(http.StatusBadRequest, invalidArgumentCode, "request body must contain a single JSON object")
	}

	input, err := convertSyncCommitRequest(request)
	if err != nil {
		return SyncCommitInput{}, err
	}

	generatedHash, err := generatePayloadHash(input)
	if err != nil {
		return SyncCommitInput{}, apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}
	if request.PayloadHash != generatedHash {
		return SyncCommitInput{}, invalidArgument("payload_hash mismatch with canonical payload")
	}
	input.ComputedPayloadHash = generatedHash
	input.PayloadHash = generatedHash

	return input, nil
}

func generatePayloadHash(input SyncCommitInput) (string, error) {
	payload := buildCanonicalPayload(input)
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func buildCanonicalPayload(input SyncCommitInput) canonicalSyncPayload {
	punchRecords := append([]PunchRecordInput(nil), input.PunchRecords...)
	sort.SliceStable(punchRecords, func(i, j int) bool {
		return punchRecords[i].ID < punchRecords[j].ID
	})

	leaveRecords := append([]LeaveRecordInput(nil), input.LeaveRecords...)
	sort.SliceStable(leaveRecords, func(i, j int) bool {
		return leaveRecords[i].ID < leaveRecords[j].ID
	})

	daySummaries := append([]DaySummaryInput(nil), input.DaySummaries...)
	sort.SliceStable(daySummaries, func(i, j int) bool {
		leftDate := daySummaries[i].LocalDate.Format("2006-01-02")
		rightDate := daySummaries[j].LocalDate.Format("2006-01-02")
		if leftDate == rightDate {
			return daySummaries[i].ID < daySummaries[j].ID
		}
		return leftDate < rightDate
	})

	monthSummaries := append([]MonthSummaryInput(nil), input.MonthSummaries...)
	sort.SliceStable(monthSummaries, func(i, j int) bool {
		leftMonth := monthSummaries[i].MonthStart.Format("2006-01-02")
		rightMonth := monthSummaries[j].MonthStart.Format("2006-01-02")
		if leftMonth == rightMonth {
			return monthSummaries[i].ID < monthSummaries[j].ID
		}
		return leftMonth < rightMonth
	})

	result := canonicalSyncPayload{
		SyncID: input.SyncID,
	}

	result.PunchRecords = make([]canonicalPunchRecord, 0, len(punchRecords))
	for _, record := range punchRecords {
		result.PunchRecords = append(result.PunchRecords, canonicalPunchRecord{
			ID:          record.ID,
			LocalDate:   record.LocalDate.Format("2006-01-02"),
			Type:        record.Type,
			AtUTC:       record.AtUTC.UTC().Format(time.RFC3339),
			TimezoneID:  record.TimezoneID,
			MinuteOfDay: record.MinuteOfDay,
			Source:      record.Source,
			DeletedAt:   formatOptionalTime(record.DeletedAt),
			Version:     record.Version,
		})
	}

	result.LeaveRecords = make([]canonicalLeaveRecord, 0, len(leaveRecords))
	for _, record := range leaveRecords {
		result.LeaveRecords = append(result.LeaveRecords, canonicalLeaveRecord{
			ID:        record.ID,
			LocalDate: record.LocalDate.Format("2006-01-02"),
			LeaveType: record.LeaveType,
			DeletedAt: formatOptionalTime(record.DeletedAt),
			Version:   record.Version,
		})
	}

	result.DaySummaries = make([]canonicalDaySummary, 0, len(daySummaries))
	for _, record := range daySummaries {
		result.DaySummaries = append(result.DaySummaries, canonicalDaySummary{
			ID:          record.ID,
			LocalDate:   record.LocalDate.Format("2006-01-02"),
			StartAtUTC:  formatOptionalTime(record.StartAtUTC),
			EndAtUTC:    formatOptionalTime(record.EndAtUTC),
			IsLeaveDay:  record.IsLeaveDay,
			LeaveType:   record.LeaveType,
			IsLate:      record.IsLate,
			WorkMinutes: record.WorkMinutes,
			AdjustMins:  record.AdjustMins,
			Status:      record.Status,
			Version:     record.Version,
			UpdatedAt:   record.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	result.MonthSummaries = make([]canonicalMonthSummary, 0, len(monthSummaries))
	for _, record := range monthSummaries {
		result.MonthSummaries = append(result.MonthSummaries, canonicalMonthSummary{
			ID:               record.ID,
			MonthStart:       record.MonthStart.Format("2006-01-02"),
			WorkMinutesTotal: record.WorkMinutesTotal,
			AdjustMinutesBal: record.AdjustMinutesBal,
			Version:          record.Version,
			UpdatedAt:        record.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}

	return result
}

type canonicalSyncPayload struct {
	SyncID         string                  `json:"sync_id"`
	PunchRecords   []canonicalPunchRecord  `json:"punch_records"`
	LeaveRecords   []canonicalLeaveRecord  `json:"leave_records"`
	DaySummaries   []canonicalDaySummary   `json:"day_summaries"`
	MonthSummaries []canonicalMonthSummary `json:"month_summaries"`
}

type canonicalPunchRecord struct {
	ID          string  `json:"id"`
	LocalDate   string  `json:"local_date"`
	Type        string  `json:"type"`
	AtUTC       string  `json:"at_utc"`
	TimezoneID  string  `json:"timezone_id"`
	MinuteOfDay int     `json:"minute_of_day"`
	Source      string  `json:"source"`
	DeletedAt   *string `json:"deleted_at"`
	Version     int64   `json:"version"`
}

type canonicalLeaveRecord struct {
	ID        string  `json:"id"`
	LocalDate string  `json:"local_date"`
	LeaveType string  `json:"leave_type"`
	DeletedAt *string `json:"deleted_at"`
	Version   int64   `json:"version"`
}

type canonicalDaySummary struct {
	ID          string  `json:"id"`
	LocalDate   string  `json:"local_date"`
	StartAtUTC  *string `json:"start_at_utc"`
	EndAtUTC    *string `json:"end_at_utc"`
	IsLeaveDay  bool    `json:"is_leave_day"`
	LeaveType   *string `json:"leave_type"`
	IsLate      *bool   `json:"is_late"`
	WorkMinutes *int    `json:"work_minutes"`
	AdjustMins  *int    `json:"adjust_minutes"`
	Status      string  `json:"status"`
	Version     int64   `json:"version"`
	UpdatedAt   string  `json:"updated_at"`
}

type canonicalMonthSummary struct {
	ID               string `json:"id"`
	MonthStart       string `json:"month_start"`
	WorkMinutesTotal int    `json:"work_minutes_total"`
	AdjustMinutesBal int    `json:"adjust_minutes_balance"`
	Version          int64  `json:"version"`
	UpdatedAt        string `json:"updated_at"`
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func decodeErrorToAPIError(err error) error {
	message := strings.TrimSpace(err.Error())
	if strings.Contains(message, "unknown field") {
		return apperrors.New(http.StatusBadRequest, unknownFieldCode, message)
	}
	return apperrors.New(http.StatusBadRequest, invalidArgumentCode, message)
}

func convertSyncCommitRequest(request syncCommitRequest) (SyncCommitInput, error) {
	var input SyncCommitInput

	syncID, err := requireUUID("sync_id", request.SyncID)
	if err != nil {
		return SyncCommitInput{}, err
	}
	payloadHash, err := requirePayloadHash(request.PayloadHash)
	if err != nil {
		return SyncCommitInput{}, err
	}
	if request.PunchRecords == nil || request.LeaveRecords == nil || request.DaySummaries == nil || request.MonthSummaries == nil {
		return SyncCommitInput{}, invalidArgument("punch_records, leave_records, day_summaries and month_summaries are required")
	}

	input = SyncCommitInput{
		SyncID:      syncID,
		PayloadHash: payloadHash,
	}

	input.PunchRecords = make([]PunchRecordInput, 0, len(request.PunchRecords))
	for i, item := range request.PunchRecords {
		record, err := convertPunchRecord(i, item)
		if err != nil {
			return SyncCommitInput{}, err
		}
		input.PunchRecords = append(input.PunchRecords, record)
	}

	input.LeaveRecords = make([]LeaveRecordInput, 0, len(request.LeaveRecords))
	for i, item := range request.LeaveRecords {
		record, err := convertLeaveRecord(i, item)
		if err != nil {
			return SyncCommitInput{}, err
		}
		input.LeaveRecords = append(input.LeaveRecords, record)
	}

	input.DaySummaries = make([]DaySummaryInput, 0, len(request.DaySummaries))
	for i, item := range request.DaySummaries {
		record, err := convertDaySummary(i, item)
		if err != nil {
			return SyncCommitInput{}, err
		}
		input.DaySummaries = append(input.DaySummaries, record)
	}

	input.MonthSummaries = make([]MonthSummaryInput, 0, len(request.MonthSummaries))
	for i, item := range request.MonthSummaries {
		record, err := convertMonthSummary(i, item)
		if err != nil {
			return SyncCommitInput{}, err
		}
		input.MonthSummaries = append(input.MonthSummaries, record)
	}

	return input, nil
}

func convertPunchRecord(index int, item punchRecordJSON) (PunchRecordInput, error) {
	recordID, err := requireUUID(fmt.Sprintf("punch_records[%d].id", index), item.ID)
	if err != nil {
		return PunchRecordInput{}, err
	}
	localDate, err := parseDate(fmt.Sprintf("punch_records[%d].local_date", index), item.LocalDate)
	if err != nil {
		return PunchRecordInput{}, err
	}
	if !isAllowed(allowedPunchTypes, item.Type) {
		return PunchRecordInput{}, invalidArgument(fmt.Sprintf("punch_records[%d].type is invalid", index))
	}
	atUTC, err := parseUTCTime(fmt.Sprintf("punch_records[%d].at_utc", index), item.AtUTC)
	if err != nil {
		return PunchRecordInput{}, err
	}
	timezoneID := strings.TrimSpace(item.TimezoneID)
	if timezoneID == "" {
		return PunchRecordInput{}, invalidArgument(fmt.Sprintf("punch_records[%d].timezone_id is required", index))
	}
	if item.MinuteOfDay < 0 || item.MinuteOfDay > 1439 {
		return PunchRecordInput{}, invalidArgument(fmt.Sprintf("punch_records[%d].minute_of_day must be between 0 and 1439", index))
	}
	if !isAllowed(allowedPunchSources, item.Source) {
		return PunchRecordInput{}, invalidArgument(fmt.Sprintf("punch_records[%d].source is invalid", index))
	}
	if item.Version <= 0 {
		return PunchRecordInput{}, invalidArgument(fmt.Sprintf("punch_records[%d].version must be > 0", index))
	}

	deletedAt, err := parseOptionalUTCTime(fmt.Sprintf("punch_records[%d].deleted_at", index), item.DeletedAt)
	if err != nil {
		return PunchRecordInput{}, err
	}

	return PunchRecordInput{
		ID:          recordID,
		LocalDate:   localDate,
		Type:        item.Type,
		AtUTC:       atUTC,
		TimezoneID:  timezoneID,
		MinuteOfDay: item.MinuteOfDay,
		Source:      item.Source,
		DeletedAt:   deletedAt,
		Version:     item.Version,
	}, nil
}

func convertLeaveRecord(index int, item leaveRecordJSON) (LeaveRecordInput, error) {
	recordID, err := requireUUID(fmt.Sprintf("leave_records[%d].id", index), item.ID)
	if err != nil {
		return LeaveRecordInput{}, err
	}
	localDate, err := parseDate(fmt.Sprintf("leave_records[%d].local_date", index), item.LocalDate)
	if err != nil {
		return LeaveRecordInput{}, err
	}
	if !isAllowed(allowedLeaveTypes, item.LeaveType) {
		return LeaveRecordInput{}, invalidArgument(fmt.Sprintf("leave_records[%d].leave_type is invalid", index))
	}
	if item.Version <= 0 {
		return LeaveRecordInput{}, invalidArgument(fmt.Sprintf("leave_records[%d].version must be > 0", index))
	}
	deletedAt, err := parseOptionalUTCTime(fmt.Sprintf("leave_records[%d].deleted_at", index), item.DeletedAt)
	if err != nil {
		return LeaveRecordInput{}, err
	}

	return LeaveRecordInput{
		ID:        recordID,
		LocalDate: localDate,
		LeaveType: item.LeaveType,
		DeletedAt: deletedAt,
		Version:   item.Version,
	}, nil
}

func convertDaySummary(index int, item daySummaryJSON) (DaySummaryInput, error) {
	recordID, err := requireUUID(fmt.Sprintf("day_summaries[%d].id", index), item.ID)
	if err != nil {
		return DaySummaryInput{}, err
	}
	localDate, err := parseDate(fmt.Sprintf("day_summaries[%d].local_date", index), item.LocalDate)
	if err != nil {
		return DaySummaryInput{}, err
	}
	if item.IsLeaveDay == nil {
		return DaySummaryInput{}, invalidArgument(fmt.Sprintf("day_summaries[%d].is_leave_day is required", index))
	}
	if !isAllowed(allowedSummaryStatus, item.Status) {
		return DaySummaryInput{}, invalidArgument(fmt.Sprintf("day_summaries[%d].status is invalid", index))
	}
	if item.Version <= 0 {
		return DaySummaryInput{}, invalidArgument(fmt.Sprintf("day_summaries[%d].version must be > 0", index))
	}
	updatedAt, err := parseUTCTime(fmt.Sprintf("day_summaries[%d].updated_at", index), item.UpdatedAt)
	if err != nil {
		return DaySummaryInput{}, err
	}

	startAt, err := parseOptionalUTCTime(fmt.Sprintf("day_summaries[%d].start_at_utc", index), item.StartAtUTC)
	if err != nil {
		return DaySummaryInput{}, err
	}
	endAt, err := parseOptionalUTCTime(fmt.Sprintf("day_summaries[%d].end_at_utc", index), item.EndAtUTC)
	if err != nil {
		return DaySummaryInput{}, err
	}

	var leaveType *string
	if item.LeaveType != nil {
		value := strings.TrimSpace(*item.LeaveType)
		if value != "" {
			if !isAllowed(allowedLeaveTypes, value) {
				return DaySummaryInput{}, invalidArgument(fmt.Sprintf("day_summaries[%d].leave_type is invalid", index))
			}
			leaveType = &value
		}
	}

	return DaySummaryInput{
		ID:          recordID,
		LocalDate:   localDate,
		StartAtUTC:  startAt,
		EndAtUTC:    endAt,
		IsLeaveDay:  *item.IsLeaveDay,
		LeaveType:   leaveType,
		IsLate:      item.IsLate,
		WorkMinutes: item.WorkMinutes,
		AdjustMins:  item.AdjustMins,
		Status:      item.Status,
		Version:     item.Version,
		UpdatedAt:   updatedAt,
	}, nil
}

func convertMonthSummary(index int, item monthSummaryJSON) (MonthSummaryInput, error) {
	recordID, err := requireUUID(fmt.Sprintf("month_summaries[%d].id", index), item.ID)
	if err != nil {
		return MonthSummaryInput{}, err
	}
	monthStart, err := parseDate(fmt.Sprintf("month_summaries[%d].month_start", index), item.MonthStart)
	if err != nil {
		return MonthSummaryInput{}, err
	}
	if monthStart.Day() != 1 {
		return MonthSummaryInput{}, invalidArgument(fmt.Sprintf("month_summaries[%d].month_start must be first day of month", index))
	}
	if item.WorkMinutesTotal < 0 {
		return MonthSummaryInput{}, invalidArgument(fmt.Sprintf("month_summaries[%d].work_minutes_total must be >= 0", index))
	}
	if item.Version <= 0 {
		return MonthSummaryInput{}, invalidArgument(fmt.Sprintf("month_summaries[%d].version must be > 0", index))
	}
	updatedAt, err := parseUTCTime(fmt.Sprintf("month_summaries[%d].updated_at", index), item.UpdatedAt)
	if err != nil {
		return MonthSummaryInput{}, err
	}

	return MonthSummaryInput{
		ID:               recordID,
		MonthStart:       monthStart,
		WorkMinutesTotal: item.WorkMinutesTotal,
		AdjustMinutesBal: item.AdjustMinutesBal,
		Version:          item.Version,
		UpdatedAt:        updatedAt,
	}, nil
}

func requireUUID(field, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !uuidRegex.MatchString(trimmed) {
		return "", invalidArgument(fmt.Sprintf("%s must be a valid UUID", field))
	}
	return strings.ToLower(trimmed), nil
}

func requirePayloadHash(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !payloadHashRegex.MatchString(trimmed) {
		return "", invalidArgument("payload_hash must be 64 lowercase hex characters")
	}
	return trimmed, nil
}

func parseDate(field, value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s is required", field))
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s must be in YYYY-MM-DD format", field))
	}
	return parsed, nil
}

func parseUTCTime(field, value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s is required", field))
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s must be RFC3339 UTC time", field))
	}
	if _, offset := parsed.Zone(); offset != 0 {
		return time.Time{}, invalidArgument(fmt.Sprintf("%s must use UTC timezone", field))
	}
	return parsed.UTC(), nil
}

func parseOptionalUTCTime(field string, value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := parseUTCTime(field, trimmed)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func isAllowed(values map[string]struct{}, value string) bool {
	_, ok := values[strings.TrimSpace(value)]
	return ok
}

func invalidArgument(message string) error {
	return apperrors.New(http.StatusBadRequest, invalidArgumentCode, message)
}

func validateSyncBusinessRules(input SyncCommitInput) error {
	if err := validatePunchTimeFields(input.PunchRecords); err != nil {
		return err
	}
	if err := validatePunchPairs(input.PunchRecords); err != nil {
		return err
	}
	if err := validateFullDayLeaveAutoPunch(input.PunchRecords, input.LeaveRecords); err != nil {
		return err
	}
	return nil
}

func validatePunchTimeFields(records []PunchRecordInput) error {
	for i, record := range records {
		if record.AtUTC.Second() != 0 || record.AtUTC.Nanosecond() != 0 {
			return rejectedBusinessRule(
				timePrecisionInvalid,
				fmt.Sprintf("punch_records[%d].at_utc must be minute precision", i),
			)
		}

		location, err := time.LoadLocation(record.TimezoneID)
		if err != nil {
			return invalidArgument(fmt.Sprintf("punch_records[%d].timezone_id is invalid", i))
		}

		localTime := record.AtUTC.In(location)
		if localTime.Format("2006-01-02") != record.LocalDate.Format("2006-01-02") {
			return rejectedBusinessRule(
				timeFieldsMismatch,
				fmt.Sprintf("punch_records[%d].local_date does not match at_utc and timezone_id", i),
			)
		}

		minuteOfDay := localTime.Hour()*60 + localTime.Minute()
		if minuteOfDay != record.MinuteOfDay {
			return rejectedBusinessRule(
				timeFieldsMismatch,
				fmt.Sprintf("punch_records[%d].minute_of_day does not match at_utc and timezone_id", i),
			)
		}
	}
	return nil
}

func validatePunchPairs(records []PunchRecordInput) error {
	type punchPair struct {
		start *time.Time
		end   *time.Time
	}

	byDate := make(map[string]punchPair)
	for _, record := range records {
		if record.DeletedAt != nil {
			continue
		}

		day := record.LocalDate.Format("2006-01-02")
		pair := byDate[day]
		if record.Type == "START" {
			t := record.AtUTC
			pair.start = &t
		} else if record.Type == "END" {
			t := record.AtUTC
			pair.end = &t
		}
		byDate[day] = pair
	}

	for day, pair := range byDate {
		if pair.end != nil && pair.start == nil {
			return rejectedBusinessRule(punchEndRequiresStart, fmt.Sprintf("END requires START on %s", day))
		}
		if pair.start != nil && pair.end != nil && !pair.end.After(*pair.start) {
			return rejectedBusinessRule(punchEndNotAfterStart, fmt.Sprintf("END must be later than START on %s", day))
		}
	}

	return nil
}

func validateFullDayLeaveAutoPunch(punchRecords []PunchRecordInput, leaveRecords []LeaveRecordInput) error {
	fullDayLeaves := make(map[string]struct{}, len(leaveRecords))
	for _, leave := range leaveRecords {
		if leave.DeletedAt != nil || leave.LeaveType != "FULL_DAY" {
			continue
		}
		fullDayLeaves[leave.LocalDate.Format("2006-01-02")] = struct{}{}
	}
	if len(fullDayLeaves) == 0 {
		return nil
	}

	for _, punch := range punchRecords {
		if punch.DeletedAt != nil || punch.Source != "AUTO" {
			continue
		}
		if _, exists := fullDayLeaves[punch.LocalDate.Format("2006-01-02")]; exists {
			return rejectedBusinessRule(autoPunchLeaveConflict, "AUTO punch conflicts with FULL_DAY leave")
		}
	}

	return nil
}

func rejectedBusinessRule(code, message string) error {
	return apperrors.New(http.StatusConflict, code, message)
}

func mapSyncCommitPersistenceError(err error, requestID string) (error, *syncCommitConflictResponse, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil, nil, false
	}

	if pgErr.Code == "23505" && pgErr.ConstraintName == "uq_sync_commits_user_sync" {
		return nil, &syncCommitConflictResponse{
			ErrorCode:  syncIDConflictCode,
			Message:    "same sync_id but different payload_hash",
			GateResult: gateResultRejected,
			GateReason: gateReasonSyncIDConflict,
			RequestID:  requestID,
		}, true
	}

	if pgErr.Code == "23514" {
		switch pgErr.ConstraintName {
		case "ck_punch_records_at_utc_minute_precision":
			return apperrors.New(http.StatusConflict, timePrecisionInvalid, "at_utc must be minute precision"), nil, true
		case "ck_punch_records_local_date_match_timezone", "ck_punch_records_minute_of_day_match_at_utc":
			return apperrors.New(http.StatusConflict, timeFieldsMismatch, "time fields mismatch"), nil, true
		}
	}

	if pgErr.Code == "P0001" {
		if mappedErr, ok := mapRuleErrorKeyToAPIError(extractErrorKey(pgErr.Message)); ok {
			return mappedErr, nil, true
		}
	}

	return nil, nil, false
}

func extractErrorKey(message string) string {
	matches := errorKeyRegex.FindStringSubmatch(message)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

type syncCommitTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

type syncCommitTxExecutor interface {
	Exec(ctx context.Context, query string, args ...any) error
}

type pgxSyncCommitTxExecutor struct {
	tx pgx.Tx
}

func (e pgxSyncCommitTxExecutor) Exec(ctx context.Context, query string, args ...any) error {
	_, err := e.tx.Exec(ctx, query, args...)
	return err
}

func persistSyncCommitTransaction(ctx context.Context, db HealthChecker, input SyncCommitInput, decision syncCommitGateDecision) error {
	// REJECTED and REPLAY_NOOP never create new durable records.
	if decision.Result == gateResultRejected || (decision.Result == gateResultNoop && decision.Reason == gateReasonReplayNoop) {
		return nil
	}

	txDB, ok := db.(syncCommitTxDB)
	if !ok {
		// Non-DB tests may provide health-only stubs.
		return nil
	}

	return txDB.WithTx(ctx, func(tx pgx.Tx) error {
		exec := pgxSyncCommitTxExecutor{tx: tx}

		if decision.Result == gateResultApplied {
			if err := writePunchRecords(ctx, exec, input); err != nil {
				return fmt.Errorf("write punch_records: %w", err)
			}
			if err := writeLeaveRecords(ctx, exec, input); err != nil {
				return fmt.Errorf("write leave_records: %w", err)
			}
			if err := writeDaySummaries(ctx, exec, input); err != nil {
				return fmt.Errorf("write day_summaries: %w", err)
			}
			if err := writeMonthSummaries(ctx, exec, input); err != nil {
				return fmt.Errorf("write month_summaries: %w", err)
			}
		}

		if err := writeSyncCommitRecord(ctx, exec, input, decision); err != nil {
			return fmt.Errorf("write sync_commits: %w", err)
		}
		return nil
	})
}

func writePunchRecords(ctx context.Context, exec syncCommitTxExecutor, input SyncCommitInput) error {
	const query = `
INSERT INTO punch_records (
	id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, deleted_at, version, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now()
)
ON CONFLICT (id) DO UPDATE
SET
	local_date = EXCLUDED.local_date,
	type = EXCLUDED.type,
	at_utc = EXCLUDED.at_utc,
	timezone_id = EXCLUDED.timezone_id,
	minute_of_day = EXCLUDED.minute_of_day,
	source = EXCLUDED.source,
	deleted_at = EXCLUDED.deleted_at,
	version = EXCLUDED.version,
	updated_at = now()
WHERE EXCLUDED.version > punch_records.version
`
	for _, record := range input.PunchRecords {
		if err := exec.Exec(ctx, query,
			record.ID,
			input.UserID,
			record.LocalDate.Format("2006-01-02"),
			record.Type,
			record.AtUTC,
			record.TimezoneID,
			record.MinuteOfDay,
			record.Source,
			record.DeletedAt,
			record.Version,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeLeaveRecords(ctx context.Context, exec syncCommitTxExecutor, input SyncCommitInput) error {
	const query = `
INSERT INTO leave_records (
	id, user_id, local_date, leave_type, deleted_at, version, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, now()
)
ON CONFLICT (id) DO UPDATE
SET
	local_date = EXCLUDED.local_date,
	leave_type = EXCLUDED.leave_type,
	deleted_at = EXCLUDED.deleted_at,
	version = EXCLUDED.version,
	updated_at = now()
WHERE EXCLUDED.version > leave_records.version
`
	for _, record := range input.LeaveRecords {
		if err := exec.Exec(ctx, query,
			record.ID,
			input.UserID,
			record.LocalDate.Format("2006-01-02"),
			record.LeaveType,
			record.DeletedAt,
			record.Version,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeDaySummaries(ctx context.Context, exec syncCommitTxExecutor, input SyncCommitInput) error {
	const query = `
INSERT INTO day_summaries (
	id, user_id, local_date, start_at_utc, end_at_utc, is_leave_day, leave_type, is_late, work_minutes, adjust_minutes, status, version, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
ON CONFLICT (id) DO UPDATE
SET
	local_date = EXCLUDED.local_date,
	start_at_utc = EXCLUDED.start_at_utc,
	end_at_utc = EXCLUDED.end_at_utc,
	is_leave_day = EXCLUDED.is_leave_day,
	leave_type = EXCLUDED.leave_type,
	is_late = EXCLUDED.is_late,
	work_minutes = EXCLUDED.work_minutes,
	adjust_minutes = EXCLUDED.adjust_minutes,
	status = EXCLUDED.status,
	version = EXCLUDED.version,
	updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.version > day_summaries.version
`
	for _, record := range input.DaySummaries {
		if err := exec.Exec(ctx, query,
			record.ID,
			input.UserID,
			record.LocalDate.Format("2006-01-02"),
			record.StartAtUTC,
			record.EndAtUTC,
			record.IsLeaveDay,
			record.LeaveType,
			record.IsLate,
			record.WorkMinutes,
			record.AdjustMins,
			record.Status,
			record.Version,
			record.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeMonthSummaries(ctx context.Context, exec syncCommitTxExecutor, input SyncCommitInput) error {
	const query = `
INSERT INTO month_summaries (
	id, user_id, month_start, work_minutes_total, adjust_minutes_balance, version, updated_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (id) DO UPDATE
SET
	month_start = EXCLUDED.month_start,
	work_minutes_total = EXCLUDED.work_minutes_total,
	adjust_minutes_balance = EXCLUDED.adjust_minutes_balance,
	version = EXCLUDED.version,
	updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.version > month_summaries.version
`
	for _, record := range input.MonthSummaries {
		if err := exec.Exec(ctx, query,
			record.ID,
			input.UserID,
			record.MonthStart.Format("2006-01-02"),
			record.WorkMinutesTotal,
			record.AdjustMinutesBal,
			record.Version,
			record.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeSyncCommitRecord(ctx context.Context, exec syncCommitTxExecutor, input SyncCommitInput, decision syncCommitGateDecision) error {
	const query = `
INSERT INTO sync_commits (
	user_id, device_id, writer_epoch, sync_id, payload_hash, status, created_at, applied_at
) VALUES (
	$1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (user_id, sync_id) DO NOTHING
`
	createdAt := decision.Record.CreatedAt.UTC()
	return exec.Exec(ctx, query,
		input.UserID,
		input.DeviceID,
		input.WriterEpoch,
		input.SyncID,
		input.PayloadHash,
		syncCommitStatusApplied,
		createdAt,
		createdAt,
	)
}
