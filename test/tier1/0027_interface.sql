-- 0027's INTERFACE: "M0" for suite 0027. The operator's function with NO
-- RULES: it writes the person, the member and the appointment for anyone,
-- any society, any lifecycle, with nothing attributed and nothing logged.
-- Applied on top of the real 0001–0026.
BEGIN;
CREATE FUNCTION operator_appoint_founding_officer(p_society UUID, p_name TEXT, p_phone TEXT, p_number TEXT, p_joined DATE,
    p_post officer_post, p_signatory BOOLEAN, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_user UUID; v_member UUID;
BEGIN
  INSERT INTO users (full_name) VALUES (coalesce(p_name, '')) RETURNING id INTO v_user;
  INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
    SELECT p_society, b.id, coalesce(p_number, ''), coalesce(p_name, ''), p_phone, 'cash', coalesce(p_joined, current_date), v_user
      FROM branches b WHERE b.society_id = p_society ORDER BY b.code LIMIT 1
    RETURNING id INTO v_member;
  INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
    VALUES (p_society, v_member, p_post, p_signatory, current_date);
  RETURN v_member;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_appoint_founding_officer(UUID, TEXT, TEXT, TEXT, DATE, officer_post, BOOLEAN, TEXT, TEXT, TEXT, DATE, TEXT) FROM PUBLIC;
COMMIT;
