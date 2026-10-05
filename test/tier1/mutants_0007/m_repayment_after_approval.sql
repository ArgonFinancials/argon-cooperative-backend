-- DELIBERATELY WRONG. A repayment needs the loan to be APPROVED, not disbursed.
--
-- Must turn red: TestLoan30_NoRepaymentBeforeDisbursement.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind = 'loan_repayment' AND NOT ((SELECT count(DISTINCT sa.user_id) FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id WHERE r.loan_id = NEW.loan_id AND r.kind = 'loan_disbursement' AND sa.signed_at <= r.expires_at) >= (SELECT max(r.required) FROM approval_requests r WHERE r.loan_id = NEW.loan_id AND r.kind = 'loan_disbursement')) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_repaid_after_disbursement'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
