-- DELIBERATELY WRONG. Every lockout ever counts towards escalation; the 24 h
-- window is missing.
--
-- The claim: a member who mistyped their PIN five times last month is
-- treated as under attack today. Their next ordinary slip costs thirty
-- minutes, and the one after needs an officer. Too strict, which is the
-- point: a lockout people hit in normal use teaches them to call an officer
-- for nothing, and officers to release without looking.
--
-- Must turn red: TestAuthG8_LockoutsOlderThanADayDoNotEscalate.
CREATE OR REPLACE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
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
            WHERE user_id = p_user                     -- (missing: only recent history)
            ORDER BY at LOOP
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + cardinality(starts);                    -- (missing: only lockouts in the last 24 h)
      starts := starts || r.at;
      last_lock := r.at;
      IF k >= 3 THEN officer := true;  lock_end := 'infinity';
      ELSE            officer := false; lock_end := r.at + interval '15 minutes' * power(2, k - 1);
      END IF;
    END IF;
  END LOOP;
  IF officer THEN
    needs_officer := NOT EXISTS (SELECT 1 FROM credential_acts
                                  WHERE kind = 'lock_release' AND target_user_id = p_user AND at >= last_lock);
    RETURN;
  END IF;
  IF lock_end IS NOT NULL AND lock_end > auth_now() THEN locked_until := lock_end; END IF;
END $$ LANGUAGE plpgsql STABLE;
