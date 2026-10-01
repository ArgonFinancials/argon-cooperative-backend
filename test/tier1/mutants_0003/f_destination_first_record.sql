-- DELIBERATELY WRONG. A person's code goes to "their" phone: whichever of
-- their member records is found first.
--
-- The claim: this is the import duplicate at the code layer. When a person's
-- records disagree on their number, one of them is wrong, and possibly
-- planted. Sending to either sends a code to a number nobody has confirmed
-- is theirs. With no single agreed number, there is no code.
--
-- Must turn red: TestAuthF1_NoCodeWhenRecordsDisagreeOnPhone.
-- Expected to stay green: TestAuthF2_*, TestAuthF3_* (the destination check itself is right).
CREATE OR REPLACE FUNCTION otp_destination(p_user UUID) RETURNS TEXT AS $$
  SELECT phone FROM members WHERE user_id = p_user ORDER BY created_at, id LIMIT 1;
  -- (missing: NULL unless every one of the person's records carries the same number)
$$ LANGUAGE sql STABLE;

CREATE FUNCTION m_code_dest() RETURNS trigger AS $$
BEGIN
  IF NEW.sent_to IS NULL OR NEW.sent_to IS DISTINCT FROM otp_destination(NEW.user_id) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'code_goes_to_registered_phone';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_code_dest BEFORE INSERT ON auth_codes
  FOR EACH ROW EXECUTE FUNCTION m_code_dest();
