package reminders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type PGStore struct {
	db TxRunner
}

func NewPGStore(db TxRunner) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) ClaimDue(ctx context.Context, now time.Time, limit, maxRetry int, lockFor time.Duration) ([]Event, error) {
	var events []Event
	lockForSeconds := int(lockFor.Seconds())
	if lockForSeconds <= 0 {
		lockForSeconds = 1
	}
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		const query = `
WITH due AS (
  SELECT id
    FROM punch_reminder_events
   WHERE (
          status = 'PENDING'
          AND scheduled_at_utc <= $1
        )
      OR (
          status = 'FAILED'
          AND next_retry_at <= $1
          AND attempt_count < $3
        )
      OR (
          status = 'SENDING'
          AND locked_until <= $1
        )
   ORDER BY scheduled_at_utc, created_at, id
   LIMIT $2
   FOR UPDATE SKIP LOCKED
)
UPDATE punch_reminder_events e
   SET status = 'SENDING',
       locked_until = $1 + make_interval(secs => $4),
       updated_at = $1
  FROM due
 WHERE e.id = due.id
RETURNING e.id::text,
          e.user_id::text,
          e.local_date::text,
          e.reminder_type::text,
          e.adjust_minutes,
          e.scheduled_after_start_minutes,
          e.attempt_count,
          e.locked_until
`
		rows, err := tx.Query(ctx, query, now, limit, maxRetry, lockForSeconds)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var event Event
			if err := rows.Scan(
				&event.ID,
				&event.UserID,
				&event.LocalDate,
				&event.ReminderType,
				&event.AdjustMinutes,
				&event.ScheduledAfterStartMinutes,
				&event.AttemptCount,
				&event.ClaimLockedUntil,
			); err != nil {
				return err
			}
			event.Message = BuildMessage(event.ReminderType, event.AdjustMinutes)
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}

func (s *PGStore) LoadSendTarget(ctx context.Context, event Event) (SendTarget, error) {
	var target SendTarget
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		const query = `
SELECT EXISTS (
         SELECT 1
           FROM punch_records
          WHERE user_id = $1::uuid
            AND local_date = $2::date
            AND type = 'END'
            AND deleted_at IS NULL
       ) AS has_end,
       COALESCE(ns.server_end_reminder_enabled, FALSE) AS enabled,
       ns.notification_url,
       ns.notification_token
  FROM (SELECT $1::uuid AS user_id) u
  LEFT JOIN user_notification_settings ns ON ns.user_id = u.user_id
`
		var url, token *string
		if err := tx.QueryRow(ctx, query, event.UserID, event.LocalDate).Scan(
			&target.HasEnd,
			&target.Enabled,
			&url,
			&token,
		); err != nil {
			return err
		}
		if url != nil && token != nil {
			target.Configured = true
			target.URL = *url
			target.Token = *token
		}
		return nil
	})
	return target, err
}

func (s *PGStore) MarkSent(ctx context.Context, event Event, now time.Time) error {
	return s.exec(ctx, `
UPDATE punch_reminder_events
   SET status = 'SENT',
       sent_at = $3,
       locked_until = NULL,
       next_retry_at = NULL,
       last_error_code = NULL,
       last_error_message = NULL,
       updated_at = $3
 WHERE id = $1::uuid
   AND status = 'SENDING'
   AND locked_until = $2
`, event.ID, event.ClaimLockedUntil, now)
}

func (s *PGStore) MarkFailed(ctx context.Context, event Event, attemptCount, maxRetry int, nextRetryAt time.Time, code, message string, now time.Time) error {
	status := StatusFailed
	var retryAt any = nextRetryAt
	if attemptCount >= maxRetry {
		retryAt = nil
	}
	return s.exec(ctx, `
UPDATE punch_reminder_events
   SET status = $3::reminder_event_status,
       attempt_count = $4,
       next_retry_at = $5,
       locked_until = NULL,
       last_error_code = $6,
       last_error_message = $7,
       updated_at = $8
 WHERE id = $1::uuid
   AND status = 'SENDING'
   AND locked_until = $2
`, event.ID, event.ClaimLockedUntil, status, attemptCount, retryAt, code, message, now)
}

func (s *PGStore) MarkCancelled(ctx context.Context, event Event, reason string, now time.Time) error {
	return s.markCancelledStatus(ctx, event, StatusCancelled, reason, now)
}

func (s *PGStore) MarkSkipped(ctx context.Context, event Event, reason string, now time.Time) error {
	return s.markCancelledStatus(ctx, event, StatusSkipped, reason, now)
}

func (s *PGStore) markCancelledStatus(ctx context.Context, event Event, status, reason string, now time.Time) error {
	return s.exec(ctx, `
UPDATE punch_reminder_events
   SET status = $3::reminder_event_status,
       cancelled_at = $4,
       cancel_reason = $5,
       locked_until = NULL,
       next_retry_at = NULL,
       updated_at = $4
 WHERE id = $1::uuid
   AND status = 'SENDING'
   AND locked_until = $2
`, event.ID, event.ClaimLockedUntil, status, now, reason)
}

func (s *PGStore) exec(ctx context.Context, query string, args ...any) error {
	if s.db == nil {
		return errors.New("reminder store db is nil")
	}
	return s.db.WithTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 && strings.Contains(query, "status = 'SENDING'") {
			return ErrClaimExpired
		}
		if tag.RowsAffected() > 1 {
			return fmt.Errorf("unexpected reminder event rows affected: %d", tag.RowsAffected())
		}
		return nil
	})
}
