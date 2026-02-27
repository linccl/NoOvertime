-- NoOvertime initial migration (Draft v0.7)
-- Execute with: psql -X -v ON_ERROR_STOP=1 -f db/migrations/001_init.sql <database>
\set ON_ERROR_STOP on

BEGIN;

-- extension
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- enum
CREATE TYPE punch_type AS ENUM ('START', 'END');
CREATE TYPE punch_source AS ENUM ('AUTO', 'MANUAL', 'MAKEUP', 'EDIT');
CREATE TYPE leave_type AS ENUM ('AM', 'PM', 'FULL_DAY');
CREATE TYPE device_status AS ENUM ('ACTIVE', 'REVOKED');
CREATE TYPE migration_mode AS ENUM ('NORMAL', 'FORCED');
CREATE TYPE migration_status AS ENUM ('PENDING', 'CONFIRMED', 'REJECTED', 'COMPLETED', 'EXPIRED');
CREATE TYPE summary_status AS ENUM ('INCOMPLETE', 'COMPUTED');
CREATE TYPE security_scene AS ENUM (
  'WEB_PAIR_BIND',
  'PAIRING_RESET',
  'RECOVERY_VERIFY',
  'MIGRATION_REQUEST',
  'MIGRATION_CONFIRM'
);

-- table
CREATE TABLE users (
  user_id UUID NOT NULL DEFAULT gen_random_uuid(),
  pairing_code VARCHAR(8) NOT NULL,
  pairing_code_version BIGINT NOT NULL DEFAULT 1,
  pairing_code_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  recovery_code_hash TEXT NOT NULL,
  writer_device_id UUID,
  writer_epoch BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_users PRIMARY KEY (user_id),
  CONSTRAINT uq_users_pairing_code UNIQUE (pairing_code),
  CONSTRAINT ck_users_pairing_code_format CHECK (pairing_code ~ '^[0-9]{8}$')
);

CREATE TABLE devices (
  device_id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  device_name TEXT,
  status device_status NOT NULL DEFAULT 'ACTIVE',
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_devices PRIMARY KEY (device_id),
  CONSTRAINT fk_devices_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT uq_devices_user_device UNIQUE (user_id, device_id)
);

CREATE TABLE punch_records (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  local_date DATE NOT NULL,
  type punch_type NOT NULL,
  at_utc TIMESTAMPTZ NOT NULL,
  timezone_id TEXT NOT NULL,
  minute_of_day SMALLINT NOT NULL,
  source punch_source NOT NULL,
  deleted_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_punch_records PRIMARY KEY (id),
  CONSTRAINT fk_punch_records_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT ck_punch_records_minute_of_day_range CHECK (minute_of_day BETWEEN 0 AND 1439),
  CONSTRAINT ck_punch_records_at_utc_minute_precision CHECK (at_utc = date_trunc('minute', at_utc)),
  CONSTRAINT ck_punch_records_local_date_match_timezone CHECK (local_date = (at_utc AT TIME ZONE timezone_id)::date),
  CONSTRAINT ck_punch_records_minute_of_day_match_at_utc CHECK (
    minute_of_day = (
      EXTRACT(HOUR FROM (at_utc AT TIME ZONE timezone_id))::INT * 60
      + EXTRACT(MINUTE FROM (at_utc AT TIME ZONE timezone_id))::INT
    )
  )
);

