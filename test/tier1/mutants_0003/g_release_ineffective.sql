-- DELIBERATELY WRONG. Lockout is computed correctly: five failures lock for
-- 15 minutes, doubling within 24 h, and the third lockout in 24 h waits for an
-- officer. But lock_state never looks at an officer's release.
--
-- The claim: this breaks the LOCK LOGIC, not the fixture. G6's fixture still
-- builds a real officer-tier lock (its precondition holds), and the
-- officer's release is accepted and recorded. Only the lock never reads it,
-- so the member stays locked forever with a release on file. If G6 goes red
-- here, it is red because the thing it guards (release ends the lock) is
-- broken.
--
-- G6 passed VACUOUSLY once, when no lock could exist. This mutant is the
-- proof that it now fails for the right reason.
--
-- Must turn red: TestAuthG6_OfficerReleaseEndsTheLock
--   (expected message: "REFUSED — the officer's release did not end the lock",
--    NOT a FIXTURE failure).
CREATE OR REPLACE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
DECLARE
  r         record;
  streak    int := 0;
  starts    timestamptz[] := '{}';
  lock_end  timestamptz;
  officer   boolean := false;
  k         int;
BEGIN
  locked_until := NULL;
  needs_officer := false;
  FOR r IN SELECT at, succeeded FROM sign_in_attempts
            WHERE user_id = p_user AND at > auth_now() - interval '48 hours'
            ORDER BY at LOOP
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;   -- failures during a lock
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + (SELECT count(*) FROM unnest(starts) s WHERE s > r.at - interval '24 hours');
      starts := starts || r.at;
      IF k >= 3 THEN
        officer := true;  lock_end := 'infinity';
      ELSE
        officer := false; lock_end := r.at + interval '15 minutes' * power(2, k - 1);
      END IF;
    END IF;
  END LOOP;
  IF officer THEN
    -- (missing: a lock_release act for this member after the lock began ends it)
    needs_officer := true;
    RETURN;
  END IF;
  IF lock_end IS NOT NULL AND lock_end > auth_now() THEN
    locked_until := lock_end;
  END IF;
END $$ LANGUAGE plpgsql STABLE;
