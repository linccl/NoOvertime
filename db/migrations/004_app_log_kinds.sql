ALTER TABLE app_log_uploads
  ADD COLUMN IF NOT EXISTS log_kind TEXT NOT NULL DEFAULT 'all';

ALTER TABLE app_log_uploads
  DROP CONSTRAINT IF EXISTS uq_app_log_uploads_user_device_date;

ALTER TABLE app_log_uploads
  DROP CONSTRAINT IF EXISTS ck_app_log_uploads_log_kind;

ALTER TABLE app_log_uploads
  ADD CONSTRAINT ck_app_log_uploads_log_kind
  CHECK (log_kind IN ('all', 'error', 'info'));

ALTER TABLE app_log_uploads
  DROP CONSTRAINT IF EXISTS uq_app_log_uploads_user_device_date_kind;

ALTER TABLE app_log_uploads
  ADD CONSTRAINT uq_app_log_uploads_user_device_date_kind
  UNIQUE (user_id, device_id, log_date, log_kind);

DROP INDEX IF EXISTS idx_app_log_uploads_device_date;

CREATE INDEX IF NOT EXISTS idx_app_log_uploads_device_date_kind
  ON app_log_uploads (device_id, log_date, log_kind);
