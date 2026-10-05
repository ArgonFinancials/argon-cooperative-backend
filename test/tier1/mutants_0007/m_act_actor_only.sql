-- DELIBERATELY WRONG. The act must be the guarantor's own, but may be an act of any kind.
--
-- Must turn red: TestLoan14_GuaranteeRestsOnAGuaranteeAct.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT actor_user_id FROM credential_acts WHERE id = NEW.act_id) <> NEW.guarantor_user_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_is_own_act'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
