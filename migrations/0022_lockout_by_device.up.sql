-- =============================================================================
-- ARGON — 0022: THE LOCKOUT, SPLIT BY DEVICE
--
-- Written against test/tier1/tier1_0022_test.go (decided 2026-11-05; shown
-- red on today's lock first). Mutation-tested with test/tier1/mutants_0022.
-- deploy/README.md findings 60 and 62.
--
-- 0003 kept ONE lock per account, fed by every wrong PIN from anywhere, and
-- its third lockout in 24 h waited for an officer. Two things were wrong:
--
--   ANYONE COULD REACH IT. Fifteen calls from anywhere, knowing only the
--   number, locked the member's own phone until an officer acted (finding 60).
--   On a device that is not the member's, a right PIN only sends a code to
--   the member's phone: the code already does what the officer step was for,
--   so there the officer step protected nothing and was the one part a
--   stranger could trigger.
--
--   IT NEVER LASTED (finding 62). The lock was computed from the last 48
--   hours of attempts: a window written for counting RECENT failures that
--   silently became the officer lock's lifetime. Two days on, the failures
--   behind it were no longer read and the lock was gone: a thief holding the
--   phone got fifteen more guesses every two days, and anyone could re-lock
--   a member for ever. "Waits for an officer" was never true.
--
-- NOW, TWO LOCKS PER ACCOUNT, neither resetting the other:
--   OWN DEVICES (an attempt naming a device this server trusted for the
--     member: sign-in on it, and every step-up, which runs on a session's
--     device): 5 failures lock 15 min, doubling within 24 h; the third waits
--     for an officer, however long that takes.
--   ANY OTHER DEVICE (no device named), and an UNREGISTERED number exactly
--     alike, so a lock still says nothing about which numbers are registered:
--     15 min, 30 min, then 48 hours. Never the officer.
-- AN OFFICER'S RELEASE (lock_release, 0003's rules) clears BOTH: the member
-- is in front of the officer. Attempts before it are not counted again.
--
-- WHAT A LOCK READS, never before the latest release:
--   OWN DEVICES: everything from 72 hours (24 h of escalation + the longest
--     timed lock) before the latest success on them, and EVERYTHING if there
--     has never been one. A locked account records no attempts (the service
--     answers before it writes one), so an officer lock's failures come after
--     the latest success and are always read, however old. Only a person
--     holding one of the member's own devices adds to it; in daily use it
--     reads about three days.
--   ANY OTHER DEVICE: the last 72 hours. Its longest lock is 48, and a
--     stranger must not be able to make every sign-in read their whole history.
-- =============================================================================

BEGIN;

CREATE FUNCTION lock_ladder(p_user UUID, p_digest TEXT, p_own BOOLEAN,
                            OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
DECLARE
  r         record;
  streak    int := 0;
  starts    timestamptz[] := '{}';
  lock_end  timestamptz;
  officer   boolean := false;
  k         int;
  released  timestamptz;
  last_ok   timestamptz;
  since     timestamptz;   -- what this lock reads: the header
BEGIN
  locked_until := NULL;
  needs_officer := false;
  IF p_user IS NOT NULL THEN
    SELECT max(ca.at) INTO released FROM credential_acts ca
     WHERE ca.kind = 'lock_release'
       AND coalesce(ca.target_user_id, (SELECT m.user_id FROM members m WHERE m.id = ca.target_member_id)) = p_user;
  END IF;
  IF p_own THEN
    SELECT max(a.at) INTO last_ok FROM sign_in_attempts a
     WHERE a.succeeded AND a.user_id = p_user AND a.device_id IS NOT NULL;
    since := coalesce(last_ok - interval '72 hours', '-infinity');
  ELSE
    since := auth_now() - interval '72 hours';
  END IF;
  IF released IS NOT NULL AND released > since THEN
    since := released;
  END IF;

  FOR r IN SELECT a.at, a.succeeded FROM sign_in_attempts a
            WHERE (CASE WHEN p_user IS NOT NULL THEN a.user_id = p_user AND (a.device_id IS NOT NULL) = p_own
                        ELSE a.user_id IS NULL AND a.credential_digest = p_digest END)
              AND a.at > since
            ORDER BY a.at, a.seq LOOP
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + (SELECT count(*) FROM unnest(starts) s WHERE s > r.at - interval '24 hours');
      starts := starts || r.at;
      IF k >= 3 AND p_own THEN
        officer := true;  lock_end := 'infinity';
      ELSIF k >= 3 THEN
        officer := false; lock_end := r.at + interval '48 hours';
      ELSE
        officer := false; lock_end := r.at + interval '15 minutes' * power(2, k - 1);
      END IF;
    END IF;
  END LOOP;
  IF officer THEN
    needs_officer := true;   -- a release after it moved `since` past it
    RETURN;
  END IF;
  IF lock_end IS NOT NULL AND lock_end > auth_now() THEN
    locked_until := lock_end;
  END IF;
END $$ LANGUAGE plpgsql STABLE;

-- The member's OWN devices: sign-in on a trusted device, and the step-up.
CREATE OR REPLACE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_ladder(p_user, NULL, true);
$$ LANGUAGE sql STABLE;

-- Any other device: sign-in naming no device of the member's.
CREATE OR REPLACE FUNCTION lock_state_unknown_device(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_ladder(p_user, NULL, false);
$$ LANGUAGE sql STABLE;

-- An unregistered number, or one shared by two people: as any other device.
CREATE OR REPLACE FUNCTION lock_state_for_credential(p_digest TEXT, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_ladder(NULL, p_digest, false);
$$ LANGUAGE sql STABLE;

-- 0003's single lock, with the 48-hour window: nothing calls it now, and a
-- function that answers with the old rule is a trap for the next caller.
DROP FUNCTION lock_state_of(UUID, TEXT);

-- A RELEASE NEEDS A LOCK. A release forgives every failure before it, so one
-- made while nothing is locked is an act on nothing that hands whoever holds
-- the phone a fresh count. 0003's rules (an officer of the member's society,
-- never their own, a reason) stand beside this.
CREATE FUNCTION lock_release_needs_a_lock() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  target UUID;
  o RECORD;
  e RECORD;
BEGIN
  IF NEW.kind <> 'lock_release' THEN
    RETURN NEW;
  END IF;
  target := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));
  SELECT * INTO o FROM lock_state(target);
  SELECT * INTO e FROM lock_state_unknown_device(target);
  IF o.locked_until IS NULL AND NOT o.needs_officer AND e.locked_until IS NULL AND NOT e.needs_officer THEN
    PERFORM auth_refuse('release_needs_a_lock', 'this member is not locked out: there is nothing to release');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_release_needs_a_lock BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION lock_release_needs_a_lock();

COMMIT;
