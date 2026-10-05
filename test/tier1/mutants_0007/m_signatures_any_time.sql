-- DELIBERATELY WRONG. A quorum of signatures counts whenever they were made, after the request expired included.
--
-- Must turn red: TestLoan26_DisbursementNeedsAQuorumSignedBeforeExpiry.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind = 'loan_disbursement' AND (SELECT count(DISTINCT sa.user_id) FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id
        WHERE r.loan_id = NEW.loan_id AND r.kind = 'loan_disbursement') < (SELECT max(required) FROM approval_requests WHERE loan_id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
