-- DELIBERATELY WRONG. The guarantor's society is checked; whether their membership is active is not.
--
-- Must turn red: TestLoan17_GuarantorIsAnActiveMemberOfTheBorrowersSociety.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT society_id FROM members WHERE id = NEW.guarantor_member_id) <> (SELECT society_id FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_member_of_society'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
