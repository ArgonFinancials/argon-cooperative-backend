-- DELIBERATELY WRONG. A repayment must name its loan; a disbursement need not.
--
-- Must turn red: TestLoan24_LoanMovementNamesItsLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind = 'loan_repayment' AND NEW.loan_id IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
