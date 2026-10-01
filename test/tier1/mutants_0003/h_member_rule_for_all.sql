-- DELIBERATELY WRONG. Every session lasts 30 days, whoever holds it.
--
-- The claim: one length for everyone is the simplest rule and the wrong one.
-- An officer's or approver's session is a standing key to other people's
-- money; a member's is a key to their own statement. They get 8 hours,
-- including an approver who signed in on the member surface.
--
-- Must turn red: TestAuthH7_OfficerSessionLastsEightHours,
--                TestAuthH8_ApproverSignedInAsMemberGetsEightHours.
CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s
     WHERE s.token_digest = p_token_digest AND s.ended_at IS NULL
       AND auth_now() < s.started_at + interval '30 days');   -- (missing: 8 h for anyone holding a post)
$$ LANGUAGE sql STABLE;
