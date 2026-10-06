-- DELIBERATELY WRONG. A recovery needs a disbursed loan, not a written-off one.
--
-- Must turn red: TestWriteOff14_ARecoveryNeedsAWrittenOffLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_recovery' AND NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = NEW.loan_id AND kind = 'loan_disbursement') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'recovery_needs_write_off'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
