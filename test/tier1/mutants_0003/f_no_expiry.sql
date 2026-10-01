-- DELIBERATELY WRONG. A code is single-use and allows three attempts, but it
-- never expires.
--
-- The claim: an unexpired code is a standing key. One read off a phone's
-- lock screen, or from an SMS log, works tomorrow. Five minutes is what makes
-- "someone holds this phone" mean NOW.
--
-- Must turn red: TestAuthF4_CodeExpiresAfterFiveMinutes.
CREATE OR REPLACE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN AS $$
DECLARE
  c auth_codes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM auth_codes WHERE id = p_code FOR UPDATE;
  IF NOT FOUND OR c.consumed_at IS NOT NULL THEN RETURN false; END IF;   -- single use
  -- (missing: auth_now() <= c.created_at + interval '5 minutes')
  IF c.attempts >= 3 THEN RETURN false; END IF;                          -- three attempts
  UPDATE auth_codes SET attempts = attempts + 1 WHERE id = p_code;
  IF c.code_digest = p_digest THEN
    UPDATE auth_codes SET consumed_at = auth_now() WHERE id = p_code;
    RETURN true;
  END IF;
  RETURN false;
END $$ LANGUAGE plpgsql VOLATILE;
