-- DELIBERATELY WRONG. A disbursement needs a signature on its request, not an APPROVED request.
--
-- Must turn red: TestLoan26_DisbursementNeedsAnApprovedRequest.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind = 'loan_disbursement' AND NOT EXISTS (SELECT 1 FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id WHERE r.loan_id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
