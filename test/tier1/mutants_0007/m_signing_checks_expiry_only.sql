-- DELIBERATELY WRONG. Signing refuses an expired request, but not a rejected one.
--
-- Must turn red: TestApproval12_SigningARejectedRequestIsRefused.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE r approval_requests%ROWTYPE; BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id;
  IF now() > r.expires_at THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_before_expiry'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m_f();
