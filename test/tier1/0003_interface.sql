-- =============================================================================
-- 0003 INTERFACE — AUTH. NAMES ONLY, NO ENFORCEMENT.
--
-- The tables, columns and functions the 0003 acceptance tests are written
-- against, with none of the rules they check. Applied on top of 0001 + 0002 it
-- is "M0" for the auth suite. Under review with the tests.
--
-- The functions exist with NAIVE bodies (session_valid is true until ended,
-- lock_state never locks, etc.) so the tests can call them; 0003 replaces
-- the bodies with the rules.
-- =============================================================================
BEGIN;

-- ONE CLOCK for every auth window. It honours a test clock only when the
-- DATABASE-level setting argon.test_clock = 'on' exists in pg_db_role_setting,
-- which only the database owner or a superuser can create. A session-level
-- SET by the app does not count. This is infrastructure, not a rule under
-- test (TestAuth0_* checks it).
CREATE FUNCTION auth_now() RETURNS timestamptz AS $$
  SELECT CASE WHEN EXISTS (
           SELECT 1 FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase
            WHERE d.datname = current_database() AND s.setrole = 0
              AND 'argon.test_clock=on' = ANY (s.setconfig))
         THEN coalesce(nullif(current_setting('argon.test_now', true), '')::timestamptz, now())
         ELSE now() END;
$$ LANGUAGE sql STABLE;

-- The current PIN, as a hash.
CREATE TABLE auth_credentials (
  user_id  UUID PRIMARY KEY REFERENCES users(id),
  pin_hash TEXT NOT NULL,
  set_at   TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);

-- Evidence that a person just proved something: their PIN, or holding the
-- phone (a code). Written by the service after it has verified.
CREATE TABLE auth_proofs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id),
  method      TEXT NOT NULL,           -- 'pin' | 'code'
  at          TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  consumed_at TIMESTAMPTZ
);

-- One-time codes. The code itself is never stored; code_digest is an HMAC.
CREATE TABLE auth_codes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id),
  purpose     TEXT NOT NULL,           -- 'new_device' | 'recovery' | 'activation'
  code_digest TEXT NOT NULL,
  sent_to     TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  attempts    INTEGER NOT NULL DEFAULT 0,
  consumed_at TIMESTAMPTZ
);

-- A device the server has issued a token to. token_digest is a hash.
CREATE TABLE auth_devices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id),
  token_digest TEXT NOT NULL,
  trusted_at   TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  revoked_at   TIMESTAMPTZ
);

CREATE TABLE auth_sessions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID NOT NULL REFERENCES users(id),
  device_id        UUID REFERENCES auth_devices(id),
  token_digest     TEXT NOT NULL,
  started_at       TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  expires_at       TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  ended_by_session UUID REFERENCES auth_sessions(id)
);

-- Every sign-in attempt. user_id is the RESOLVED account (NULL for a
-- credential that matches no account); credential_digest identifies an
-- unregistered credential so it can be treated identically.
CREATE TABLE sign_in_attempts (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID REFERENCES users(id),
  credential_digest TEXT NOT NULL,
  device_id         UUID REFERENCES auth_devices(id),
  succeeded         BOOLEAN NOT NULL,
  at                TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);

-- Every attributable act on a credential, append-only.
--   kind: 'pin_change' (self) | 'member_reset' | 'phone_change' | 'lock_release'
--       | 'signatory_reset_request' | 'signatory_phone_request' | 'signatory_approval'
CREATE TABLE credential_acts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind             TEXT NOT NULL,
  actor_user_id    UUID NOT NULL REFERENCES users(id),
  actor_session_id UUID REFERENCES auth_sessions(id),
  proof_id         UUID REFERENCES auth_proofs(id),
  target_user_id   UUID REFERENCES users(id),
  target_member_id UUID REFERENCES members(id),
  new_phone        TEXT,
  new_pin_hash     TEXT,
  request_id       UUID REFERENCES credential_acts(id),
  reason           TEXT,
  at               TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);

-- ---- functions, NAIVE ------------------------------------------------------

-- Where a code for this person may be sent.
CREATE FUNCTION otp_destination(p_user UUID) RETURNS TEXT AS $$
  SELECT phone FROM members WHERE user_id = p_user LIMIT 1;
$$ LANGUAGE sql STABLE;

-- Check a code attempt; true if it is accepted.
CREATE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (SELECT 1 FROM auth_codes WHERE id = p_code AND code_digest = p_digest);
$$ LANGUAGE sql VOLATILE;

-- The account's lock state now.
CREATE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT NULL::timestamptz, false;
$$ LANGUAGE sql STABLE;

CREATE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (SELECT 1 FROM auth_sessions WHERE token_digest = p_token_digest AND ended_at IS NULL);
$$ LANGUAGE sql STABLE;

CREATE FUNCTION device_trusted(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (SELECT 1 FROM auth_devices WHERE token_digest = p_token_digest);
$$ LANGUAGE sql STABLE;

COMMIT;
