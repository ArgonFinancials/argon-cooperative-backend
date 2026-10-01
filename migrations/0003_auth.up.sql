-- =============================================================================
-- ARGON — 0003: AUTHENTICATION STATE
--
-- Written against test/tier1/tier1_0003_test.go and tier1_0003_service_test.go
-- (reviewed, and mutation-tested by test/tier1/mutate.sh 0003 before this file
-- existed). Constraint names are the tests' interface: a trigger reports one
-- with RAISE ... USING ERRCODE = 'check_violation', CONSTRAINT = '<name>'.
--
-- What lives here: every rule about STATE. Hash formats, proofs, attributable
-- acts, the two-signatory reset, phone changes, codes, lockout, sessions.
-- What does not: argon2id (not available in Postgres), code generation, the
-- uniform refusal. Those are internal/auth.
--
-- Effects (a reset clearing a PIN, a phone change, consuming a proof, counting
-- a code attempt) run in SECURITY DEFINER functions owned by argon_owner. The
-- app holds no UPDATE or DELETE on credentials, proofs or codes, so the only
-- way to consume a proof or reset an attempt counter is through the rule.
-- =============================================================================

BEGIN;

-- -----------------------------------------------------------------------------
-- THE CLOCK. One clock for every auth window. It honours a test clock only via
-- a DATABASE-level setting (pg_db_role_setting), which only the owner or a
-- superuser can create; a session-level SET does not count (TestAuth0).
-- -----------------------------------------------------------------------------
CREATE FUNCTION auth_now() RETURNS timestamptz AS $$
  SELECT CASE WHEN EXISTS (
           SELECT 1 FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase
            WHERE d.datname = current_database() AND s.setrole = 0
              AND 'argon.test_clock=on' = ANY (s.setconfig))
         THEN coalesce(nullif(current_setting('argon.test_now', true), '')::timestamptz, now())
         ELSE now() END;
$$ LANGUAGE sql STABLE;

-- Terms are dates in the society's own calendar.
CREATE FUNCTION auth_today() RETURNS date AS $$
  SELECT (auth_now() AT TIME ZONE 'Africa/Lagos')::date;
$$ LANGUAGE sql STABLE;

-- Does this person hold any post today? Optionally: a signing post, in a
-- society, excluding the internal auditor.
CREATE FUNCTION person_holds_post(p_user UUID, p_society UUID DEFAULT NULL,
                                  p_signing BOOLEAN DEFAULT false, p_exclude_auditor BOOLEAN DEFAULT false)
RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
     WHERE m.user_id = p_user
       AND (p_society IS NULL OR a.society_id = p_society)
       AND (NOT p_signing OR a.is_signatory)
       AND (NOT p_exclude_auditor OR a.post <> 'internal_auditor')
       AND a.held_from <= auth_today() AND (a.held_until IS NULL OR a.held_until >= auth_today()));
$$ LANGUAGE sql STABLE;

-- An officer of the target's society: holds a non-auditor post in a society
-- where the target person has a member record.
CREATE FUNCTION officer_over(p_actor UUID, p_target UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM members tm
     WHERE tm.user_id = p_target
       AND person_holds_post(p_actor, tm.society_id, false, true));
$$ LANGUAGE sql STABLE;

CREATE FUNCTION auth_refuse(p_constraint TEXT, p_message TEXT) RETURNS void AS $$
BEGIN
  RAISE EXCEPTION '%', p_message USING ERRCODE = 'check_violation', CONSTRAINT = p_constraint;
END $$ LANGUAGE plpgsql;

-- Server-written time: a supplied value is refused, not checked.
CREATE FUNCTION auth_server_time(p_value timestamptz, p_constraint TEXT) RETURNS void AS $$
BEGIN
  IF p_value IS DISTINCT FROM auth_now() THEN
    PERFORM auth_refuse(p_constraint, format('%s is written by the server, not supplied', p_constraint));
  END IF;
END $$ LANGUAGE plpgsql;

