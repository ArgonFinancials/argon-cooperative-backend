-- DELIBERATELY WRONG. "A member cannot change their own phone" checked on the
-- act's target_user_id field only.
--
-- The claim: a phone change names its target by MEMBER RECORD. With the
-- self-check on the user field, a member naming their own record changes
-- their own registered number. A code to the old phone is not identity,
-- so this lets whoever holds a member's phone move the account's codes to a
-- number of their choosing.
--
-- Must turn red: TestAuthE2_MemberCannotChangeOwnPhone.
CREATE FUNCTION m_phone_act() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'phone_change' AND NEW.target_user_id = NEW.actor_user_id THEN
    -- (missing: resolve the PERSON behind target_member_id and compare that)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'phone_change_not_self';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_phone_act BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_phone_act();
