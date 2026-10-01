-- DELIBERATELY WRONG. A session's length is recomputed from the privileges
-- the person holds NOW: 8 hours if they hold a post, else 30 days.
--
-- The claim: this is the obvious fix for h_privilege_at_start, and it passes
-- H9 (appointed mid-session). Then it falls into the trap the decision
-- names: a treasurer whose term ends mid-session holds no post "now", so
-- their 8-hour session silently becomes a 30-day member one. Terms are
-- DATES, so ending one also makes the appointments table say it ended before
-- the session began. Nothing in "now" remembers the session was an
-- officer's. The decision is that it ends.
--
-- Must turn red: TestAuthH9_AppointmentEndingMidSessionEndsTheSession
--   (expected message: "...the session became a 30-day member session").
-- Expected to stay green: TestAuthH9_AppointedMidSessionShrinksToEightHours.
CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s
     WHERE s.token_digest = p_token_digest AND s.ended_at IS NULL
       AND auth_now() < s.started_at + CASE WHEN EXISTS (
             SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
              WHERE m.user_id = s.user_id
                AND a.held_from <= auth_now()::date AND (a.held_until IS NULL OR a.held_until >= auth_now()::date))
           THEN interval '8 hours' ELSE interval '30 days' END);
  -- (missing: a session that was an officer's and whose holder no longer holds a post is ended)
$$ LANGUAGE sql STABLE;
