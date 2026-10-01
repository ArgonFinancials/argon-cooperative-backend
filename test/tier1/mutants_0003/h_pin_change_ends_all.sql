-- DELIBERATELY WRONG (over-strict). Changing the PIN signs out EVERY session,
-- including the one the change was made from.
--
-- The claim: "a PIN change ends other sessions" implemented as "ends all".
-- The member who just proved their old PIN and set a new one is thrown out
-- mid-act, and on a new device they need an SMS to get back in. The
-- decision, and the screen ("any other device signed in will be asked for
-- the new PIN"), keeps the current one.
--
-- Must turn red: TestAuthH1_PinChangeEndsOtherSessionsKeepsThisOne
--   (expected message: "REFUSED — changing the PIN signed out the device it was changed from").
CREATE FUNCTION m_pin_change() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'pin_change' THEN
    UPDATE auth_credentials SET pin_hash = NEW.new_pin_hash WHERE user_id = NEW.actor_user_id;
    UPDATE auth_sessions SET ended_at = auth_now()
     WHERE user_id = NEW.actor_user_id AND ended_at IS NULL;   -- (missing: AND id <> NEW.actor_session_id)
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_pin_change AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_pin_change();
