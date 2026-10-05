-- DELIBERATELY WRONG. Signing refuses a rejected request, but not an expired one.
--
-- Must turn red: TestApproval4_SigningAnExpiredRequestIsRefused.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE r approval_requests%ROWTYPE; BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id;
  IF EXISTS (SELECT 1 FROM approval_rejections WHERE request_id = r.id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_on_open_request'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m_f();
