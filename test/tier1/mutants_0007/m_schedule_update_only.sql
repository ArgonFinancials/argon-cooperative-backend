-- DELIBERATELY WRONG. An instalment cannot be edited, but it can be deleted.
--
-- Must turn red: TestLoan8_ScheduleIsImmutable.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_immutable';
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE UPDATE ON loan_instalments FOR EACH ROW EXECUTE FUNCTION m_f();
