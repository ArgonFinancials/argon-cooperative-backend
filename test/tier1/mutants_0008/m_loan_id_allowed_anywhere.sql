-- DELIBERATELY WRONG. Loan movements must name a loan; nothing stops any other movement naming one.
--
-- Must turn red: TestLoan25_OnlyLoanMovementsNameALoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind IN ('loan_disbursement', 'loan_repayment') AND NEW.loan_id IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
