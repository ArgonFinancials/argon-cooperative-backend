-- DELIBERATELY WRONG. The guarantor count is checked when the request is raised, and never again: a withdrawal after the signatures goes unnoticed.
--
-- Must turn red: TestLoan33_DisbursementRechecksTheGuarantors.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.loan_id IS NOT NULL AND (SELECT count(*) FROM loan_guarantees g WHERE g.loan_id = NEW.loan_id AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals w WHERE w.guarantee_id = g.id)) < (SELECT guarantors_required FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
