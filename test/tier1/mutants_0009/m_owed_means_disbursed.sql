-- DELIBERATELY WRONG. A write-off needs the loan disbursed, not money owed: a loan already repaid can be 'written off'.
--
-- Must turn red: TestWriteOff3_OnlyADisbursedLoanWithMoneyOwed.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = NEW.loan_id AND kind = 'loan_disbursement') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_needs_money_owed'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
