-- DELIBERATELY WRONG. An officer of the society may record a guarantee on a member's behalf: consent by proxy.
--
-- Must turn red: TestLoan13_GuaranteeRestsOnTheGuarantorsOwnAct.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE who UUID := (SELECT actor_user_id FROM credential_acts WHERE id = NEW.act_id); BEGIN
  IF who <> NEW.guarantor_user_id AND NOT person_holds_post(who, (SELECT society_id FROM loans WHERE id = NEW.loan_id)) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_is_own_act'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
