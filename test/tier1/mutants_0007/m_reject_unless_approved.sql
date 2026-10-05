-- DELIBERATELY WRONG. A rejection is refused on an approved request, but an expired one can still be rejected.
--
-- Must turn red: TestApproval14_RejectingAnExpiredRequestIsRefused.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE r approval_requests%ROWTYPE; BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id;
  IF (SELECT count(DISTINCT user_id) FROM signatory_approvals WHERE request_id = r.id) >= r.required THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_on_open_request'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
