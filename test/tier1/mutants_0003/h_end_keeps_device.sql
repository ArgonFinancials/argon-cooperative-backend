-- DELIBERATELY WRONG. Ending a session from elsewhere ends that session, and
-- nothing else: the device stays trusted.
--
-- The claim: this is addition 4. A member, from another phone, ends the
-- session on the phone that was stolen. The session dies, but the stolen
-- phone is still a known device. A thief who saw the PIN signs straight back
-- in, PIN only, with no code. "End this session" must also stop the device
-- being known.
--
-- Must turn red: TestAuthH3_MemberEndsStolenDeviceFromElsewhere
--   (expected message: "...the stolen phone is still a trusted device").
CREATE OR REPLACE FUNCTION device_trusted(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (SELECT 1 FROM auth_devices WHERE token_digest = p_token_digest AND revoked_at IS NULL);
$$ LANGUAGE sql STABLE;

CREATE FUNCTION m_end() RETURNS trigger AS $$
BEGIN
  IF OLD.ended_at IS NOT NULL THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'session_end_is_final';
  END IF;
  IF NEW.ended_at IS NOT NULL AND NEW.ended_by_session IS NOT NULL
     AND (SELECT user_id FROM auth_sessions WHERE id = NEW.ended_by_session) <> OLD.user_id THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'session_ended_by_owner';
  END IF;
  -- (missing: ended from ANOTHER session → revoke this session's device)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_end BEFORE UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION m_end();
