BEGIN;

CREATE TABLE IF NOT EXISTS punch_photo_uploads (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  device_id UUID NOT NULL,
  punch_record_id UUID NOT NULL,
  local_date DATE NOT NULL,
  punch_type punch_type NOT NULL,
  object_key TEXT NOT NULL,
  remote_url TEXT NOT NULL,
  content_type TEXT NOT NULL,
  file_size_bytes BIGINT NOT NULL,
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_punch_photo_uploads PRIMARY KEY (id),
  CONSTRAINT fk_punch_photo_uploads_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT fk_punch_photo_uploads_device FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, device_id) ON DELETE CASCADE,
  CONSTRAINT uq_punch_photo_uploads_user_punch UNIQUE (user_id, punch_record_id),
  CONSTRAINT uq_punch_photo_uploads_object_key UNIQUE (object_key),
  CONSTRAINT ck_punch_photo_uploads_file_size_non_negative CHECK (file_size_bytes >= 0),
  CONSTRAINT ck_punch_photo_uploads_expiry_after_upload CHECK (expires_at > uploaded_at)
);

CREATE INDEX IF NOT EXISTS idx_punch_photo_uploads_expiry_active
  ON punch_photo_uploads (expires_at)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_punch_photo_uploads_device_date
  ON punch_photo_uploads (device_id, local_date);

CREATE TABLE IF NOT EXISTS app_log_uploads (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  device_id UUID NOT NULL,
  log_date DATE NOT NULL,
  object_key TEXT NOT NULL,
  remote_url TEXT NOT NULL,
  content_type TEXT NOT NULL,
  file_size_bytes BIGINT NOT NULL,
  uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_app_log_uploads PRIMARY KEY (id),
  CONSTRAINT fk_app_log_uploads_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT fk_app_log_uploads_device FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, device_id) ON DELETE CASCADE,
  CONSTRAINT uq_app_log_uploads_user_device_date UNIQUE (user_id, device_id, log_date),
  CONSTRAINT uq_app_log_uploads_object_key UNIQUE (object_key),
  CONSTRAINT ck_app_log_uploads_file_size_non_negative CHECK (file_size_bytes >= 0),
  CONSTRAINT ck_app_log_uploads_expiry_after_upload CHECK (expires_at > uploaded_at)
);

CREATE INDEX IF NOT EXISTS idx_app_log_uploads_expiry_active
  ON app_log_uploads (expires_at)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_app_log_uploads_device_date
  ON app_log_uploads (device_id, log_date);

COMMIT;
