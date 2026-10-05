-- DELIBERATELY WRONG. Guarantees are counted, withdrawn ones included.
--
-- Must turn red: TestLoan20_RequestNeedsTheProductsGuarantors.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.loan_id IS NOT NULL AND (SELECT count(*) FROM loan_guarantees WHERE loan_id = NEW.loan_id) < (SELECT guarantors_required FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
