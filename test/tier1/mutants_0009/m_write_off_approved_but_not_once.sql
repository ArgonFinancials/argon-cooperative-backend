-- DELIBERATELY WRONG. A write-off entry needs an approved request; nothing stops a second one on the same approval.
--
-- Must turn red: TestWriteOff11_WrittenOffOnce.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = NEW.loan_id AND r.kind::text = 'loan_write_off' AND approval_status(r.id) = 'approved') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_approved'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m_f();