-- =============================================================================
-- CREDENTIALS — the PIN, as an argon2id PHC string, and only that.
-- =============================================================================
CREATE TABLE auth_credentials (
  user_id  UUID PRIMARY KEY REFERENCES users(id),
  pin_hash TEXT NOT NULL,
  set_at   TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  CONSTRAINT pin_is_argon2id CHECK (
    pin_hash ~ '^\$argon2id\$v=19\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$')
);

-- =============================================================================
-- PROOFS — "someone just proved their PIN" or "someone holds the phone".
-- A code proves the phone, not the person: only a 'pin' proof authorises an
-- attributable act (TestAuthB*).
-- =============================================================================
CREATE TABLE auth_proofs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id),
  method      TEXT NOT NULL CHECK (method IN ('pin', 'code')),
  at          TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  consumed_at TIMESTAMPTZ
);

CREATE FUNCTION auth_proofs_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.at, 'auth_proofs_at_is_now');
  IF NEW.consumed_at IS NOT NULL THEN
    PERFORM auth_refuse('auth_proofs_at_is_now', 'a proof is born unconsumed');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_proofs_admissible BEFORE INSERT ON auth_proofs
  FOR EACH ROW EXECUTE FUNCTION auth_proofs_admissible();

-- =============================================================================
-- CODES — never stored; a 64-hex HMAC digest. Only to the one number all of
-- the person's records agree on; 5 minutes; 3 attempts; one use; 60 s apart.
-- =============================================================================
CREATE TABLE auth_codes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id),
  purpose     TEXT NOT NULL CHECK (purpose IN ('new_device', 'recovery', 'activation')),
  code_digest TEXT NOT NULL CONSTRAINT code_is_digest CHECK (code_digest ~ '^[0-9a-f]{64}$'),
  sent_to     TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  attempts    INTEGER NOT NULL DEFAULT 0,
  consumed_at TIMESTAMPTZ
);
CREATE INDEX auth_codes_user_idx ON auth_codes (user_id, created_at DESC);

-- The one number every one of the person's records carries, or NULL: no
-- number, or records that disagree (a duplicate that may carry a planted one).
CREATE FUNCTION otp_destination(p_user UUID) RETURNS TEXT AS $$
  SELECT CASE WHEN count(DISTINCT phone) = 1 THEN min(phone) END FROM members WHERE user_id = p_user;
$$ LANGUAGE sql STABLE;

CREATE FUNCTION auth_codes_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.created_at, 'auth_codes_at_is_now');
  IF NEW.attempts <> 0 OR NEW.consumed_at IS NOT NULL THEN
    PERFORM auth_refuse('auth_codes_at_is_now', 'a code is born unattempted and unconsumed');
  END IF;
  IF NEW.sent_to IS NULL OR NEW.sent_to IS DISTINCT FROM otp_destination(NEW.user_id) THEN
    PERFORM auth_refuse('code_goes_to_registered_phone',
      'a code goes only to the one number all of the person''s records agree on');
  END IF;
  IF EXISTS (SELECT 1 FROM auth_codes WHERE user_id = NEW.user_id AND created_at > auth_now() - interval '60 seconds') THEN
    PERFORM auth_refuse('code_resend_interval', 'a code can be re-sent once every 60 seconds');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_codes_admissible BEFORE INSERT ON auth_codes
  FOR EACH ROW EXECUTE FUNCTION auth_codes_admissible();

-- Every attempt counts, right or wrong; the fourth is refused even if right.
CREATE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c auth_codes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM auth_codes WHERE id = p_code FOR UPDATE;
  IF NOT FOUND OR c.consumed_at IS NOT NULL THEN RETURN false; END IF;
  IF auth_now() > c.created_at + interval '5 minutes' THEN RETURN false; END IF;
  IF c.attempts >= 3 THEN RETURN false; END IF;
  UPDATE auth_codes SET attempts = attempts + 1 WHERE id = p_code;
  IF c.code_digest = p_digest THEN
    UPDATE auth_codes SET consumed_at = auth_now() WHERE id = p_code;
    RETURN true;
  END IF;
  RETURN false;
END $$ LANGUAGE plpgsql VOLATILE;

