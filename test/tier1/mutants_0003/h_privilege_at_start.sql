-- DELIBERATELY WRONG. A session's length is fixed when it starts, from the
-- privileges the person held then.
--
-- The claim: "the highest privilege the person holds" read as "held at
-- sign-in". A member signs in (30 days), is appointed treasurer an hour
-- later, and carries signing power on a 30-day session for the rest of the
-- month.
--
-- Must turn red: TestAuthH9_AppointedMidSessionShrinksToEightHours.
CREATE FUNCTION m_session_expiry() RETURNS trigger AS $$
BEGIN
  NEW.expires_at := NEW.started_at + CASE WHEN EXISTS (
      SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
       WHERE m.user_id = NEW.user_id
         AND a.held_from <= auth_now()::date AND (a.held_until IS NULL OR a.held_until >= auth_now()::date))
    THEN interval '8 hours' ELSE interval '30 days' END;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_session_expiry BEFORE INSERT ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION m_session_expiry();

CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s
     WHERE s.token_digest = p_token_digest AND s.ended_at IS NULL
       AND auth_now() < s.expires_at);       -- (missing: privileges gained since start shrink it)
$$ LANGUAGE sql STABLE;