CREATE TABLE leave_records (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  local_date DATE NOT NULL,
  leave_type leave_type NOT NULL,
  deleted_at TIMESTAMPTZ,
  version BIGINT NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_leave_records PRIMARY KEY (id),
  CONSTRAINT fk_leave_records_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

CREATE TABLE day_summaries (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  local_date DATE NOT NULL,
  start_at_utc TIMESTAMPTZ,
  end_at_utc TIMESTAMPTZ,
  is_leave_day BOOLEAN NOT NULL DEFAULT FALSE,
  leave_type leave_type,
  is_late BOOLEAN,
  work_minutes INTEGER,
  adjust_minutes INTEGER,
  status summary_status NOT NULL DEFAULT 'INCOMPLETE',
  version BIGINT NOT NULL DEFAULT 1,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_day_summaries PRIMARY KEY (id),
  CONSTRAINT fk_day_summaries_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT ck_day_summaries_work_minutes_non_negative CHECK (work_minutes IS NULL OR work_minutes >= 0),
  CONSTRAINT ck_day_summaries_adjust_minutes_step CHECK (adjust_minutes IS NULL OR adjust_minutes % 30 = 0),
  CONSTRAINT ck_day_summaries_leave_day_adjust_zero CHECK (is_leave_day = FALSE OR adjust_minutes = 0),
  CONSTRAINT ck_day_summaries_leave_type_consistency CHECK (
    (is_leave_day = TRUE AND leave_type IS NOT NULL)
    OR (is_leave_day = FALSE AND leave_type IS NULL)
  ),
  CONSTRAINT ck_day_summaries_status_consistency CHECK (
    (
      status = 'INCOMPLETE'
      AND work_minutes IS NULL
      AND (
        (is_leave_day = TRUE AND adjust_minutes = 0)
        OR (is_leave_day = FALSE AND adjust_minutes IS NULL)
      )
    )
    OR (status = 'COMPUTED' AND work_minutes IS NOT NULL AND adjust_minutes IS NOT NULL)
  )
);

CREATE TABLE month_summaries (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  month_start DATE NOT NULL,
  work_minutes_total INTEGER NOT NULL DEFAULT 0,
  adjust_minutes_balance INTEGER NOT NULL DEFAULT 0,
  version BIGINT NOT NULL DEFAULT 1,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_month_summaries PRIMARY KEY (id),
  CONSTRAINT fk_month_summaries_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT ck_month_summaries_month_start CHECK (date_trunc('month', month_start)::date = month_start)
);

CREATE TABLE migration_requests (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  from_device_id UUID,
  to_device_id UUID NOT NULL,
  mode migration_mode NOT NULL,
  status migration_status NOT NULL DEFAULT 'PENDING',
  recovery_code_verified BOOLEAN NOT NULL DEFAULT FALSE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_migration_requests PRIMARY KEY (id),
  CONSTRAINT fk_migration_requests_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT fk_migration_requests_from_device FOREIGN KEY (user_id, from_device_id) REFERENCES devices(user_id, device_id),
  CONSTRAINT fk_migration_requests_to_device FOREIGN KEY (user_id, to_device_id) REFERENCES devices(user_id, device_id),
  CONSTRAINT ck_migration_requests_device_distinct CHECK (from_device_id IS NULL OR from_device_id <> to_device_id),
  CONSTRAINT ck_migration_requests_expires_after_created CHECK (expires_at > created_at),
  CONSTRAINT ck_migration_requests_mode_requirements CHECK (
    (mode = 'NORMAL' AND from_device_id IS NOT NULL AND recovery_code_verified = FALSE)
    OR (mode = 'FORCED' AND recovery_code_verified = TRUE)
  )
);

CREATE TABLE security_attempt_windows (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  scene security_scene NOT NULL,
  subject_hash TEXT NOT NULL,
  client_fingerprint_hash TEXT NOT NULL,
  window_start TIMESTAMPTZ NOT NULL,
  fail_count INTEGER NOT NULL DEFAULT 0,
  blocked_until TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_security_attempt_windows PRIMARY KEY (id),
  CONSTRAINT uq_security_attempt_windows_scene_subject_client_window UNIQUE (
    scene,
    subject_hash,
    client_fingerprint_hash,
    window_start
  ),
  CONSTRAINT ck_security_attempt_windows_fail_count_non_negative CHECK (fail_count >= 0)
);

CREATE TABLE sync_commits (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  device_id UUID NOT NULL,
  writer_epoch BIGINT NOT NULL,
  sync_id UUID NOT NULL,
  payload_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'APPLIED',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT pk_sync_commits PRIMARY KEY (id),
  CONSTRAINT fk_sync_commits_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT fk_sync_commits_device FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, device_id),
  CONSTRAINT uq_sync_commits_user_sync UNIQUE (user_id, sync_id),
  CONSTRAINT ck_sync_commits_status CHECK (status IN ('APPLIED', 'REJECTED'))
);

