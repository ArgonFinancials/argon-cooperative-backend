-- Reverses 0014 only while nothing rests on it: once the operator has logged a
-- change, the log is the record of who changed a society's rules, and it is
-- never dropped. When it refuses, nothing changes, but migrate has already
-- recorded version 13, dirty. The schema is still at 14: recover with
-- `migrate force 14` (deploy/README.md, "migrate says dirty").
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM society_settings_log) THEN
    RAISE EXCEPTION '0014 down: refusing — the settings log holds who changed a society''s rules';
  END IF;
END $$;
DROP FUNCTION operator_set_withdrawal_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT);
DROP FUNCTION operator_set_write_off_quorum(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT);
DROP FUNCTION operator_set_signature_count(p_society UUID, p_quorum INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT);
DROP FUNCTION operator_set_account_floor(p_society UUID, p_floor BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT);
DROP FUNCTION operator_set_guard_threshold(p_society UUID, p_kobo BIGINT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT);
DROP FUNCTION operator_add_charge_series(p_society UUID, p_kind movement_kind, p_component TEXT, p_from DATE, p_source TEXT, p_bands JSONB, p_operator TEXT, p_login TEXT, p_reason TEXT);
DROP FUNCTION operator_set_lifecycle(p_society UUID, p_lifecycle society_lifecycle, p_operator TEXT, p_login TEXT, p_reason TEXT);
DROP TRIGGER society_settings_log_immutable ON society_settings_log;
DROP FUNCTION society_settings_log_immutable();
DROP TABLE society_settings_log;
COMMIT;
