-- 0030's INTERFACE: "M0" for suite 0030. The question's shape, answering
-- nothing: no slip is ever said to be used. Applied on top of the real 0001–0029.
BEGIN;
CREATE FUNCTION enrolment_slip_used(p_user UUID, p_digest TEXT) RETURNS boolean
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$ SELECT false $$;
REVOKE EXECUTE ON FUNCTION enrolment_slip_used(UUID, TEXT) FROM PUBLIC;
COMMIT;
