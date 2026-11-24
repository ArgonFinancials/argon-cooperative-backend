-- 0037's INTERFACE: "M0" for suite 0037, GENERATED (gen0037.py) from the migration
-- with every rule off: founding takes the window and the threshold and keeps the
-- columns' defaults, unchecked; members and loan products keep their defaults;
-- postable and the wallets tables stay. Applied on top of the real 0001-0036.
BEGIN;

DROP FUNCTION operator_create_society(TEXT, TEXT, TEXT, TEXT, INT, INT, INT, TEXT, TEXT, TEXT, DATE, TEXT);
CREATE FUNCTION operator_create_society(p_code TEXT, p_name TEXT, p_reg TEXT, p_directorate TEXT,
    p_withdrawal INT, p_write_off INT, p_signatures INT, p_request_days INT, p_guard_kobo BIGINT,
    p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'founding a society says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s founding decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF btrim(coalesce(p_name, '')) = '' OR btrim(coalesce(p_reg, '')) = '' OR btrim(coalesce(p_directorate, '')) = '' THEN
    PERFORM auth_refuse('society_is_registered', 'a society is registered: its registered name, its registration number under the Cooperative Societies Act and its state directorate, none of them blank');
  END IF;
  IF p_withdrawal IS NULL OR p_withdrawal < 2 OR p_withdrawal > 9 THEN
    PERFORM auth_refuse('withdrawal_quorum_at_least_two', 'a quorum of one is no quorum: it makes withdrawals unilateral, the single risk PRD 8.4 exists to prevent. A society that wants single-signature withdrawals is a conversation with Argon about whether this platform suits it, not a setting');
  END IF;
  IF p_write_off IS NULL OR p_write_off < 2 OR p_write_off > 9 THEN
    PERFORM auth_refuse('write_off_quorum_at_least_two', 'a write-off quorum of one would let one person decide a debt will not be recovered: the easiest way to make money disappear from a society''s books while leaving a legitimate-looking record. A write-off needs two people');
  END IF;
  IF p_signatures IS NULL OR p_signatures < 2 OR p_signatures > 9 THEN
    PERFORM auth_refuse('signatures_required_at_least_two', 'a signature count of one is no quorum: every act it governs would be unilateral');
  END IF;
  -- 0037: the request window and the guard threshold are the resolution's too,
  -- never a column's default.
  IF p_request_days IS NULL THEN
    PERFORM auth_refuse('withdrawal_request_days_sane', 'a request window is at least one day (a request that expires as it is raised gives nobody time to sign) and at most a year');
  END IF;
  -- The guard threshold: positive, by the table's own guard_threshold_positive (0001).

  INSERT INTO societies (code, registered_name, registration_number, state_directorate,
      withdrawal_signatures_required, write_off_signatures_required, signatures_required)
    VALUES (p_code, btrim(p_name), btrim(p_reg), btrim(p_directorate), p_withdrawal, p_write_off, p_signatures)
    RETURNING id INTO v_id;
  INSERT INTO branches (society_id, code, name) VALUES (v_id, 'HQ', 'Head office');
  INSERT INTO accounts (society_id, code, name, kind, role)
    SELECT v_id, code, name, kind, role FROM standard_chart;

  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
  VALUES (v_id, 'founded',                        NULL, p_code,              p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'withdrawal_signatures_required', NULL, p_withdrawal::text,  p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'write_off_signatures_required',  NULL, p_write_off::text,   p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'signatures_required',            NULL, p_signatures::text,  p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'withdrawal_request_days',        NULL, p_request_days::text, p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'guard_threshold_kobo',           NULL, p_guard_kobo::text,  p_operator, p_login, p_ref, p_date, p_reason);
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_create_society(TEXT, TEXT, TEXT, TEXT, INT, INT, INT, INT, BIGINT, TEXT, TEXT, TEXT, DATE, TEXT) FROM PUBLIC;

-- The demonstration society (0035) on the new founding: its values said.
CREATE OR REPLACE FUNCTION operator_create_demo_society(p_code TEXT, p_name TEXT, p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  v_id := operator_create_society(p_code, 'TEST DATA: ' || btrim(p_name), 'TEST DATA', 'TEST DATA', 2, 2, 2, 7, 10000000,
                                  p_operator, p_login, 'TEST DATA: a demonstration, no resolution', current_date, p_reason);
  UPDATE societies SET demo = true WHERE id = v_id;
  RETURN v_id;
END $$ LANGUAGE plpgsql;


COMMIT;
