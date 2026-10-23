-- 0018's INTERFACE: "M0" for suite 0018, the shape with NO RULES: a session's
-- CSRF digest, written by nobody in particular and checked by nothing.
-- Applied on top of the real 0001-0017.
BEGIN;
ALTER TABLE auth_sessions ADD COLUMN csrf_digest TEXT;
COMMIT;
