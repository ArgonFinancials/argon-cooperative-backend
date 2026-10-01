-- DELIBERATELY WRONG. A direct UPDATE of members.phone is allowed whenever a
-- phone_change act for that member exists in the same transaction —
-- whatever number the act records.
--
-- The claim: the attributable record says one number and the row gets
-- another. The audit trail shows the officer changing the phone to X; the
-- member's codes now go to Y. The record has to be the change, not
-- permission for any change.
--
-- Must turn red: TestAuthE5_AppliedNumberMustBeTheRecordedNumber.
-- Expected to stay green: TestAuthE1_DirectPhoneUpdateRefused,
--                         TestAuthE3_OfficerPhoneChangeAppliesTheRecordedNumber.
CREATE FUNCTION m_phone_apply() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'phone_change' THEN
    UPDATE members SET phone = NEW.new_phone WHERE id = NEW.target_member_id;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_phone_apply AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_phone_apply();

CREATE FUNCTION m_phone_guard() RETURNS trigger AS $$
BEGIN
  IF NEW.phone IS DISTINCT FROM OLD.phone
     AND NOT EXISTS (SELECT 1 FROM credential_acts c
                      WHERE c.kind = 'phone_change' AND c.target_member_id = NEW.id
                        AND c.xmin::text = pg_current_xact_id()::text
                        -- (missing: AND c.new_phone = NEW.phone)
                    ) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'phone_change_attributable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_phone_guard BEFORE UPDATE OF phone ON members
  FOR EACH ROW EXECUTE FUNCTION m_phone_guard();
