-- DELIBERATELY WRONG. OVER-STRICT: every rejection needs a note, the three self-explanatory reasons included.
--
-- Must turn red: TestApproval_Control_NoNoteNeededExceptForLooksWrong.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF coalesce(trim(NEW.note), '') = '' THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_note_for_looks_wrong'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
