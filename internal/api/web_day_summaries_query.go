package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	apperrors "noovertime/internal/errors"

	"github.com/jackc/pgx/v5"
)

type webDaySummariesQueryRequest struct {
	MonthStart string `json:"month_start"`
}

type webDaySummariesQueryInput struct {
	MonthStart time.Time
}

type webDaySummary struct {
	ID            string  `json:"id"`
	LocalDate     string  `json:"local_date"`
	StartAtUTC    *string `json:"start_at_utc"`
	EndAtUTC      *string `json:"end_at_utc"`
	IsLeaveDay    bool    `json:"is_leave_day"`
	LeaveType     *string `json:"leave_type"`
	IsLate        *bool   `json:"is_late"`
	WorkMinutes   *int    `json:"work_minutes"`
	AdjustMinutes *int    `json:"adjust_minutes"`
	Status        string  `json:"status"`
	Version       int64   `json:"version"`
	UpdatedAt     string  `json:"updated_at"`
}

type webDaySummariesQueryResponse struct {
	DaySummaries []webDaySummary `json:"day_summaries"`
}

func (s *Server) webDaySummariesQueryHandler(w http.ResponseWriter, r *http.Request) error {
	if err := ensurePostMethod(r); err != nil {
		return err
	}

	header, err := parseMobileTokenHeaders(r)
	if err != nil {
		return err
	}
	auth, err := resolveMobileAuthContext(r.Context(), s.db, header, true)
	if err != nil {
		return err
	}

	input, err := parseWebDaySummariesQueryBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkWebReadQueryRateLimit(auth.UserID, fingerprint); err != nil {
		return err
	}

	response, err := queryWebDaySummaries(r.Context(), s.db, auth.UserID, input)
	if err != nil {
		var apiErr apperrors.APIError
		if errors.As(err, &apiErr) {
			return apiErr
		}
		return apperrors.New(http.StatusInternalServerError, internalErrorCode, internalErrorMessage)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

func parseWebDaySummariesQueryBody(reader io.Reader) (webDaySummariesQueryInput, error) {
	var body webDaySummariesQueryRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return webDaySummariesQueryInput{}, err
	}

	monthStart, err := parseDate("month_start", body.MonthStart)
	if err != nil {
		return webDaySummariesQueryInput{}, err
	}
	if monthStart.Day() != 1 {
		return webDaySummariesQueryInput{}, invalidArgument("month_start must be first day of month")
	}

	return webDaySummariesQueryInput{
		MonthStart: monthStart,
	}, nil
}

func formatOptionalRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	value := t.UTC().Format(time.RFC3339)
	return &value
}

func queryWebDaySummaries(
	ctx context.Context,
	db HealthChecker,
	userID string,
	input webDaySummariesQueryInput,
) (webDaySummariesQueryResponse, error) {
	txDB, ok := db.(readOnlySummariesTxDB)
	if !ok {
		return webDaySummariesQueryResponse{}, errors.New("database transaction is not available")
	}

	response := webDaySummariesQueryResponse{
		DaySummaries: []webDaySummary{},
	}

	start := input.MonthStart
	end := input.MonthStart.AddDate(0, 1, 0)

	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		const query = `
SELECT id,
       local_date,
       start_at_utc,
       end_at_utc,
       is_leave_day,
       leave_type,
       is_late,
       work_minutes,
       adjust_minutes,
       status,
       version,
       updated_at
  FROM day_summaries
 WHERE user_id = $1::uuid
   AND local_date >= $2::date
   AND local_date < $3::date
 ORDER BY local_date ASC
`
		rows, err := tx.Query(
			ctx,
			query,
			userID,
			start.Format("2006-01-02"),
			end.Format("2006-01-02"),
		)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var (
				id          string
				localDate   time.Time
				startAtUTC  *time.Time
				endAtUTC    *time.Time
				isLeaveDay  bool
				leaveType   *string
				isLate      *bool
				workMinutes *int
				adjustMins  *int
				status      string
				version     int64
				updatedAt   time.Time
			)
			if err := rows.Scan(
				&id,
				&localDate,
				&startAtUTC,
				&endAtUTC,
				&isLeaveDay,
				&leaveType,
				&isLate,
				&workMinutes,
				&adjustMins,
				&status,
				&version,
				&updatedAt,
			); err != nil {
				return err
			}

			response.DaySummaries = append(response.DaySummaries, webDaySummary{
				ID:            id,
				LocalDate:     localDate.Format("2006-01-02"),
				StartAtUTC:    formatOptionalRFC3339(startAtUTC),
				EndAtUTC:      formatOptionalRFC3339(endAtUTC),
				IsLeaveDay:    isLeaveDay,
				LeaveType:     leaveType,
				IsLate:        isLate,
				WorkMinutes:   workMinutes,
				AdjustMinutes: adjustMins,
				Status:        status,
				Version:       version,
				UpdatedAt:     updatedAt.UTC().Format(time.RFC3339),
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return webDaySummariesQueryResponse{}, err
	}

	return response, nil
}
