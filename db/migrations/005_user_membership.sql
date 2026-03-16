ALTER TABLE users
  ADD COLUMN IF NOT EXISTS membership_tier TEXT NOT NULL DEFAULT 'FREE';

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS membership_expires_at TIMESTAMPTZ;

ALTER TABLE users
  DROP CONSTRAINT IF EXISTS ck_users_membership_tier;

ALTER TABLE users
  ADD CONSTRAINT ck_users_membership_tier
  CHECK (membership_tier IN ('FREE', 'MEMBER'));

CREATE INDEX IF NOT EXISTS idx_users_membership_active
  ON users (membership_tier, membership_expires_at)
  WHERE membership_tier = 'MEMBER';
