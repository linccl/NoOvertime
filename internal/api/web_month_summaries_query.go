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

type webMonthSummariesQueryRequest struct {
	Year *int `json:"year"`
}

type webMonthSummariesQueryInput struct {
	Year int
}

type webMonthSummary struct {
	ID                   string `json:"id"`
	MonthStart           string `json:"month_start"`
	WorkMinutesTotal     int    `json:"work_minutes_total"`
	AdjustMinutesBalance int    `json:"adjust_minutes_balance"`
	Version              int64  `json:"version"`
	UpdatedAt            string `json:"updated_at"`
}

type webMonthSummariesQueryResponse struct {
	MonthSummaries []webMonthSummary `json:"month_summaries"`
}

type readOnlySummariesTxDB interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}

func (s *Server) webMonthSummariesQueryHandler(w http.ResponseWriter, r *http.Request) error {
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

	input, err := parseWebMonthSummariesQueryBody(io.LimitReader(r.Body, migrationRequestBodyMaxBytes))
	if err != nil {
		return err
	}

	fingerprint := migrationClientFingerprint(r, auth.DeviceID)
	if err := s.checkWebReadQueryRateLimit(auth.UserID, fingerprint); err != nil {
		return err
	}

	response, err := queryWebMonthSummaries(r.Context(), s.db, auth.UserID, input)
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

func parseWebMonthSummariesQueryBody(reader io.Reader) (webMonthSummariesQueryInput, error) {
	var body webMonthSummariesQueryRequest
	if err := decodeStrictMigrationJSON(reader, &body); err != nil {
		return webMonthSummariesQueryInput{}, err
	}

	if body.Year == nil {
		return webMonthSummariesQueryInput{}, invalidArgument("year is required")
	}
	year := *body.Year
	if year < 2000 || year > 2100 {
		return webMonthSummariesQueryInput{}, invalidArgument("year must be between 2000 and 2100")
	}

	return webMonthSummariesQueryInput{
		Year: year,
	}, nil
}

func queryWebMonthSummaries(
	ctx context.Context,
	db HealthChecker,
	userID string,
	input webMonthSummariesQueryInput,
) (webMonthSummariesQueryResponse, error) {
	txDB, ok := db.(readOnlySummariesTxDB)
	if !ok {
		return webMonthSummariesQueryResponse{}, errors.New("database transaction is not available")
	}

	response := webMonthSummariesQueryResponse{
		MonthSummaries: []webMonthSummary{},
	}

	start := time.Date(input.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(input.Year+1, 1, 1, 0, 0, 0, 0, time.UTC)

	err := txDB.WithTx(ctx, func(tx pgx.Tx) error {
		const query = `
SELECT id,
       month_start,
       work_minutes_total,
       adjust_minutes_balance,
       version,
       updated_at
  FROM month_summaries
 WHERE user_id = $1::uuid
   AND month_start >= $2::date
   AND month_start < $3::date
 ORDER BY month_start ASC
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
				id                   string
				monthStart           time.Time
				workMinutesTotal     int
				adjustMinutesBalance int
				version              int64
				updatedAt            time.Time
			)
			if err := rows.Scan(
				&id,
				&monthStart,
				&workMinutesTotal,
				&adjustMinutesBalance,
				&version,
				&updatedAt,
			); err != nil {
				return err
			}

			response.MonthSummaries = append(response.MonthSummaries, webMonthSummary{
				ID:                   id,
				MonthStart:           monthStart.Format("2006-01-02"),
				WorkMinutesTotal:     workMinutesTotal,
				AdjustMinutesBalance: adjustMinutesBalance,
				Version:              version,
				UpdatedAt:            updatedAt.UTC().Format(time.RFC3339),
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return webMonthSummariesQueryResponse{}, err
	}

	return response, nil
}
