-- DELIBERATELY WRONG (over-strict for members). Every session lasts 8 hours.
--
-- The claim: the officer rule applied to everyone. Members, many on weak
-- signal, sign in again every working day, and each sign-in on a new
-- device needs an SMS that may not arrive. The decided member length is 30
-- days on a known device.
--
-- Must turn red: TestAuthH6_MemberSessionLastsThirtyDays.
CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth_sessions s
     WHERE s.token_digest = p_token_digest AND s.ended_at IS NULL
       AND auth_now() < s.started_at + interval '8 hours');   -- (missing: 30 d for someone holding no post)
$$ LANGUAGE sql STABLE;
