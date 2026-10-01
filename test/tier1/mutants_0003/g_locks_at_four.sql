-- DELIBERATELY WRONG (over-strict). The account locks at the FOURTH wrong PIN.
--
-- The claim: this breaks the NORMAL path, not the rule. A member who mistypes
-- four times and then gets it right must simply be signed in. Locking them
-- out is the control's failure. If G_Control passed here, it would only
-- prove that four failures and a success can be written to the table.
--
-- Must turn red: TestAuthG_Control_FourFailuresThenSuccess
--   (expected message: "REFUSED — four wrong PINs then a right one locked the account").
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
    IF r.succeeded THEN streak := 0; CONTINUE; END IF;
    IF lock_end IS NOT NULL AND r.at < lock_end THEN CONTINUE; END IF;
    streak := streak + 1;
    IF streak = 4 THEN                                   -- the error: four, not five
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