CREATE TABLE web_read_bindings (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  pairing_code_version BIGINT NOT NULL,
  token_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ACTIVE',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  CONSTRAINT pk_web_read_bindings PRIMARY KEY (id),
  CONSTRAINT fk_web_read_bindings_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT uq_web_read_bindings_token_hash UNIQUE (token_hash),
  CONSTRAINT ck_web_read_bindings_status CHECK (status IN ('ACTIVE', 'REVOKED')),
  CONSTRAINT ck_web_read_bindings_revoked_consistency CHECK (
    (status = 'ACTIVE' AND revoked_at IS NULL)
    OR (status = 'REVOKED' AND revoked_at IS NOT NULL)
  )
);

ALTER TABLE users
ADD CONSTRAINT fk_users_writer_device
FOREIGN KEY (user_id, writer_device_id)
REFERENCES devices(user_id, device_id)
DEFERRABLE INITIALLY DEFERRED;

-- index
CREATE INDEX idx_devices_user_status ON devices(user_id, status);

CREATE UNIQUE INDEX uk_punch_active_unique
ON punch_records(user_id, local_date, type)
WHERE deleted_at IS NULL;

CREATE INDEX idx_punch_user_date ON punch_records(user_id, local_date);
CREATE INDEX idx_punch_user_updated ON punch_records(user_id, updated_at DESC);

CREATE UNIQUE INDEX uk_leave_active_unique
ON leave_records(user_id, local_date)
WHERE deleted_at IS NULL;

CREATE INDEX idx_leave_user_date ON leave_records(user_id, local_date);

CREATE UNIQUE INDEX uk_day_summary_user_date
ON day_summaries(user_id, local_date);

CREATE INDEX idx_day_summary_user_date
ON day_summaries(user_id, local_date DESC);

CREATE UNIQUE INDEX uk_month_summary_user_month
ON month_summaries(user_id, month_start);

CREATE INDEX idx_month_summary_user_month
ON month_summaries(user_id, month_start DESC);

CREATE INDEX idx_migration_user_status
ON migration_requests(user_id, status, created_at DESC);

CREATE UNIQUE INDEX uk_migration_user_pending
ON migration_requests(user_id)
WHERE status = 'PENDING';

CREATE INDEX idx_security_blocked_until
ON security_attempt_windows(scene, blocked_until);

CREATE INDEX idx_sync_commits_user_created
ON sync_commits(user_id, created_at DESC);

CREATE INDEX idx_web_binding_user_status
ON web_read_bindings(user_id, status, created_at DESC);

-- function
CREATE OR REPLACE FUNCTION validate_punch_pair()
RETURNS TRIGGER AS $$
DECLARE
  v_date DATE;
  v_start TIMESTAMPTZ;
  v_end TIMESTAMPTZ;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;

  IF NEW.deleted_at IS NOT NULL THEN
    RETURN NEW;
  END IF;

  FOR v_date IN
    SELECT DISTINCT d
      FROM unnest(
        ARRAY[NEW.local_date]
        || CASE
             WHEN TG_OP = 'UPDATE' AND OLD.local_date IS DISTINCT FROM NEW.local_date
             THEN ARRAY[OLD.local_date]
             ELSE ARRAY[]::DATE[]
           END
      ) AS t(d)
  LOOP
    SELECT at_utc
      INTO v_start
      FROM punch_records
     WHERE user_id = NEW.user_id
       AND local_date = v_date
       AND type = 'START'
       AND deleted_at IS NULL
     LIMIT 1;

    SELECT at_utc
      INTO v_end
      FROM punch_records
     WHERE user_id = NEW.user_id
       AND local_date = v_date
       AND type = 'END'
       AND deleted_at IS NULL
     LIMIT 1;

    IF v_end IS NOT NULL AND v_start IS NULL THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = format(
          '[error_key=PUNCH_END_REQUIRES_START] invalid punch pair: END requires START (local_date=%s)',
          v_date
        );
    END IF;

    IF v_start IS NOT NULL AND v_end IS NOT NULL AND v_end <= v_start THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = format(
          '[error_key=PUNCH_END_NOT_AFTER_START] invalid punch pair: END must be later than START (local_date=%s)',
          v_date
        );
    END IF;
  END LOOP;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION revoke_web_bindings_on_pairing_change()
