-- DELIBERATELY WRONG. Due dates must differ, but need not increase with the instalment number.
--
-- Must turn red: TestLoan10_ScheduleDueDatesIncrease.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT count(*) - count(DISTINCT due_on) FROM loan_instalments WHERE loan_id = NEW.loan_id) > 0 THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_in_order'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON loan_instalments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
