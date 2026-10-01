-- DELIBERATELY WRONG. A revoked device cannot open a new session, but its
-- revocation can be undone.
--
-- The claim: the check is on the session, but the revocation it depends on is
-- just a column. Clear revoked_at and the stolen phone is trusted again:
-- PIN only, no code. A revocation that can be reversed is a suspension.
--
-- Must turn red: TestAuthH4_RevokedDeviceCannotRevive.
CREATE FUNCTION m_session_device() RETURNS trigger AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM auth_devices WHERE id = NEW.device_id AND revoked_at IS NOT NULL) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'session_on_revoked_device';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_session_device BEFORE INSERT ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION m_session_device();

-- (missing: BEFORE UPDATE ON auth_devices — once revoked_at is set, it never changes)
