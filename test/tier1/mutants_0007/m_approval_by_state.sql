-- DELIBERATELY WRONG. A request whose state column says 'approved' counts, whatever its signatures (finding 14).
--
-- Must turn red: TestLoan26_DisbursementNeedsAQuorumSignedBeforeExpiry.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind = 'loan_disbursement' AND NOT (EXISTS (SELECT 1 FROM approval_requests WHERE loan_id = NEW.loan_id AND state = 'approved') OR (SELECT count(DISTINCT sa.user_id) FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id WHERE r.loan_id = NEW.loan_id AND r.kind = 'loan_disbursement' AND sa.signed_at <= r.expires_at) >= (SELECT max(r.required) FROM approval_requests r WHERE r.loan_id = NEW.loan_id AND r.kind = 'loan_disbursement')) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
