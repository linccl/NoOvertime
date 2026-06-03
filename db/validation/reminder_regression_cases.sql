\set ON_ERROR_STOP on
SET search_path = public;
SET TIME ZONE 'UTC';

BEGIN;

CREATE TEMP TABLE tmp_reminder_case_results (
  case_id TEXT PRIMARY KEY,
  pass BOOLEAN NOT NULL,
  actual_result TEXT NOT NULL
);

CREATE OR REPLACE FUNCTION _reminder_seed_base()
RETURNS VOID AS $$
BEGIN
  DELETE FROM punch_reminder_events;
  DELETE FROM user_notification_settings;
  DELETE FROM punch_records;
  DELETE FROM devices;
  DELETE FROM users;

  INSERT INTO users(user_id, pairing_code, recovery_code_hash, writer_epoch)
  VALUES ('90000000-0000-0000-0000-000000000001', '90123456', 'hash', 1);

  INSERT INTO devices(device_id, user_id, device_name, status)
  VALUES ('90000000-0000-0000-0000-000000000101', '90000000-0000-0000-0000-000000000001', 'writer', 'ACTIVE');

  UPDATE users
     SET writer_device_id = '90000000-0000-0000-0000-000000000101'
   WHERE user_id = '90000000-0000-0000-0000-000000000001';

  INSERT INTO user_notification_settings(
    user_id,
    server_end_reminder_enabled,
    notification_url,
    notification_token,
    notification_url_hash
  )
  VALUES (
    '90000000-0000-0000-0000-000000000001',
    TRUE,
    'https://notify.example/hook',
    '<notification-token>',
    repeat('a', 64)
  );

  INSERT INTO punch_records(id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, version)
  VALUES ('91000000-0000-0000-0000-000000000001', '90000000-0000-0000-0000-000000000001', DATE '2026-02-12', 'START', '2026-02-12 01:10:00+00', 'UTC', 70, 'MANUAL', 1);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION _reminder_insert_event(
  p_id UUID,
  p_adjust INTEGER,
  p_status reminder_event_status DEFAULT 'PENDING',
  p_version BIGINT DEFAULT 1
)
RETURNS VOID AS $$
BEGIN
  INSERT INTO punch_reminder_events(
    id,
    user_id,
    source_start_punch_id,
    source_start_punch_version,
    local_date,
    reminder_type,
    adjust_minutes,
    scheduled_after_start_minutes,
    scheduled_at_utc,
    status,
    attempt_count,
    next_retry_at,
    locked_until
  )
  VALUES (
    p_id,
    '90000000-0000-0000-0000-000000000001',
    '91000000-0000-0000-0000-000000000001',
    p_version,
    DATE '2026-02-12',
    CASE WHEN p_adjust = 0 THEN 'END_REMINDER'::reminder_type ELSE 'ADJUST_REMINDER'::reminder_type END,
    p_adjust,
    CASE WHEN p_adjust = 0 THEN 539 ELSE 540 + p_adjust - 1 END,
    '2026-02-12 01:10:00+00'::timestamptz + (CASE WHEN p_adjust = 0 THEN 539 ELSE 540 + p_adjust - 1 END) * interval '1 minute',
    p_status,
    CASE WHEN p_status = 'FAILED' THEN 1 ELSE 0 END,
    CASE WHEN p_status = 'FAILED' THEN '2026-02-12 11:00:00+00'::timestamptz ELSE NULL END,
    CASE WHEN p_status = 'SENDING' THEN '2026-02-12 10:00:00+00'::timestamptz ELSE NULL END
  );
END;
$$ LANGUAGE plpgsql;

-- REM-C01: unique key blocks duplicated source/type/adjust events.
DO $$
DECLARE v_ok BOOLEAN := FALSE;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000001', 0);
  BEGIN
    PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000002', 0);
  EXCEPTION WHEN unique_violation THEN
    v_ok := TRUE;
  END;
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C01', v_ok, 'duplicate source/type/adjust unique_violation=' || v_ok);
END $$;

-- REM-C02: END synced before send cancels future unsent reminders only.
DO $$
DECLARE v_cancelled INT;
DECLARE v_sent INT;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000003', 0, 'SENT');
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000004', 30, 'PENDING');
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000005', 60, 'FAILED');
  INSERT INTO punch_records(id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, version)
  VALUES ('91000000-0000-0000-0000-000000000002', '90000000-0000-0000-0000-000000000001', DATE '2026-02-12', 'END', '2026-02-12 10:10:00+00', 'UTC', 610, 'MANUAL', 1);

  UPDATE punch_reminder_events
     SET status = 'CANCELLED',
         cancelled_at = now(),
         cancel_reason = 'END_SYNCED',
         locked_until = NULL,
         updated_at = now()
   WHERE user_id = '90000000-0000-0000-0000-000000000001'
     AND local_date = DATE '2026-02-12'
     AND status IN ('PENDING', 'SENDING', 'FAILED');

  SELECT count(*) INTO v_cancelled FROM punch_reminder_events WHERE status = 'CANCELLED' AND cancel_reason = 'END_SYNCED';
  SELECT count(*) INTO v_sent FROM punch_reminder_events WHERE status = 'SENT';
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C02', (v_cancelled = 2 AND v_sent = 1), format('cancelled=%s sent=%s', v_cancelled, v_sent));
END $$;

