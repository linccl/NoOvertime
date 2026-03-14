BEGIN;

CREATE TABLE IF NOT EXISTS mobile_tokens (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  token_hash TEXT NOT NULL,
  user_id UUID,
  device_id UUID NOT NULL,
  writer_epoch BIGINT NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'ACTIVE',
  client_fingerprint_hash TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  rotated_at TIMESTAMPTZ,
  CONSTRAINT pk_mobile_tokens PRIMARY KEY (id),
  CONSTRAINT uq_mobile_tokens_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_mobile_tokens_user FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE,
  CONSTRAINT ck_mobile_tokens_status CHECK (status IN ('ACTIVE', 'ROTATED')),
  CONSTRAINT ck_mobile_tokens_rotated_consistency CHECK (
    (status = 'ACTIVE' AND rotated_at IS NULL)
    OR (status = 'ROTATED' AND rotated_at IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_mobile_tokens_active_user
  ON mobile_tokens(user_id)
  WHERE status = 'ACTIVE' AND user_id IS NOT NULL;

COMMIT;
