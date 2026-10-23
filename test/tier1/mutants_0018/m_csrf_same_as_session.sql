-- DELIBERATELY WRONG. A session's CSRF token may be its own session token.
--
-- Must turn red: TestCsrf04_TheCsrfTokenIsNotTheSessionToken.
BEGIN;
ALTER TABLE auth_sessions ADD CONSTRAINT csrf_is_digest CHECK (csrf_digest ~ '^[0-9a-f]{64}$');
CREATE FUNCTION auth_sessions_csrf() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'INSERT' AND NEW.csrf_digest IS NULL THEN
    PERFORM auth_refuse('session_csrf_required', 'a session carries its own CSRF token from the start');
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.csrf_digest IS DISTINCT FROM OLD.csrf_digest THEN
    PERFORM auth_refuse('session_csrf_fixed', 'a session''s CSRF token never changes');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_sessions_csrf BEFORE INSERT OR UPDATE ON auth_sessions
  FOR EACH ROW EXECUTE FUNCTION auth_sessions_csrf();
COMMIT;