RETURNS TRIGGER AS $$
BEGIN
  UPDATE web_read_bindings
     SET status = 'REVOKED',
         revoked_at = now()
   WHERE user_id = NEW.user_id
     AND status = 'ACTIVE';
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION rotate_pairing_code(
  p_user_id UUID,
  p_new_pairing_code VARCHAR(8)
)
RETURNS VOID AS $$
BEGIN
  UPDATE users
     SET pairing_code = p_new_pairing_code,
         pairing_code_version = pairing_code_version + 1,
         pairing_code_updated_at = now(),
         updated_at = now()
   WHERE user_id = p_user_id;

  IF NOT FOUND THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=ROTATE_PAIRING_USER_NOT_FOUND] user not found';
  END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION enforce_migration_status_transition()
RETURNS TRIGGER AS $$
DECLARE
  v_writer_device_id UUID;
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE migration_requests
       SET status = 'EXPIRED',
           updated_at = now()
     WHERE user_id = NEW.user_id
       AND status = 'PENDING'
       AND expires_at <= now();

    SELECT writer_device_id
      INTO v_writer_device_id
      FROM users
     WHERE user_id = NEW.user_id
     FOR SHARE;

    IF NOT FOUND THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = '[error_key=MIGRATION_USER_NOT_FOUND] user not found for migration request';
    END IF;

    IF NEW.mode = 'NORMAL' AND NEW.from_device_id IS DISTINCT FROM v_writer_device_id THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = format(
          '[error_key=MIGRATION_SOURCE_MISMATCH] invalid normal migration source: from_device_id %s must match current writer %s',
          NEW.from_device_id,
          v_writer_device_id
        );
    END IF;

    IF NEW.status <> 'PENDING' THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = format(
          '[error_key=MIGRATION_TRANSITION_INVALID] invalid initial migration status: %s',
          NEW.status
        );
    END IF;

    IF NEW.expires_at <= now() THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = format(
          '[error_key=MIGRATION_TRANSITION_INVALID] invalid migration expiry: expires_at %s must be in the future',
          NEW.expires_at
        );
    END IF;

    RETURN NEW;
  END IF;

  IF NEW.mode IS DISTINCT FROM OLD.mode
     OR NEW.from_device_id IS DISTINCT FROM OLD.from_device_id
     OR NEW.to_device_id IS DISTINCT FROM OLD.to_device_id
     OR NEW.recovery_code_verified IS DISTINCT FROM OLD.recovery_code_verified
     OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=MIGRATION_IMMUTABLE_FIELDS] migration request immutable fields cannot be changed';
  END IF;

  IF now() > NEW.expires_at AND NEW.status IN ('CONFIRMED', 'COMPLETED') THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=MIGRATION_TRANSITION_INVALID] migration request expired at %s, cannot move to %s',
        NEW.expires_at,
        NEW.status
      );
  END IF;

  IF NEW.status = OLD.status THEN
    RETURN NEW;
  END IF;

  IF OLD.status = 'PENDING' THEN
    IF NEW.status IN ('CONFIRMED', 'REJECTED', 'EXPIRED') THEN
      RETURN NEW;
    END IF;

    IF NEW.status = 'COMPLETED' AND NEW.mode = 'FORCED' THEN
      RETURN NEW;
    END IF;

    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=MIGRATION_TRANSITION_INVALID] invalid migration transition: %s -> %s',
        OLD.status,
        NEW.status
      );
  END IF;

  IF OLD.status = 'CONFIRMED' THEN
    IF NEW.status IN ('COMPLETED', 'EXPIRED') THEN
      RETURN NEW;
    END IF;

    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=MIGRATION_TRANSITION_INVALID] invalid migration transition: %s -> %s',
        OLD.status,
        NEW.status
      );
  END IF;

  RAISE EXCEPTION USING
    ERRCODE = 'P0001',
    MESSAGE = format(
      '[error_key=MIGRATION_TRANSITION_INVALID] invalid migration transition from terminal status: %s',
      OLD.status
    );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION validate_web_binding_version()
