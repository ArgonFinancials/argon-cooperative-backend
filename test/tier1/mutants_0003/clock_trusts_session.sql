-- DELIBERATELY WRONG. auth_now() honours a SESSION-level argon.test_clock.
--
-- The claim: this is the obvious way to build a test clock, and it hands the
-- app control of every auth window. Any connection can SET argon.test_clock
-- and argon.test_now, and move code expiry, lockout, the two-signatory
-- window and session length wherever it likes.
--
-- Must turn red: TestAuth0_SessionCannotEnableTheTestClock.
CREATE OR REPLACE FUNCTION auth_now() RETURNS timestamptz AS $$
  SELECT CASE WHEN current_setting('argon.test_clock', true) = 'on'
    THEN coalesce(nullif(current_setting('argon.test_now', true), '')::timestamptz, now())
    ELSE now() END;
$$ LANGUAGE sql STABLE;
