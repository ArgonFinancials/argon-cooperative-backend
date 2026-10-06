-- DELIBERATELY WRONG. An unset quorum is read as 2, the most common bye-law: nobody is told the society never decided.
--
-- Must turn red: TestWriteOff1_AnUnsetQuorumRefusesAndSaysSo.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND NEW.required <> coalesce((SELECT write_off_signatures_required FROM societies WHERE id = NEW.society_id), 2) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_quorum_is_societys'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
