-- DELIBERATELY WRONG. A successful sign-in wipes the lockout history, not just
-- the current run of failures.
--
-- The claim: "a success clears the counter" applied to everything. One
-- correct guess between lockouts and every lockout is the first again:
-- always 15 minutes, never doubling, never reaching the officer tier.
-- Someone with a few candidate PINs cycles them forever.
--
-- Must turn red: TestAuthG4_SuccessDoesNotEraseLockoutHistory.
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
            WHERE user_id = p_user AND at > auth_now() - interval '48 hours'
            ORDER BY at LOOP
    IF r.succeeded THEN
      streak := 0;
      starts := '{}';                     -- the error: history erased with the streak
      CONTINUE;
    END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + (SELECT count(*) FROM unnest(starts) s WHERE s > r.at - interval '24 hours');
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
