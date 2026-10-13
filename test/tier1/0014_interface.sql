-- 0014's INTERFACE: "M0" for suite 0014, the operator's functions with NO
-- RULES: each changes its setting and nothing else (no checks, no log).
-- Applied on top of the real 0001–0013. Callable only by the owner's role
-- (the operator's); never by the app.
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
BEGIN
  UPDATE societies SET withdrawal_signatures_required = p_quorum WHERE id = p_society;
END $$ LANGUAGE plpgsql;
CREATE FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE societies SET account_floor_kobo = p_floor WHERE id = p_society;
END $$ LANGUAGE plpgsql;
CREATE FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT) RETURNS UUID SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
    VALUES (p_society, p_kind, p_component, p_from, p_source) RETURNING id INTO v_id;
  INSERT INTO charge_bands (schedule_id, from_kobo, to_kobo, flat_kobo, bp, cap_kobo)
    SELECT v_id, (b->>'from')::bigint, (b->>'to')::bigint, (b->>'flat')::bigint, (b->>'bp')::int, (b->>'cap')::bigint
      FROM jsonb_array_elements(p_bands) b;
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_set_withdrawal_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT) FROM PUBLIC;
COMMIT;
