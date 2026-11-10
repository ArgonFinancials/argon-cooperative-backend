-- 0027 down: the operator's function only. The officers it appointed stay:
-- they are the society's members and posts, recorded in the settings log.
BEGIN;
DROP FUNCTION operator_appoint_founding_officer(UUID, TEXT, TEXT, TEXT, DATE, officer_post, BOOLEAN, TEXT, TEXT, TEXT, DATE, TEXT);
COMMIT;
