-- DELIBERATELY WRONG. OVER-STRICT: a write-off is approved against the society's CURRENT quorum, so a later bye-law change re-judges a decision already taken.
--
-- Must turn red: TestWriteOff_Control_ALaterQuorumDoesNotRejudge.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = NEW.loan_id AND r.kind::text = 'loan_write_off'
        AND (SELECT count(DISTINCT sa.user_id) FROM signatory_approvals sa WHERE sa.request_id = r.id) >= (SELECT write_off_signatures_required FROM societies WHERE id = r.society_id)) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
