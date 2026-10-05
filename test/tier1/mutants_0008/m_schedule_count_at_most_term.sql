-- DELIBERATELY WRONG. No more instalments than months, but fewer is accepted.
--
-- Must turn red: TestLoan7_ScheduleHasOneInstalmentPerMonthOfTerm.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT count(*) FROM loan_instalments WHERE loan_id = NEW.loan_id) > (SELECT term_months FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_matches_term'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON loan_instalments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
