-- DELIBERATELY WRONG (over-strict). A re-send must wait two minutes, not
-- sixty seconds.
--
-- The claim: the re-send control must be able to FAIL. A member on a weak
-- signal whose first code never arrived asks again after a minute, as the
-- screen tells them they may ("a code can be re-sent once every 60
-- seconds"). The rule and the screen must agree.
--
-- Must turn red: TestAuthF7_Control_ResendAfterSixtySeconds.
CREATE FUNCTION m_code_resend() RETURNS trigger AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM auth_codes WHERE user_id = NEW.user_id
                AND created_at > auth_now() - interval '120 seconds') THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'code_resend_interval';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_code_resend BEFORE INSERT ON auth_codes
  FOR EACH ROW EXECUTE FUNCTION m_code_resend();
