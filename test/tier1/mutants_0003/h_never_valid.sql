-- DELIBERATELY WRONG (over-strict). No session is ever valid.
--
-- The claim: this breaks the NORMAL path. A session just created, unended,
-- on a trusted device, must be usable. If H_Control passed here it would
-- only prove that session_valid can be called, not that a fresh session
-- works.
--
-- Must turn red: TestAuthH_Control_FreshSessionValid
--   (expected message: "REFUSED — a new session is not valid").
CREATE OR REPLACE FUNCTION session_valid(p_token_digest TEXT) RETURNS BOOLEAN AS $$
  SELECT false;
$$ LANGUAGE sql STABLE;
