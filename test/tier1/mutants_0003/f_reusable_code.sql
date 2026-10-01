-- DELIBERATELY WRONG. A code expires and caps attempts, but is never marked
-- used.
--
-- The claim: a right code accepted twice. Whoever sees it over a shoulder,
-- or in a forwarded SMS, uses it again within the five minutes and trusts a
-- second device on the member's one act. One code, one device.
--
-- Must turn red: TestAuthF6_CodeIsSingleUse.
CREATE OR REPLACE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN AS $$
DECLARE
  c auth_codes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM auth_codes WHERE id = p_code FOR UPDATE;
  IF NOT FOUND THEN RETURN false; END IF;
  -- (missing: c.consumed_at IS NULL, and marking it consumed on success)
  IF auth_now() > c.created_at + interval '5 minutes' THEN RETURN false; END IF;  -- expiry
  IF c.attempts >= 3 THEN RETURN false; END IF;                                   -- three attempts
  UPDATE auth_codes SET attempts = attempts + 1 WHERE id = p_code;
  RETURN c.code_digest = p_digest;
END $$ LANGUAGE plpgsql VOLATILE;
