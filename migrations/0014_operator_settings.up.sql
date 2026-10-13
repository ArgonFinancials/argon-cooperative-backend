-- =============================================================================
-- ARGON — 0014: THE OPERATOR SETS A SOCIETY'S RULES, AND SAYS WHO, ON WHOSE
-- DECISION
--
-- Written against test/tier1/tier1_0014_test.go (approved 2026-10-13, extended
-- to every governance setting the same day; mutation-tested with
-- test/tier1/mutants_0014 before this file existed). LEDGER_CONTRACT.md §10.
--
-- A society's quorums, floor and guard threshold are bye-law decisions, and
-- charges are configuration the app never writes (grants.sql). Until a request
-- kind exists to change them, the OPERATOR sets them, through these functions
-- only, callable only by the owner's role (the operator's), never by the app:
--
--   operator_set_withdrawal_quorum  operator_set_write_off_quorum
--   operator_set_signature_count    operator_set_account_floor
--   operator_set_guard_threshold    operator_add_charge_series
--   operator_set_lifecycle
--
-- Every call names the operator and the server login that ran it, says why,
-- and is logged append-only (society_settings_log: old and new values, who,
-- on whose decision, when). A society's setting also carries THE SOCIETY'S
-- RESOLUTION (minutes reference and date, never in the future): the operator
-- carries the decision out; without it, an Argon staff member would be the
-- author of a society's governance.
--
--   EVERY QUORUM IS AT LEAST 2, and the refusal says why: a quorum of one is no
--   quorum. A write-off above all: one person deciding a debt will not be
--   recovered is the easiest way to make money disappear behind a
--   legitimate-looking record.
--   THE GUARD THRESHOLD HAS NO MINIMUM: it is interface friction, not
--   authorisation. Applying the quorum rule to it by analogy would refuse a
--   legitimate low threshold.
--   THE LIFECYCLE is Argon's state for a society, not the society's decision:
--   a named operator and a reason, no society resolution.
--   A CHARGE SERIES IS NEVER BACKDATED: today, or a future date announced
--   before it takes effect.
--
-- The owner's role can still write these columns directly; that is the
-- documented trust boundary (deploy/README.md, "Who can act as argon_owner").
-- These functions make the operator's path the attributable one.
-- =============================================================================

BEGIN;
CREATE TABLE society_settings_log (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id      UUID NOT NULL REFERENCES societies(id),
  setting         TEXT NOT NULL,
  old_value       TEXT,
  new_value       TEXT NOT NULL,
  operator_name   TEXT,
  server_login    TEXT,
  resolution_ref  TEXT,
  resolution_date DATE,
  reason          TEXT,
  at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION operator_set_withdrawal_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old SMALLINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_quorum < 2 OR p_quorum > 9 THEN
    PERFORM auth_refuse('withdrawal_quorum_at_least_two', 'a quorum of one is no quorum: it makes withdrawals unilateral, the single risk PRD 8.4 exists to prevent. A society that wants single-signature withdrawals is a conversation with Argon about whether this platform suits it, not a setting');
  END IF;
  SELECT withdrawal_signatures_required INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET withdrawal_signatures_required = p_quorum WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'withdrawal_signatures_required', v_old::text, p_quorum::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_set_write_off_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old SMALLINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_quorum < 2 OR p_quorum > 9 THEN
    PERFORM auth_refuse('write_off_quorum_at_least_two', 'a write-off quorum of one would let one person decide a debt will not be recovered: the easiest way to make money disappear from a society''s books while leaving a legitimate-looking record. A write-off needs two people');
  END IF;
  SELECT write_off_signatures_required INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET write_off_signatures_required = p_quorum WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'write_off_signatures_required', v_old::text, p_quorum::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_set_signature_count(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old SMALLINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_quorum < 2 OR p_quorum > 9 THEN
    PERFORM auth_refuse('signatures_required_at_least_two', 'a signature count of one is no quorum: every act it governs would be unilateral');
  END IF;
  SELECT signatures_required INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET signatures_required = p_quorum WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'signatures_required', v_old::text, p_quorum::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old BIGINT;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF p_floor < 0 THEN
    PERFORM auth_refuse('account_floor_not_negative', 'an account floor is never negative');
  END IF;
  SELECT account_floor_kobo INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET account_floor_kobo = p_floor WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'account_floor_kobo', v_old::text, p_floor::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_set_guard_threshold(p_society UUID, p_kobo BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old BIGINT;
BEGIN
  -- Interface friction, NOT authorisation: it decides what a person is asked
  -- to type or hold, never who must sign. NO minimum, by design: applying the
  -- quorum rule here by analogy would refuse a legitimate low threshold.
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;

  SELECT guard_threshold_kobo INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET guard_threshold_kobo = p_kobo WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'guard_threshold_kobo', v_old::text, p_kobo::text, p_operator, p_login, p_ref, p_date, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT) RETURNS UUID SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  IF p_from < current_date THEN PERFORM auth_refuse('charge_series_not_backdated', 'a charge series is never backdated: a contribution recorded late would be charged by a series that did not exist when the money arrived, at a figure the member was never told. Today or a future date, announced before it takes effect'); END IF;
  INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
    VALUES (p_society, p_kind, p_component, p_from, p_source) RETURNING id INTO v_id;
  INSERT INTO charge_bands (schedule_id, from_kobo, to_kobo, flat_kobo, bp, cap_kobo)
    SELECT v_id, (b->>'from')::bigint, (b->>'to')::bigint, (b->>'flat')::bigint, (b->>'bp')::int, (b->>'cap')::bigint
      FROM jsonb_array_elements(p_bands) b;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'charge_series', NULL, format('%s %s from %s (source: %s): %s', p_kind, p_component, p_from, p_source, p_bands::text), p_operator, p_login, NULL, NULL, p_reason);
  RETURN v_id;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION operator_set_lifecycle(p_society UUID, p_lifecycle society_lifecycle, p_operator TEXT, p_login TEXT, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_old society_lifecycle;
BEGIN
  -- Argon's state for the society, not the society's decision about itself:
  -- a named operator and a reason, and no society resolution.
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'a change to a society''s rules says why'); END IF;
  SELECT lifecycle INTO v_old FROM societies WHERE id = p_society FOR UPDATE;
  UPDATE societies SET lifecycle = p_lifecycle WHERE id = p_society;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
    VALUES (p_society, 'lifecycle', v_old::text, p_lifecycle::text, p_operator, p_login, NULL, NULL, p_reason);
END $$ LANGUAGE plpgsql;

CREATE FUNCTION society_settings_log_immutable() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('society_settings_log_immutable', 'the settings log is never edited: it is who changed a society''s rules, on whose decision');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER society_settings_log_immutable BEFORE UPDATE OR DELETE ON society_settings_log
  FOR EACH ROW EXECUTE FUNCTION society_settings_log_immutable();

REVOKE EXECUTE ON FUNCTION operator_set_withdrawal_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_write_off_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_signature_count(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_guard_threshold(p_society UUID, p_kobo BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_lifecycle(p_society UUID, p_lifecycle society_lifecycle, p_operator TEXT, p_login TEXT, p_reason TEXT) FROM PUBLIC;

COMMIT;
