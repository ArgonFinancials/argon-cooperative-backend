-- DELIBERATELY WRONG. The schedule closes at disbursement, not at agreement: an agreed, undisbursed loan can still grow.
--
-- Must turn red: TestLoan9_ScheduleCannotGrowAfterAgreement.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = NEW.loan_id AND kind = 'loan_disbursement') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_closed'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_instalments FOR EACH ROW EXECUTE FUNCTION m_f();
