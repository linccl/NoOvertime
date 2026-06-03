package api

import (
	"context"
	"errors"
	"time"

	"noovertime/internal/reminders"

	"github.com/jackc/pgx/v5"
)

const (
	reminderCancelReasonStartChanged = "START_CHANGED"
	reminderCancelReasonStartDeleted = "START_DELETED"
	reminderCancelReasonEndSynced    = "END_SYNCED"
)

type punchReminderSnapshot struct {
	Exists    bool
	Type      string
	LocalDate time.Time
	AtUTC     time.Time
	Deleted   bool
	Version   int64
}

func loadPunchReminderSnapshots(ctx context.Context, tx pgx.Tx, input SyncCommitInput) (map[string]punchReminderSnapshot, error) {
	snapshots := make(map[string]punchReminderSnapshot, len(input.PunchRecords))
	for _, record := range input.PunchRecords {
		snapshot, err := loadPunchReminderSnapshot(ctx, tx, input.UserID, record.ID)
		if err != nil {
			return nil, err
		}
		snapshots[record.ID] = snapshot
	}
	return snapshots, nil
}

func loadPunchReminderSnapshot(ctx context.Context, tx pgx.Tx, userID, punchID string) (punchReminderSnapshot, error) {
	const query = `
SELECT type::text,
       local_date,
       at_utc,
       deleted_at IS NOT NULL,
       version
  FROM punch_records
 WHERE user_id = $1::uuid
   AND id = $2::uuid
`

	var snapshot punchReminderSnapshot
	if err := tx.QueryRow(ctx, query, userID, punchID).Scan(
		&snapshot.Type,
		&snapshot.LocalDate,
		&snapshot.AtUTC,
		&snapshot.Deleted,
		&snapshot.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return punchReminderSnapshot{}, nil
		}
		return punchReminderSnapshot{}, err
	}
	snapshot.Exists = true
	return snapshot, nil
}

func maintainPunchReminderEvents(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	records []PunchRecordInput,
	snapshots map[string]punchReminderSnapshot,
	now time.Time,
) error {
	for _, record := range records {
		snapshot := snapshots[record.ID]
		if !isPunchVersionApplied(record, snapshot) {
			continue
		}
		if record.Type == "START" && record.DeletedAt != nil {
			cancelVersion := record.Version
			if snapshot.Exists && snapshot.Type == "START" {
				cancelVersion = snapshot.Version
			}
			if err := cancelStartReminderEvents(ctx, tx, userID, record.ID, cancelVersion, reminderCancelReasonStartDeleted); err != nil {
				return err
			}
			continue
		}

		if snapshot.Exists && snapshot.Type == "START" && snapshot.Version != record.Version {
			if err := cancelStartReminderEvents(ctx, tx, userID, record.ID, snapshot.Version, reminderCancelReasonStartChanged); err != nil {
				return err
			}
		}

		if record.Type == "START" {
			if err := rebuildStartReminderEvents(ctx, tx, userID, record, now, false); err != nil {
				return err
			}
			continue
		}

		if record.Type == "END" {
			if record.DeletedAt != nil {
				if err := restoreFutureReminderEventsForActiveStart(ctx, tx, userID, record.LocalDate, now); err != nil {
					return err
				}
				continue
			}
			if err := cancelReminderEventsForEnd(ctx, tx, userID, record.LocalDate); err != nil {
				return err
			}
		}
	}
	return nil
}

func isPunchVersionApplied(record PunchRecordInput, snapshot punchReminderSnapshot) bool {
	return !snapshot.Exists || record.Version > snapshot.Version
}