-- =============================================================================
-- DEVICES — known only by a token the server issued (stored as a digest).
-- A revocation is final.
-- =============================================================================
CREATE TABLE auth_devices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id),
  token_digest TEXT NOT NULL CONSTRAINT token_is_digest CHECK (token_digest ~ '^[0-9a-f]{64}$'),
  trusted_at   TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  revoked_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX auth_devices_token_idx ON auth_devices (token_digest);

CREATE FUNCTION auth_devices_revocation_final() RETURNS trigger AS $$
BEGIN
  IF OLD.revoked_at IS NOT NULL THEN
    PERFORM auth_refuse('device_revocation_is_final', 'a revoked device stays revoked');
  END IF;
  IF NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.token_digest IS DISTINCT FROM OLD.token_digest
     OR NEW.trusted_at IS DISTINCT FROM OLD.trusted_at THEN
    PERFORM auth_refuse('device_revocation_is_final', 'a device record changes only by revocation');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_devices_revocation_final BEFORE UPDATE ON auth_devices
  FOR EACH ROW EXECUTE FUNCTION auth_devices_revocation_final();

CREATE FUNCTION device_trusted(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (SELECT 1 FROM auth_devices WHERE token_digest = p_token_digest AND revoked_at IS NULL);
$$ LANGUAGE sql STABLE;

-- =============================================================================
-- SESSIONS
--
-- Length follows the highest privilege the person holds NOW: 8 h for anyone
-- holding a post (officers, approvers, signed in on any surface), 30 d
-- otherwise. A session REMEMBERS whether it was ever an officer's
-- (privileged), set at start and whenever the person gains a post; when such
-- a session's holder no longer holds a post, it ENDS (decision 2026-10-01).
-- The memory is the point: terms are dates, and ending one rewrites what the
-- appointments table says about when it ended.
-- =============================================================================
CREATE TABLE auth_sessions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID NOT NULL REFERENCES users(id),
  device_id        UUID REFERENCES auth_devices(id),
  token_digest     TEXT NOT NULL CONSTRAINT token_is_digest CHECK (token_digest ~ '^[0-9a-f]{64}$'),
  started_at       TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  expires_at       TIMESTAMPTZ,              -- never written: the server computes validity
  ended_at         TIMESTAMPTZ,
  ended_by_session UUID REFERENCES auth_sessions(id),
  privileged       BOOLEAN NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX auth_sessions_token_idx ON auth_sessions (token_digest);
CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id) WHERE ended_at IS NULL;

CREATE FUNCTION auth_sessions_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.started_at, 'session_expiry_is_server');
  IF NEW.expires_at IS NOT NULL THEN
    PERFORM auth_refuse('session_expiry_is_server', 'a session''s length is the server''s to compute, not supplied');
  END IF;
  IF NEW.ended_at IS NOT NULL OR NEW.ended_by_session IS NOT NULL THEN
    PERFORM auth_refuse('session_end_is_final', 'a session is born open');
  END IF;
  IF NEW.device_id IS NOT NULL THEN
    IF EXISTS (SELECT 1 FROM auth_devices WHERE id = NEW.device_id AND revoked_at IS NOT NULL) THEN
      PERFORM auth_refuse('session_on_revoked_device', 'no session on a revoked device');
    END IF;
    IF (SELECT user_id FROM auth_devices WHERE id = NEW.device_id) IS DISTINCT FROM NEW.user_id THEN
      PERFORM auth_refuse('session_on_revoked_device', 'a session runs on its own person''s device');
    END IF;
  END IF;
  NEW.privileged := person_holds_post(NEW.user_id);
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_sessions_admissible BEFORE INSERT ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION auth_sessions_admissible();

