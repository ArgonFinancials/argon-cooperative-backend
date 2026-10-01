-- DELIBERATELY WRONG. Phone changes are attributable, never self-service, and
-- applied exactly as recorded — but ONE officer may change a signatory's
-- number.
--
-- The claim: every member-level rule is right, and the signatory case is
-- forgotten. An officer points a treasurer's registered number at their own
-- phone, then "recovers" the treasurer's account with the code that now
-- arrives there, and holds the seat. D-15 revised protects the reset. This
-- is the same seat, entered by the side door.
--
-- Must turn red: TestAuthE4_OneOfficerCannotChangeASignatorysPhone.
CREATE FUNCTION m_phone_act() RETURNS trigger AS $$
DECLARE
  target uuid;
BEGIN
  IF NEW.kind <> 'phone_change' THEN
    RETURN NEW;
  END IF;
  target := (SELECT user_id FROM members WHERE id = NEW.target_member_id);
  IF target = NEW.actor_user_id THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'phone_change_not_self';
  END IF;
  -- (missing: a current signatory's number needs two other signatories)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_phone_act BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_phone_act();

CREATE FUNCTION m_phone_apply() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'phone_change' THEN
    UPDATE members SET phone = NEW.new_phone WHERE id = NEW.target_member_id;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_phone_apply AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_phone_apply();
