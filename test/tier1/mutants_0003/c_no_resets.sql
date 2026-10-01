-- DELIBERATELY WRONG (over-strict). No officer may ever reset a member.
--
-- The claim: the reset control must be able to FAIL. A legitimate reset (a
-- current officer of the member's society, a reason, their own fresh PIN
-- proof) has to be accepted. If the control passes here, it only checks
-- that the INSERT parses.
--
-- Must turn red: TestAuthC_Control_OfficerResetsMember.
CREATE FUNCTION m_reset() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'member_reset' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_by_current_officer';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_reset BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_reset();
