-- DELIBERATELY WRONG. A write-off entry needs a signed write-off request, not an APPROVED one.
--
-- Must turn red: TestWriteOff8_TheEntryNeedsAnApprovedWriteOff.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NOT EXISTS (SELECT 1 FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id WHERE r.loan_id = NEW.loan_id AND r.kind::text = 'loan_write_off') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
