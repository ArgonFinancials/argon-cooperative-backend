-- DELIBERATELY WRONG (over-strict). OVER-STRICT: a series must start after today.
--
-- Must turn red: TestOperator_Control_ASeriesFromTodayOrLater.
BEGIN;
CREATE OR REPLACE FUNCTION operator_set_withdrawal_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old SMALLINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a societys rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the societys decision: name its resolution (minutes reference and date); without it, an Argon staff member would be the author of a societys governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_quorum < 2 OR p_quorum > 9 THEN
    PERFORM auth_refuse('withdrawal_quorum_at_least_two', 'a quorum of one is no quorum: it makes withdrawals unilateral, the single risk PRD 8.4 exists to prevent. A society that wants single-signature withdrawals is a conversation with Argon about whether this platform suits it, not a setting');
  END IF;
  SELECT withdrawal_signatures_required INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET withdrawal_signatures_required = p_quorum WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'withdrawal_signatures_required', v_old::text, p_quorum::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old BIGINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a societys rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the societys decision: name its resolution (minutes reference and date); without it, an Argon staff member would be the author of a societys governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_floor < 0 THEN PERFORM auth_refuse('account_floor_not_negative', 'an account floor is never negative'); END IF;
  SELECT account_floor_kobo INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET account_floor_kobo = p_floor WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'account_floor_kobo', v_old::text, p_floor::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT) RETURNS UUID SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a societys rules says why'); END IF;
  IF p_from <= current_date THEN PERFORM auth_refuse('charge_series_not_backdated', 'a charge series is never backdated: a contribution recorded late would be charged by a series that did not exist when the money arrived, at a figure the member was never told. Today or a future date, announced before it takes effect'); END IF;
  INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
    VALUES (p_society, p_kind, p_component, p_from, p_source) RETURNING id INTO v_id;
  INSERT INTO charge_bands (schedule_id, from_kobo, to_kobo, flat_kobo, bp, cap_kobo)
    SELECT v_id, (b->>'from')::bigint, (b->>'to')::bigint, (b->>'flat')::bigint, (b->>'bp')::int, (b->>'cap')::bigint
      FROM jsonb_array_elements(p_bands) b;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'charge_series', NULL, format('%s %s from %s (source: %s): %s', p_kind, p_component, p_from, p_source, p_bands::text), p_operator, p_login, NULL, NULL, p_reason);
  RETURN v_id;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION society_settings_log_immutable() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('society_settings_log_immutable', 'the settings log is never edited: it is who changed a societys rules, on whose decision');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER society_settings_log_immutable BEFORE UPDATE OR DELETE ON society_settings_log
  FOR EACH ROW EXECUTE FUNCTION society_settings_log_immutable();
COMMIT;
