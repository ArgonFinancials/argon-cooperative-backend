-- DELIBERATELY WRONG. A reason must be given, but any text will do: it routes nowhere.
--
-- Must turn red: TestApproval8_RejectionGivesOneOfTheFourReasons.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF coalesce(trim(NEW.reason), '') = '' THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_reason_known'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
