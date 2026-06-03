-- NoOvertime reminder infrastructure.
-- Execute with: psql -X -v ON_ERROR_STOP=1 -f db/migrations/006_reminders.sql <database>
\set ON_ERROR_STOP on

BEGIN;

CREATE TYPE reminder_type AS ENUM ('END_REMINDER', 'ADJUST_REMINDER');
CREATE TYPE reminder_event_status AS ENUM (
  'PENDING',
  'SENDING',
  'FAILED',
  'SENT',
  'CANCELLED',
  'SKIPPED'
);

CREATE TABLE user_notification_settings (
  user_id UUID NOT NULL,
  server_end_reminder_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  notification_url TEXT NOT NULL,
  notification_token TEXT NOT NULL,
  notification_url_hash TEXT NOT NULL,
  config_version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_user_notification_settings PRIMARY KEY (user_id),
  CONSTRAINT fk_user_notification_settings_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT ck_user_notification_settings_url_non_empty CHECK (length(trim(notification_url)) > 0),
  CONSTRAINT ck_user_notification_settings_url_len CHECK (length(notification_url) <= 2048),
  CONSTRAINT ck_user_notification_settings_token_non_empty CHECK (length(trim(notification_token)) > 0),
  CONSTRAINT ck_user_notification_settings_token_len CHECK (length(notification_token) <= 4096),
  CONSTRAINT ck_user_notification_settings_hash_format CHECK (notification_url_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT ck_user_notification_settings_config_version_positive CHECK (config_version > 0)
);

CREATE TABLE punch_reminder_events (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  source_start_punch_id UUID NOT NULL,
  source_start_punch_version BIGINT NOT NULL,
  local_date DATE NOT NULL,
  reminder_type reminder_type NOT NULL,
  adjust_minutes INTEGER NOT NULL,
  scheduled_after_start_minutes INTEGER NOT NULL,
  scheduled_at_utc TIMESTAMPTZ NOT NULL,
  status reminder_event_status NOT NULL DEFAULT 'PENDING',
  attempt_count INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  locked_until TIMESTAMPTZ,
  sent_at TIMESTAMPTZ,
  cancelled_at TIMESTAMPTZ,
  cancel_reason TEXT,
  last_error_code TEXT,
  last_error_message TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_punch_reminder_events PRIMARY KEY (id),
  CONSTRAINT fk_punch_reminder_events_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT fk_punch_reminder_events_start FOREIGN KEY (source_start_punch_id) REFERENCES punch_records(id) ON DELETE CASCADE,
  CONSTRAINT ck_punch_reminder_events_version_positive CHECK (source_start_punch_version > 0),
  CONSTRAINT ck_punch_reminder_events_attempt_count_non_negative CHECK (attempt_count >= 0),
  CONSTRAINT ck_punch_reminder_events_last_error_message_len CHECK (
    last_error_message IS NULL OR length(last_error_message) <= 500
  ),
  CONSTRAINT ck_punch_reminder_events_terminal_timestamps CHECK (
    (status = 'SENT' AND sent_at IS NOT NULL)
    OR (status <> 'SENT' AND sent_at IS NULL)
  ),
  CONSTRAINT ck_punch_reminder_events_cancelled_timestamps CHECK (
    (status IN ('CANCELLED', 'SKIPPED') AND cancelled_at IS NOT NULL)
    OR (status NOT IN ('CANCELLED', 'SKIPPED') AND cancelled_at IS NULL)
  ),
  CONSTRAINT ck_punch_reminder_events_type_schedule CHECK (
    (
      reminder_type = 'END_REMINDER'
      AND adjust_minutes = 0
      AND scheduled_after_start_minutes = 539
    )
    OR (
      reminder_type = 'ADJUST_REMINDER'
      AND adjust_minutes BETWEEN 30 AND 300
      AND adjust_minutes % 30 = 0
      AND scheduled_after_start_minutes = 540 + adjust_minutes - 1
    )
  )
);

CREATE UNIQUE INDEX uk_punch_reminder_events_source_type_adjust
ON punch_reminder_events(
  user_id,
  source_start_punch_id,
  source_start_punch_version,
  reminder_type,
  adjust_minutes
);

CREATE INDEX idx_punch_reminder_events_due
ON punch_reminder_events(status, scheduled_at_utc, next_retry_at, locked_until);

CREATE INDEX idx_punch_reminder_events_user_date
ON punch_reminder_events(user_id, local_date);

CREATE INDEX idx_punch_reminder_events_start_version
ON punch_reminder_events(source_start_punch_id, source_start_punch_version);

COMMIT;