-- Ending: final; by the owner's own session, or by the system's own effects
-- (a reset, a PIN change) running as argon_owner. The app may not end a
-- session anonymously. Ending from ANOTHER session revokes this session's
-- device: "end this session" from elsewhere is the stolen-phone case.
CREATE FUNCTION auth_sessions_end() RETURNS trigger AS $$
BEGIN
  IF OLD.ended_at IS NOT NULL THEN
    PERFORM auth_refuse('session_end_is_final', 'an ended session stays ended');
  END IF;
  IF NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.device_id IS DISTINCT FROM OLD.device_id
     OR NEW.token_digest IS DISTINCT FROM OLD.token_digest OR NEW.started_at IS DISTINCT FROM OLD.started_at
     OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
    PERFORM auth_refuse('session_end_is_final', 'a session changes only by ending, or by gaining privilege');
  END IF;
  IF NEW.ended_at IS NULL THEN
    IF NEW.ended_by_session IS NOT NULL THEN
      PERFORM auth_refuse('session_end_is_final', 'ended_by_session without an end');
    END IF;
    RETURN NEW;                                        -- privileged flag only
  END IF;
  PERFORM auth_server_time(NEW.ended_at, 'session_end_is_final');
  IF NEW.ended_by_session IS NULL THEN
    IF current_user <> 'argon_owner' THEN
      PERFORM auth_refuse('session_ended_by_owner', 'a session is ended by its owner''s own session');
    END IF;
  ELSIF (SELECT user_id FROM auth_sessions WHERE id = NEW.ended_by_session) IS DISTINCT FROM OLD.user_id THEN
    PERFORM auth_refuse('session_ended_by_owner', 'a session is ended by its owner''s own session');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_sessions_end BEFORE UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION auth_sessions_end();

CREATE FUNCTION auth_sessions_revoke_device() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF OLD.ended_at IS NULL AND NEW.ended_at IS NOT NULL
     AND NEW.ended_by_session IS NOT NULL AND NEW.ended_by_session <> NEW.id AND NEW.device_id IS NOT NULL THEN
    UPDATE auth_devices SET revoked_at = auth_now() WHERE id = NEW.device_id AND revoked_at IS NULL;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_sessions_revoke_device AFTER UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION auth_sessions_revoke_device();

-- Gaining a post marks the person's live sessions as officers' sessions.
CREATE FUNCTION appointments_mark_sessions() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  person UUID := (SELECT user_id FROM members WHERE id = NEW.member_id);
BEGIN
  IF person IS NOT NULL AND person_holds_post(person) THEN
    UPDATE auth_sessions SET privileged = true WHERE user_id = person AND ended_at IS NULL AND NOT privileged;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER appointments_mark_sessions AFTER INSERT OR UPDATE ON appointments
  FOR EACH ROW EXECUTE FUNCTION appointments_mark_sessions();

CREATE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s LEFT JOIN auth_devices d ON d.id = s.device_id
     WHERE s.token_digest = p_token_digest
       AND s.ended_at IS NULL
       AND (d.id IS NULL OR d.revoked_at IS NULL)
       AND NOT (s.privileged AND NOT person_holds_post(s.user_id))           -- term ended: the session ends
       AND auth_now() < s.started_at + CASE WHEN s.privileged OR person_holds_post(s.user_id)
                                            THEN interval '8 hours' ELSE interval '30 days' END);
$$ LANGUAGE sql STABLE;

-- =============================================================================
-- SIGN-IN ATTEMPTS AND LOCKOUT — per ACCOUNT.
-- Five consecutive failures lock for 15 min, doubling with each lockout in
-- 24 h; the third in 24 h waits for an officer. A success ends a run of
-- failures but never erases the lockout history. An unregistered credential
-- (user_id NULL) locks by its digest, identically, so lockout cannot tell an
-- attacker which numbers are registered.
-- =============================================================================
CREATE TABLE sign_in_attempts (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seq               BIGINT GENERATED ALWAYS AS IDENTITY,
  user_id           UUID REFERENCES users(id),
  credential_digest TEXT NOT NULL,
  device_id         UUID REFERENCES auth_devices(id),
  succeeded         BOOLEAN NOT NULL,
  at                TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);
CREATE INDEX sign_in_attempts_user_idx ON sign_in_attempts (user_id, at);
CREATE INDEX sign_in_attempts_digest_idx ON sign_in_attempts (credential_digest, at) WHERE user_id IS NULL;

CREATE FUNCTION sign_in_attempts_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.at, 'sign_in_attempts_at_is_now');
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER sign_in_attempts_admissible BEFORE INSERT ON sign_in_attempts
  FOR EACH ROW EXECUTE FUNCTION sign_in_attempts_admissible();

