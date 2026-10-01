-- DELIBERATELY WRONG. A code expires and is single-use, but may be guessed at
-- without limit inside its five minutes.
--
-- The claim: a six-digit code has a million values. Without an attempt cap,
-- a script tries them in the window: expiry alone limits how long, not how
-- often. Three attempts is what makes a code worth one in 333,333, rather
-- than a certainty for anyone patient for five minutes.
--
-- Must turn red: TestAuthF5_FourthAttemptRefusedEvenIfRight.
CREATE OR REPLACE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN AS $$
DECLARE
  c auth_codes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM auth_codes WHERE id = p_code FOR UPDATE;
  IF NOT FOUND OR c.consumed_at IS NOT NULL THEN RETURN false; END IF;   -- single use
  IF auth_now() > c.created_at + interval '5 minutes' THEN RETURN false; END IF;  -- expiry
  -- (missing: c.attempts < 3)
  UPDATE auth_codes SET attempts = attempts + 1 WHERE id = p_code;
  IF c.code_digest = p_digest THEN
    UPDATE auth_codes SET consumed_at = auth_now() WHERE id = p_code;
    RETURN true;
  END IF;
  RETURN false;
END $$ LANGUAGE plpgsql VOLATILE;
