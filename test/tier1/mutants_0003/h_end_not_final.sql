-- DELIBERATELY WRONG. Only the owner may end a session, and ending one from
-- elsewhere revokes its device. But an ended session can be revived.
--
-- The claim: "end session" as a state flag that can be flipped back. Whoever
-- holds the row (a support tool, a bug, a future endpoint) clears ended_at,
-- and the stolen phone's session lives again with nobody re-entering
-- anything. Ending is final.
--
-- Must turn red: TestAuthH2_EndedSessionCannotBeRevived.
CREATE FUNCTION m_end() RETURNS trigger AS $$
BEGIN
  -- (missing: OLD.ended_at IS NOT NULL → refuse any change)
  IF NEW.ended_at IS NOT NULL AND NEW.ended_by_session IS NOT NULL
     AND (SELECT user_id FROM auth_sessions WHERE id = NEW.ended_by_session) <> OLD.user_id THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'session_ended_by_owner';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_end BEFORE UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION m_end();