CREATE FUNCTION lock_state_of(p_user UUID, p_digest TEXT, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
DECLARE
  r         record;
  streak    int := 0;
  starts    timestamptz[] := '{}';
  lock_end  timestamptz;
  last_lock timestamptz;
  officer   boolean := false;
  k         int;
BEGIN
  locked_until := NULL;
  needs_officer := false;
  FOR r IN SELECT at, succeeded FROM sign_in_attempts
            WHERE (CASE WHEN p_user IS NOT NULL THEN user_id = p_user
                        ELSE user_id IS NULL AND credential_digest = p_digest END)
              AND at > auth_now() - interval '48 hours'
            ORDER BY at, seq LOOP
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + (SELECT count(*) FROM unnest(starts) s WHERE s > r.at - interval '24 hours');
      starts := starts || r.at;
      last_lock := r.at;
      IF k >= 3 THEN
        officer := true;  lock_end := 'infinity';
      ELSE
        officer := false; lock_end := r.at + interval '15 minutes' * power(2, k - 1);
      END IF;
    END IF;
  END LOOP;
  IF officer THEN
    needs_officer := p_user IS NULL OR NOT EXISTS (
      SELECT 1 FROM credential_acts WHERE kind = 'lock_release' AND target_user_id = p_user AND at >= last_lock);
    RETURN;
  END IF;
  IF lock_end IS NOT NULL AND lock_end > auth_now() THEN
    locked_until := lock_end;
  END IF;
END $$ LANGUAGE plpgsql STABLE;

-- needs_officer = true means locked until an officer releases it.
CREATE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_state_of(p_user, NULL);
$$ LANGUAGE sql STABLE;

CREATE FUNCTION lock_state_for_credential(p_digest TEXT, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_state_of(NULL, p_digest);
$$ LANGUAGE sql STABLE;

-- =============================================================================
-- ATTRIBUTABLE ACTS — append-only. Every one consumes a fresh (5 min),
-- single-use PIN proof OF THE ACTOR. Its time is the server's.
-- =============================================================================
CREATE TABLE credential_acts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind             TEXT NOT NULL CHECK (kind IN ('pin_change', 'member_reset', 'phone_change', 'lock_release',
                     'signatory_reset_request', 'signatory_phone_request', 'signatory_approval')),
  actor_user_id    UUID NOT NULL REFERENCES users(id),
  actor_session_id UUID REFERENCES auth_sessions(id),
  proof_id         UUID REFERENCES auth_proofs(id),
  target_user_id   UUID REFERENCES users(id),
  target_member_id UUID REFERENCES members(id),
  new_phone        TEXT,
  new_pin_hash     TEXT,
  request_id       UUID REFERENCES credential_acts(id),
  reason           TEXT,
  at               TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  act_xid          xid8 NOT NULL DEFAULT pg_current_xact_id()
);
CREATE INDEX credential_acts_target_idx ON credential_acts (target_user_id, kind, at);
CREATE INDEX credential_acts_request_idx ON credential_acts (request_id) WHERE request_id IS NOT NULL;

CREATE FUNCTION credential_acts_immutable() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('credential_acts_at_is_now', 'an attributable act is never altered or removed');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_immutable BEFORE UPDATE OR DELETE ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION credential_acts_immutable();

CREATE FUNCTION credential_act_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  pr     auth_proofs%ROWTYPE;
  rq     credential_acts%ROWTYPE;
  target UUID;
  soc    UUID;
BEGIN
  PERFORM auth_server_time(NEW.at, 'credential_acts_at_is_now');
  IF NEW.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('credential_acts_at_is_now', 'act_xid is written by the server');
  END IF;

  -- The actor's own, fresh, unused PIN proof. A code is not an attribution.
  SELECT * INTO pr FROM auth_proofs WHERE id = NEW.proof_id FOR UPDATE;
  IF NOT FOUND OR pr.method <> 'pin' OR pr.user_id <> NEW.actor_user_id
     OR pr.at < auth_now() - interval '5 minutes' OR pr.consumed_at IS NOT NULL THEN
    PERFORM auth_refuse('act_needs_pin_proof',
      'an attributable act needs the actor''s own PIN, entered in the last five minutes and not already used');
  END IF;
  UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = pr.id;

  target := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));

  CASE NEW.kind
  WHEN 'pin_change' THEN
    IF target IS DISTINCT FROM NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'a PIN change is the person''s own act');
    END IF;
    IF NEW.new_pin_hash IS NULL OR NEW.new_pin_hash !~ '^\$argon2id\$v=19\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$' THEN
      PERFORM auth_refuse('pin_is_argon2id', 'the new PIN must be an argon2id hash');
    END IF;
    IF (SELECT user_id FROM auth_sessions WHERE id = NEW.actor_session_id AND ended_at IS NULL)
       IS DISTINCT FROM NEW.actor_user_id THEN
      PERFORM auth_refuse('session_ended_by_owner', 'a PIN change is made from the person''s own live session');
    END IF;

  WHEN 'member_reset' THEN
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a reset states its reason');
    END IF;
    IF NOT officer_over(NEW.actor_user_id, target) THEN
      PERFORM auth_refuse('reset_by_current_officer',
        'a reset is made by a current officer of the member''s society (not the read-only auditor)');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'an officer never resets themselves, through any member record');
    END IF;
    IF person_holds_post(target, NULL, true) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'a signatory is reset by two OTHER current signatories (D-15)');
    END IF;

  WHEN 'phone_change' THEN
    IF NEW.target_member_id IS NULL OR NEW.new_phone IS NULL THEN
      PERFORM auth_refuse('phone_change_attributable', 'a phone change names the member record and the new number');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('phone_change_not_self',
        'a member never changes their own registered number: a code to the old phone is not identity');
    END IF;
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a phone change states its reason');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = NEW.target_member_id);
    IF NOT person_holds_post(NEW.actor_user_id, soc, false, true) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a phone change is made by a current officer of the society');
    END IF;
    IF person_holds_post(target, soc, true) THEN
      PERFORM auth_refuse('signatory_phone_needs_two', 'a signatory''s number is changed by two OTHER current signatories');
    END IF;

  WHEN 'lock_release' THEN
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a release states its reason');
    END IF;
    IF NOT officer_over(NEW.actor_user_id, target) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a lock is released by a current officer of the member''s society');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('release_not_self', 'an officer never releases their own lock');
    END IF;

  WHEN 'signatory_reset_request', 'signatory_phone_request' THEN
    IF NEW.target_member_id IS NULL THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'a request names the signatory''s member record');
    END IF;
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a request states its reason');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = NEW.target_member_id);
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'nobody raises their own reset');
    END IF;
    IF NOT person_holds_post(NEW.actor_user_id, soc, false, true) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a request is raised by a current officer of the society');
    END IF;
    IF NOT person_holds_post(target, soc, true) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'this path is for a current signatory; use a member reset');
    END IF;
    IF NEW.kind = 'signatory_phone_request' AND NEW.new_phone IS NULL THEN
      PERFORM auth_refuse('phone_change_attributable', 'a phone request names the new number');
    END IF;

  WHEN 'signatory_approval' THEN
    SELECT * INTO rq FROM credential_acts WHERE id = NEW.request_id;
    IF NOT FOUND OR rq.kind NOT IN ('signatory_reset_request', 'signatory_phone_request') THEN
      PERFORM auth_refuse('reset_approver_is_signatory', 'an approval approves a signatory request');
    END IF;
    IF NEW.actor_user_id = rq.target_user_id
       OR NEW.actor_user_id = (SELECT user_id FROM members WHERE id = rq.target_member_id) THEN
      PERFORM auth_refuse('reset_approver_not_target', 'the signatory concerned never approves their own request');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = rq.target_member_id);
    IF NOT person_holds_post(NEW.actor_user_id, soc, true) THEN
      PERFORM auth_refuse('reset_approver_is_signatory',
        'an approver holds a current signing post in the signatory''s society');
    END IF;
    IF EXISTS (SELECT 1 FROM credential_acts WHERE request_id = NEW.request_id AND kind = 'signatory_approval'
                  AND actor_user_id = NEW.actor_user_id) THEN
      PERFORM auth_refuse('reset_one_approval_per_person', 'one approval per person');
    END IF;
    IF auth_now() > rq.at + interval '48 hours' THEN
      PERFORM auth_refuse('reset_request_expired', 'approvals count only within 48 hours of the request');
    END IF;
    IF pr.at < rq.at THEN
      PERFORM auth_refuse('approval_after_request', 'an approval rests on a PIN entered after the request existed');
    END IF;
  ELSE
    NULL;   -- an unknown kind is refused by the column's CHECK, by name
  END CASE;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_admissible BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION credential_act_admissible();

