-- DELIBERATELY WRONG. Lockouts escalate (15, 30, 60 minutes...) but always
-- release themselves; none ever waits for an officer.
--
-- The claim: escalation alone still lets a determined guesser through,
-- slowly. The third lockout in a day is the signal that someone other than
-- the member is trying, and the decision was that it waits for a person who
-- can confirm who the member is.
--
-- Must turn red: TestAuthG5_ThirdLockoutInADayNeedsAnOfficer.
CREATE OR REPLACE FUNCTION lock_state(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
DECLARE
  r         record;
  streak    int := 0;
  starts    timestamptz[] := '{}';
  lock_end  timestamptz;
  k         int;
BEGIN
  locked_until := NULL;
  needs_officer := false;
  FOR r IN SELECT at, succeeded FROM sign_in_attempts
            WHERE user_id = p_user AND at > auth_now() - interval '48 hours'
            ORDER BY at LOOP
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 5 THEN
      streak := 0;
      k := 1 + (SELECT count(*) FROM unnest(starts) s WHERE s > r.at - interval '24 hours');
      starts := starts || r.at;
      -- (missing: k >= 3 → locked until an officer releases it)
      lock_end := r.at + interval '15 minutes' * power(2, k - 1);
    END IF;
  END LOOP;
  IF lock_end IS NOT NULL AND lock_end > auth_now() THEN locked_until := lock_end; END IF;
END $$ LANGUAGE plpgsql STABLE;
