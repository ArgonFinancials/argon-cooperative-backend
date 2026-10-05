-- DELIBERATELY WRONG. A withdrawal is refused after disbursement, but anyone's act may withdraw a guarantee.
--
-- Must turn red: TestLoan18_WithdrawalIsTheGuarantorsOwnAct.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM loan_guarantees g JOIN ledger_entries e ON e.loan_id = g.loan_id AND e.kind = 'loan_disbursement' WHERE g.id = NEW.guarantee_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_withdrawn_before_disbursement'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantee_withdrawals FOR EACH ROW EXECUTE FUNCTION m_f();
