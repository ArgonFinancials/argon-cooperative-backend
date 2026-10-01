-- DELIBERATELY WRONG. Codes go only to the one agreed registered number, but
-- may be re-sent without limit.
--
-- The claim: every re-send is a fresh three attempts. With no interval, a
-- guesser requests a new code whenever the old one's attempts run out, and
-- the attempt cap stops meaning anything. It also turns the sign-in page
-- into a way to flood a member's phone at Argon's expense. Sixty seconds.
--
-- Must turn red: TestAuthF7_ResendNotWithinSixtySeconds.
CREATE OR REPLACE FUNCTION otp_destination(p_user UUID) RETURNS TEXT AS $$
  SELECT CASE WHEN count(DISTINCT phone) = 1 THEN min(phone) END FROM members WHERE user_id = p_user;
$$ LANGUAGE sql STABLE;

CREATE FUNCTION m_code_dest() RETURNS trigger AS $$
BEGIN
  IF NEW.sent_to IS NULL OR NEW.sent_to IS DISTINCT FROM otp_destination(NEW.user_id) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'code_goes_to_registered_phone';
  END IF;
  -- (missing: no earlier code for this person within the last 60 seconds)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_code_dest BEFORE INSERT ON auth_codes
  FOR EACH ROW EXECUTE FUNCTION m_code_dest();
