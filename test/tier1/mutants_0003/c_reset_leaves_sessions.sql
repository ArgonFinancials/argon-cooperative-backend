-- DELIBERATELY WRONG. A member reset clears the PIN and nothing else.
--
-- The claim: "reset their PIN" read literally. The member's signed-in
-- devices keep working, so whoever was in the account (the reason for the
-- reset) stays in it. The requirement is "it clears their PIN AND every
-- signed-in device", as one act.
--
-- Must turn red: TestAuthC7_ResetClearsPinAndEndsEverySession.
CREATE FUNCTION m_reset_effect() RETURNS trigger AS $$
DECLARE
  target uuid := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));
BEGIN
  IF NEW.kind = 'member_reset' THEN
    DELETE FROM auth_credentials WHERE user_id = target;
    -- (missing: end every session, revoke every device)
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_reset_effect AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_reset_effect();