-- Effects, as one act with the record of it.
CREATE FUNCTION auth_reset_person(p_user UUID) RETURNS void AS $$
  DELETE FROM auth_credentials WHERE user_id = p_user;
  UPDATE auth_sessions SET ended_at = auth_now() WHERE user_id = p_user AND ended_at IS NULL;
  UPDATE auth_devices SET revoked_at = auth_now() WHERE user_id = p_user AND revoked_at IS NULL;
$$ LANGUAGE sql;

CREATE FUNCTION credential_act_effect() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  rq     credential_acts%ROWTYPE;
  target UUID := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));
BEGIN
  CASE NEW.kind
  WHEN 'pin_change' THEN
    INSERT INTO auth_credentials (user_id, pin_hash) VALUES (NEW.actor_user_id, NEW.new_pin_hash)
      ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = auth_now();
    -- Other devices are asked for the new PIN: their sessions end, the devices stay known.
    UPDATE auth_sessions SET ended_at = auth_now()
     WHERE user_id = NEW.actor_user_id AND ended_at IS NULL AND id <> NEW.actor_session_id;
  WHEN 'member_reset' THEN
    PERFORM auth_reset_person(target);
  WHEN 'phone_change' THEN
    UPDATE members SET phone = NEW.new_phone WHERE id = NEW.target_member_id;
  WHEN 'signatory_approval' THEN
    SELECT * INTO rq FROM credential_acts WHERE id = NEW.request_id;
    IF (SELECT count(DISTINCT actor_user_id) FROM credential_acts
         WHERE request_id = NEW.request_id AND kind = 'signatory_approval') = 2 THEN
      IF rq.kind = 'signatory_reset_request' THEN
        PERFORM auth_reset_person(coalesce(rq.target_user_id, (SELECT user_id FROM members WHERE id = rq.target_member_id)));
      ELSE
        UPDATE members SET phone = rq.new_phone WHERE id = rq.target_member_id;
      END IF;
    END IF;
  ELSE
    NULL;
  END CASE;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_effect AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION credential_act_effect();

