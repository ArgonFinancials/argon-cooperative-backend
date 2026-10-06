-- DELIBERATELY WRONG. OVER-STRICT: only a loan whose schedule has ended may be written off; the society carries a debt it knows is lost until a calendar date.
--
-- Must turn red: TestWriteOff_Control_AnInArrearsLoanWrittenOffThenRecovered.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND (SELECT max(due_on) FROM loan_instalments WHERE loan_id = NEW.loan_id) > current_date THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_needs_money_owed'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
