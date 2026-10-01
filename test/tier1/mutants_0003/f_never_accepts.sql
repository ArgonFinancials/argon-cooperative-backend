-- DELIBERATELY WRONG (over-strict). No code is ever accepted.
--
-- The claim: the code control must be able to FAIL. The right code, on its
-- first attempt, inside its five minutes, has to be accepted. Otherwise the
-- control only proves that code_verify can be called.
--
-- Must turn red: TestAuthF_Control_RightCodeInTime.
CREATE OR REPLACE FUNCTION code_verify(p_code UUID, p_digest TEXT) RETURNS BOOLEAN AS $$
BEGIN
  RETURN false;
END $$ LANGUAGE plpgsql VOLATILE;
