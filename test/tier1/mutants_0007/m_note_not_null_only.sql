-- DELIBERATELY WRONG. 'looks wrong' needs a note, but a note of spaces counts as one.
--
-- Must turn red: TestApproval18_LooksWrongRequiresANote.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.reason = 'looks_wrong' AND NEW.note IS NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_note_for_looks_wrong'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
