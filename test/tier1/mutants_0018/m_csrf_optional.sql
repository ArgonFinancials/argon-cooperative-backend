-- DELIBERATELY WRONG. A session may start without a CSRF token.
--
-- Must turn red: TestCsrf01_ASessionWithoutACsrfDigestIsRefused.
BEGIN;
ALTER TABLE auth_sessions ADD CONSTRAINT csrf_is_digest CHECK (csrf_digest ~ '^[0-9a-f]{64}$');
ALTER TABLE auth_sessions ADD CONSTRAINT csrf_is_not_the_session CHECK (csrf_digest <> token_digest);
CREATE FUNCTION auth_sessions_csrf() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.csrf_digest IS DISTINCT FROM OLD.csrf_digest THEN
    PERFORM auth_refuse('session_csrf_fixed', 'a session''s CSRF token never changes');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_sessions_csrf BEFORE INSERT OR UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION auth_sessions_csrf();
COMMIT;