func rebuildStartReminderEvents(ctx context.Context, tx pgx.Tx, userID string, record PunchRecordInput, now time.Time, futureOnly bool) error {
	hasEnd, err := hasActiveEndForReminder(ctx, tx, userID, record.LocalDate)
	if err != nil {
		return err
	}
	if hasEnd {
		return nil
	}

	items := reminders.BuildSchedule(reminders.ScheduleRequest{
		UserID:            userID,
		StartPunchID:      record.ID,
		StartPunchVersion: record.Version,
		LocalDate:         record.LocalDate.Format("2006-01-02"),
		StartAtUTC:        record.AtUTC,
	})
	for _, item := range items {
		if futureOnly && !item.ScheduledAtUTC.After(now) {
			continue
		}
		if err := insertReminderEvent(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func insertReminderEvent(ctx context.Context, tx pgx.Tx, item reminders.ScheduleItem) error {
	const query = `
INSERT INTO punch_reminder_events (
  user_id,
  source_start_punch_id,
  source_start_punch_version,
  local_date,
  reminder_type,
  adjust_minutes,
  scheduled_after_start_minutes,
  scheduled_at_utc,
  status,
  created_at,
  updated_at
)
VALUES (
  $1::uuid,
  $2::uuid,
  $3,
  $4,
  $5,
  $6,
  $7,
  $8,
  'PENDING',
  now(),
  now()
)
ON CONFLICT (
  user_id,
  source_start_punch_id,
  source_start_punch_version,
  reminder_type,
  adjust_minutes
) DO UPDATE
   SET status = 'PENDING',
       cancelled_at = NULL,
       cancel_reason = NULL,
       locked_until = NULL,
       updated_at = now()
 WHERE punch_reminder_events.status = 'CANCELLED'
   AND punch_reminder_events.cancel_reason = $9
`
	_, err := tx.Exec(
		ctx,
		query,
		item.UserID,
		item.SourceStartPunchID,
		item.SourceStartPunchVersion,
		item.LocalDate,
		item.ReminderType,
		item.AdjustMinutes,
		item.ScheduledAfterStartMinutes,
		item.ScheduledAtUTC,
		reminderCancelReasonEndSynced,
	)
	return err
}

func cancelStartReminderEvents(ctx context.Context, tx pgx.Tx, userID, startPunchID string, startVersion int64, reason string) error {
	const query = `
UPDATE punch_reminder_events
   SET status = 'CANCELLED',
       cancelled_at = now(),
       cancel_reason = $4,
       locked_until = NULL,
       updated_at = now()
 WHERE user_id = $1::uuid
   AND source_start_punch_id = $2::uuid
   AND source_start_punch_version = $3
   AND status IN ('PENDING', 'SENDING', 'FAILED')
`
	_, err := tx.Exec(ctx, query, userID, startPunchID, startVersion, reason)
	return err
}

func cancelReminderEventsForEnd(ctx context.Context, tx pgx.Tx, userID string, localDate time.Time) error {
	const query = `
UPDATE punch_reminder_events
   SET status = 'CANCELLED',
       cancelled_at = now(),
       cancel_reason = $3,
       locked_until = NULL,
       updated_at = now()
 WHERE user_id = $1::uuid
   AND local_date = $2
   AND status IN ('PENDING', 'SENDING', 'FAILED')
`
	_, err := tx.Exec(ctx, query, userID, localDate.Format("2006-01-02"), reminderCancelReasonEndSynced)
	return err
}

func restoreFutureReminderEventsForActiveStart(ctx context.Context, tx pgx.Tx, userID string, localDate time.Time, now time.Time) error {
	start, ok, err := loadActiveStartForReminder(ctx, tx, userID, localDate)
	if err != nil || !ok {
		return err
	}
	return rebuildStartReminderEvents(ctx, tx, userID, start, now, true)
}

func loadActiveStartForReminder(ctx context.Context, tx pgx.Tx, userID string, localDate time.Time) (PunchRecordInput, bool, error) {
	const query = `
SELECT id::text,
       local_date,
       at_utc,
       timezone_id,
       minute_of_day,
       source::text,
       version
  FROM punch_records
 WHERE user_id = $1::uuid
   AND local_date = $2
   AND type = 'START'
   AND deleted_at IS NULL
 LIMIT 1
`

	var record PunchRecordInput
	if err := tx.QueryRow(ctx, query, userID, localDate.Format("2006-01-02")).Scan(
		&record.ID,
		&record.LocalDate,
		&record.AtUTC,
		&record.TimezoneID,
		&record.MinuteOfDay,
		&record.Source,
		&record.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PunchRecordInput{}, false, nil
		}
		return PunchRecordInput{}, false, err
	}
	record.Type = "START"
	return record, true, nil
}

func hasActiveEndForReminder(ctx context.Context, tx pgx.Tx, userID string, localDate time.Time) (bool, error) {
	const query = `
SELECT 1
  FROM punch_records
 WHERE user_id = $1::uuid
   AND local_date = $2
   AND type = 'END'
   AND deleted_at IS NULL
 LIMIT 1
`
	var marker int
	if err := tx.QueryRow(ctx, query, userID, localDate.Format("2006-01-02")).Scan(&marker); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
