-- DELIBERATELY WRONG. Ending a session is final and revokes the device — and
-- any session may end any other.
--
-- The claim: the end path is guarded on everything except whose it is. One
-- member's session ends another member's sessions and revokes their devices.
-- A nuisance at best; at worst a way to force a signatory onto a fresh
-- device sign-in (and a code to a phone) at a chosen moment. A session is
-- ended by its owner's own sessions, or by an officer's reset.
--
-- Must turn red: TestAuthH5_AnotherPersonCannotEndMySession.
CREATE FUNCTION m_end() RETURNS trigger AS $$
BEGIN
  IF OLD.ended_at IS NOT NULL THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'session_end_is_final';
  END IF;
  -- (missing: NEW.ended_by_session belongs to the same person)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_end BEFORE UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION m_end();