-- A registered number changes only as the attributable act recorded, in the
-- same transaction, says: a phone_change to exactly this number, or the
-- second approval of a signatory phone request for exactly this number.
CREATE FUNCTION members_phone_attributable() RETURNS trigger AS $$
BEGIN
  IF NEW.phone IS NOT DISTINCT FROM OLD.phone THEN
    RETURN NEW;
  END IF;
  IF EXISTS (SELECT 1 FROM credential_acts c
              WHERE c.act_xid = pg_current_xact_id() AND c.kind = 'phone_change'
                AND c.target_member_id = NEW.id AND c.new_phone = NEW.phone)
  OR EXISTS (SELECT 1 FROM credential_acts a JOIN credential_acts rq ON rq.id = a.request_id
              WHERE a.act_xid = pg_current_xact_id() AND a.kind = 'signatory_approval'
                AND rq.kind = 'signatory_phone_request' AND rq.target_member_id = NEW.id AND rq.new_phone = NEW.phone) THEN
    RETURN NEW;
  END IF;
  PERFORM auth_refuse('phone_change_attributable',
    'a registered number changes only by an attributable act recording exactly this number');
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER members_phone_attributable BEFORE UPDATE OF phone ON members
  FOR EACH ROW EXECUTE FUNCTION members_phone_attributable();

COMMIT;