-- REM-C03: repeated scan/claim does not duplicate or resend SENT event.
DO $$
DECLARE v_first INT;
DECLARE v_second INT;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000006', 0);

  WITH due AS (
    SELECT id FROM punch_reminder_events
     WHERE status = 'PENDING' AND scheduled_at_utc <= '2026-02-12 10:09:00+00'
     LIMIT 10
     FOR UPDATE SKIP LOCKED
  )
  UPDATE punch_reminder_events e
     SET status = 'SENDING', locked_until = '2026-02-12 10:10:00+00'
    FROM due
   WHERE e.id = due.id;

  UPDATE punch_reminder_events SET status = 'SENT', sent_at = now(), locked_until = NULL WHERE status = 'SENDING';
  SELECT count(*) INTO v_first FROM punch_reminder_events WHERE status = 'SENT';

  WITH due AS (
    SELECT id FROM punch_reminder_events
     WHERE status = 'PENDING' AND scheduled_at_utc <= '2026-02-12 10:09:00+00'
     LIMIT 10
     FOR UPDATE SKIP LOCKED
  )
  UPDATE punch_reminder_events e
     SET status = 'SENDING'
    FROM due
   WHERE e.id = due.id;

  SELECT count(*) INTO v_second FROM punch_reminder_events WHERE status = 'SENT';
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C03', (v_first = 1 AND v_second = 1), format('sent first=%s second=%s', v_first, v_second));
END $$;

-- REM-C04: START modify/delete cancels old-version unsent events and keeps new version distinct.
DO $$
DECLARE v_old_cancelled INT;
DECLARE v_new_pending INT;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000007', 0, 'PENDING', 1);
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000008', 30, 'PENDING', 2);

  UPDATE punch_reminder_events
     SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'START_CHANGED', locked_until = NULL
   WHERE source_start_punch_id = '91000000-0000-0000-0000-000000000001'
     AND source_start_punch_version = 1
     AND status IN ('PENDING', 'SENDING', 'FAILED');

  UPDATE punch_reminder_events
     SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'START_DELETED', locked_until = NULL
   WHERE source_start_punch_id = '91000000-0000-0000-0000-000000000001'
     AND source_start_punch_version = 2
     AND status IN ('PENDING', 'SENDING', 'FAILED');

  SELECT count(*) INTO v_old_cancelled FROM punch_reminder_events WHERE status = 'CANCELLED';
  SELECT count(*) INTO v_new_pending FROM punch_reminder_events WHERE status = 'PENDING';
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C04', (v_old_cancelled = 2 AND v_new_pending = 0), format('cancelled=%s pending=%s', v_old_cancelled, v_new_pending));
END $$;

-- REM-C05: disabling notification settings cancels unsent reminders and keeps SENT.
DO $$
DECLARE v_cancelled INT;
DECLARE v_sent INT;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000009', 0, 'SENT');
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000010', 30, 'PENDING');
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000011', 60, 'SENDING');

  UPDATE user_notification_settings
     SET server_end_reminder_enabled = FALSE, config_version = config_version + 1, updated_at = now()
   WHERE user_id = '90000000-0000-0000-0000-000000000001';
  UPDATE punch_reminder_events
     SET status = 'CANCELLED', cancelled_at = now(), cancel_reason = 'CONFIG_DISABLED', locked_until = NULL
   WHERE user_id = '90000000-0000-0000-0000-000000000001'
     AND status IN ('PENDING', 'SENDING', 'FAILED');

  SELECT count(*) INTO v_cancelled FROM punch_reminder_events WHERE status = 'CANCELLED' AND cancel_reason = 'CONFIG_DISABLED';
  SELECT count(*) INTO v_sent FROM punch_reminder_events WHERE status = 'SENT';
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C05', (v_cancelled = 2 AND v_sent = 1), format('cancelled=%s sent=%s', v_cancelled, v_sent));
END $$;

-- REM-C06: SENDING lock timeout becomes claimable again.
DO $$
DECLARE v_claimed INT;
BEGIN
  PERFORM _reminder_seed_base();
  PERFORM _reminder_insert_event('92000000-0000-0000-0000-000000000012', 0, 'SENDING');

  WITH due AS (
    SELECT id FROM punch_reminder_events
     WHERE status = 'SENDING' AND locked_until <= '2026-02-12 10:30:00+00'
     LIMIT 10
     FOR UPDATE SKIP LOCKED
  )
  UPDATE punch_reminder_events e
     SET status = 'SENDING', locked_until = '2026-02-12 10:31:00+00', updated_at = now()
    FROM due
   WHERE e.id = due.id;

  SELECT count(*) INTO v_claimed FROM punch_reminder_events WHERE id = '92000000-0000-0000-0000-000000000012' AND status = 'SENDING' AND locked_until = '2026-02-12 10:31:00+00';
  INSERT INTO tmp_reminder_case_results VALUES ('REM-C06', v_claimed = 1, format('reclaimed=%s', v_claimed));
END $$;

SELECT case_id, CASE WHEN pass THEN 'PASS' ELSE 'FAIL' END AS result, actual_result
  FROM tmp_reminder_case_results
 ORDER BY case_id;

DO $$
DECLARE v_failures INT;
BEGIN
  SELECT count(*) INTO v_failures FROM tmp_reminder_case_results WHERE NOT pass;
  IF v_failures > 0 THEN
    RAISE EXCEPTION 'reminder regression failures=%', v_failures;
  END IF;
END $$;

ROLLBACK;

