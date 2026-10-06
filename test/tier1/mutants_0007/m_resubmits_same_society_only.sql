-- DELIBERATELY WRONG. A resubmission must point into its own society, but at any request, rejected or not.
--
-- Must turn red: TestApproval19_ResubmissionPointsAtARejectedRequest.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.resubmits_id IS NOT NULL AND (SELECT society_id FROM approval_requests WHERE id = NEW.resubmits_id) <> NEW.society_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'resubmits_a_rejected_request'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
