-- 0036: A FOUNDING OFFICER IS AN ACTIVE MEMBER (2026-11-22, the reviewer: "the
-- column has a default that nothing ever overrides, so every founding officer of
-- every society sits in a state nobody chose").
--
-- members.status defaults to 'pending'. The register (0031) says 'active'; the
-- founding officer (0027) said nothing, so every founding officer, the
-- demonstration societies' included, was 'pending', which nothing ever moves.
-- 'pending' has one consequence in the rules: a pending member cannot guarantee
-- a loan (0008, guarantor_member_of_society). /me and the register show it.
--
-- The function now says 'active'. The officers it already appointed are made
-- active by backfill_founding_officers_active(): only those the settings log
-- records as founding officers, and only while still 'pending', so a status
-- someone has since chosen is never overwritten.
BEGIN;

CREATE OR REPLACE FUNCTION operator_appoint_founding_officer(p_society UUID, p_name TEXT, p_phone TEXT, p_number TEXT, p_joined DATE,
    p_post officer_post, p_signatory BOOLEAN, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_user   UUID;
  v_member UUID;
  v_life   society_lifecycle;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'appointing a founding officer says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s founding decision: name its resolution (minutes reference and date)'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF btrim(coalesce(p_name, '')) = '' OR btrim(coalesce(p_phone, '')) = '' OR btrim(coalesce(p_number, '')) = '' OR p_joined IS NULL THEN
    PERFORM auth_refuse('founding_officer_identified', 'a founding officer is identified: their name, their phone, the society''s member number for them, and when they joined');
  END IF;
  SELECT lifecycle INTO v_life FROM societies WHERE id = p_society;
  IF v_life IS DISTINCT FROM 'prospect' THEN
    PERFORM auth_refuse('founding_officer_while_founding', 'the operator appoints a society''s officers only while it is being founded; after that they are the society''s to appoint');
  END IF;

  INSERT INTO users (full_name) VALUES (btrim(p_name)) RETURNING id INTO v_user;
  -- ACTIVE, said (0036): an officer appointed on the founding resolution is a
  -- member of the society. The column's default ('pending') was never a choice.
  INSERT INTO members (society_id, branch_id, member_number, full_name, phone, status, collection_channel, joined_on, user_id)
    SELECT p_society, b.id, btrim(p_number), btrim(p_name), btrim(p_phone), 'active', 'cash', p_joined, v_user
      FROM branches b WHERE b.society_id = p_society AND b.code = 'HQ'
    RETURNING id INTO v_member;
  INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
    VALUES (p_society, v_member, p_post, p_signatory, p_date);
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'founding_officer', NULL, p_post::text || ':' || btrim(p_number), p_operator, p_login, p_ref, p_date, p_reason);
  RETURN v_member;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION backfill_founding_officers_active() RETURNS integer SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n integer;
BEGIN
  UPDATE members m SET status = 'active'
    FROM society_settings_log l
   WHERE l.setting = 'founding_officer' AND l.society_id = m.society_id
     AND m.member_number = substr(l.new_value, strpos(l.new_value, ':') + 1)
     AND m.status = 'pending';
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION backfill_founding_officers_active() FROM PUBLIC;

SELECT backfill_founding_officers_active();

COMMIT;
