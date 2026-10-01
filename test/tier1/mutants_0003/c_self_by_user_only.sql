-- DELIBERATELY WRONG. "An officer cannot reset themselves" checked on the
-- act's target_user_id field only.
--
-- The claim: an act can name its target by MEMBER RECORD alone. An officer
-- naming their own duplicate record leaves target_user_id empty, and the
-- check compares nothing. The rule is enforced on the wrong object, the same
-- shape as the auditor relink.
--
-- Must turn red:       TestAuthC4_OfficerCannotResetThemselvesThroughOwnOtherRecord
-- Expected to stay green: TestAuthC4_OfficerCannotResetThemselves (names the user)
CREATE FUNCTION m_reset() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'member_reset' AND NEW.target_user_id = NEW.actor_user_id THEN
    -- (missing: resolve the PERSON behind target_member_id and compare that)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_not_self';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_reset BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_reset();
