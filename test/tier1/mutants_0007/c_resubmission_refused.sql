-- DELIBERATELY WRONG. OVER-STRICT: no request may point at another, so a resubmission cannot carry its original's age.
--
-- Must turn red: TestApproval_Control_ResubmittingARejectedRequest.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.resubmits_id IS NOT NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'resubmits_a_rejected_request'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
