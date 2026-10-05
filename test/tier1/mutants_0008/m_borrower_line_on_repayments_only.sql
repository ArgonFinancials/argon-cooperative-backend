-- DELIBERATELY WRONG. A repayment's loan line must be the borrower's; a disbursement's need not.
--
-- Must turn red: TestLoan29_DisbursementIsToTheBorrower.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE e ledger_entries%ROWTYPE; BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind = 'loan_repayment' AND (SELECT role FROM accounts WHERE id = NEW.account_id) = 'member_loan'
     AND NEW.member_id IS DISTINCT FROM (SELECT member_id FROM loans WHERE id = e.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_line_is_borrowers'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m_f();
