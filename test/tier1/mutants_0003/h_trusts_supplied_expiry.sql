-- DELIBERATELY WRONG. A session uses the expiry the caller wrote, if any, and
-- the server's rule only as a default.
--
-- The claim: the posted_at hole in another place. Whoever creates the
-- session decides how long it lasts, so a year-long officer session is one
-- INSERT away. The expiry is the server's to compute, and a supplied one is
-- refused.
--
-- Must turn red: TestAuthH10_SessionExpiryIsTheServers.
CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s
     WHERE s.token_digest = p_token_digest AND s.ended_at IS NULL
       AND auth_now() < coalesce(s.expires_at,                          -- the error: caller's value first
             s.started_at + CASE WHEN EXISTS (
               SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
                WHERE m.user_id = s.user_id
                  AND a.held_from <= auth_now()::date AND (a.held_until IS NULL OR a.held_until >= auth_now()::date))
             THEN interval '8 hours' ELSE interval '30 days' END));
$$ LANGUAGE sql STABLE;
