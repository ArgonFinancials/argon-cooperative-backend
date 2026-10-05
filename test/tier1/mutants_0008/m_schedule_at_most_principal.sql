-- DELIBERATELY WRONG. A schedule may not exceed the principal, but may fall short of it.
--
-- Must turn red: TestLoan6_ScheduleSumsToThePrincipal.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT sum(amount_kobo) FROM loan_instalments WHERE loan_id = NEW.loan_id) > (SELECT principal_kobo FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_sums_to_principal'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON loan_instalments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