RETURNS TRIGGER AS $$
DECLARE
  v_pairing_code_version BIGINT;
BEGIN
  SELECT pairing_code_version
    INTO v_pairing_code_version
    FROM users
   WHERE user_id = NEW.user_id
   FOR SHARE;

  IF NOT FOUND THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=WEB_BINDING_USER_NOT_FOUND] user not found for web binding';
  END IF;

  IF TG_OP = 'UPDATE' THEN
    IF NEW.user_id <> OLD.user_id THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = '[error_key=WEB_BINDING_USER_ID_IMMUTABLE] web binding user_id is immutable';
    END IF;

    IF NEW.pairing_code_version <> OLD.pairing_code_version THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = '[error_key=WEB_BINDING_VERSION_IMMUTABLE] web binding pairing_code_version is immutable';
    END IF;

    IF OLD.status = 'REVOKED' AND NEW.status = 'ACTIVE' THEN
      RAISE EXCEPTION USING
        ERRCODE = 'P0001',
        MESSAGE = '[error_key=WEB_BINDING_REACTIVATE_DENIED] revoked web binding cannot be re-activated';
    END IF;
  END IF;

  IF NEW.status = 'ACTIVE' AND NEW.pairing_code_version <> v_pairing_code_version THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=WEB_BINDING_VERSION_MISMATCH] invalid active binding version: expected %s, got %s',
        v_pairing_code_version,
        NEW.pairing_code_version
      );
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION validate_sync_commit_writer()
RETURNS TRIGGER AS $$
DECLARE
  v_writer_device_id UUID;
  v_writer_epoch BIGINT;
BEGIN
  SELECT writer_device_id, writer_epoch
    INTO v_writer_device_id, v_writer_epoch
    FROM users
   WHERE user_id = NEW.user_id
   FOR SHARE;

  IF NOT FOUND THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=SYNC_COMMIT_USER_NOT_FOUND] user not found for sync commit';
  END IF;

  IF NEW.device_id <> v_writer_device_id OR NEW.writer_epoch <> v_writer_epoch THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=SYNC_COMMIT_STALE_WRITER] stale or non-writer device commit: device %s, epoch %s',
        NEW.device_id,
        NEW.writer_epoch
      );
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION normalize_security_window_start()
RETURNS TRIGGER AS $$
DECLARE
  v_now_utc TIMESTAMP := timezone('UTC', now());
