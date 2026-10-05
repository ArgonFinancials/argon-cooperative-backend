-- DELIBERATELY WRONG. A rejection cannot be edited, but it can be deleted, reopening the request.
--
-- Must turn red: TestApproval15_RejectionsAreImmutable.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'approval_rejections_immutable';
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE UPDATE ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