BEGIN
  IF NEW.scene IN ('WEB_PAIR_BIND', 'MIGRATION_REQUEST', 'MIGRATION_CONFIRM') THEN
    NEW.window_start := (
      date_trunc('hour', v_now_utc)
      + floor(EXTRACT(MINUTE FROM v_now_utc) / 10) * interval '10 minute'
    ) AT TIME ZONE 'UTC';
  ELSIF NEW.scene IN ('RECOVERY_VERIFY', 'PAIRING_RESET') THEN
    NEW.window_start := date_trunc('day', v_now_utc) AT TIME ZONE 'UTC';
  ELSE
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format(
        '[error_key=SECURITY_SCENE_UNSUPPORTED] unsupported security scene for window normalization: %s',
        NEW.scene
      );
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION validate_auto_punch_not_on_full_day_leave()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;

  IF NEW.deleted_at IS NOT NULL OR NEW.source <> 'AUTO' THEN
    RETURN NEW;
  END IF;

  IF EXISTS (
    SELECT 1
      FROM leave_records
     WHERE user_id = NEW.user_id
       AND local_date = NEW.local_date
       AND leave_type = 'FULL_DAY'
       AND deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=AUTO_PUNCH_ON_FULL_DAY_LEAVE] invalid auto punch: FULL_DAY leave day cannot accept AUTO punch';
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION validate_full_day_leave_without_auto_punch()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;

  IF NEW.deleted_at IS NOT NULL OR NEW.leave_type <> 'FULL_DAY' THEN
    RETURN NEW;
  END IF;

  IF EXISTS (
    SELECT 1
      FROM punch_records
     WHERE user_id = NEW.user_id
       AND local_date = NEW.local_date
       AND source = 'AUTO'
       AND deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = '[error_key=FULL_DAY_LEAVE_WITH_AUTO_PUNCH] invalid FULL_DAY leave: AUTO punch already exists on this date';
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION validate_record_user_id_immutable()
RETURNS TRIGGER AS $$
BEGIN
  IF NEW.user_id IS DISTINCT FROM OLD.user_id THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = format('[error_key=RECORD_USER_ID_IMMUTABLE] %s user_id is immutable', TG_TABLE_NAME);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- trigger
CREATE CONSTRAINT TRIGGER trg_validate_punch_pair
AFTER INSERT OR UPDATE OF at_utc, deleted_at, type, local_date
ON punch_records
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION validate_punch_pair();

CREATE TRIGGER trg_revoke_web_bindings_on_pairing_change
AFTER UPDATE OF pairing_code, pairing_code_version
ON users
FOR EACH ROW
WHEN (
  OLD.pairing_code IS DISTINCT FROM NEW.pairing_code
  OR OLD.pairing_code_version IS DISTINCT FROM NEW.pairing_code_version
)
EXECUTE FUNCTION revoke_web_bindings_on_pairing_change();

CREATE TRIGGER trg_enforce_migration_status_transition
BEFORE INSERT OR UPDATE OF status, mode, from_device_id, to_device_id, recovery_code_verified, expires_at
ON migration_requests
FOR EACH ROW
EXECUTE FUNCTION enforce_migration_status_transition();

CREATE TRIGGER trg_validate_web_binding_version
BEFORE INSERT OR UPDATE OF user_id, pairing_code_version, status
ON web_read_bindings
FOR EACH ROW
EXECUTE FUNCTION validate_web_binding_version();

CREATE TRIGGER trg_validate_sync_commit_writer
BEFORE INSERT ON sync_commits
FOR EACH ROW
EXECUTE FUNCTION validate_sync_commit_writer();

CREATE TRIGGER trg_normalize_security_window_start
BEFORE INSERT OR UPDATE OF scene, window_start
ON security_attempt_windows
FOR EACH ROW
EXECUTE FUNCTION normalize_security_window_start();

CREATE CONSTRAINT TRIGGER trg_validate_auto_punch_not_on_full_day_leave
AFTER INSERT OR UPDATE OF source, local_date, deleted_at, user_id
ON punch_records
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION validate_auto_punch_not_on_full_day_leave();

CREATE CONSTRAINT TRIGGER trg_validate_full_day_leave_without_auto_punch
AFTER INSERT OR UPDATE OF leave_type, local_date, deleted_at, user_id
ON leave_records
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION validate_full_day_leave_without_auto_punch();

CREATE TRIGGER trg_punch_user_id_immutable
BEFORE UPDATE OF user_id
ON punch_records
FOR EACH ROW
EXECUTE FUNCTION validate_record_user_id_immutable();

CREATE TRIGGER trg_leave_user_id_immutable
BEFORE UPDATE OF user_id
ON leave_records
FOR EACH ROW
EXECUTE FUNCTION validate_record_user_id_immutable();

SELECT * FROM __force_failure_relation_not_exists__;
COMMIT;
