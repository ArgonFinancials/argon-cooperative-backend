-- DELIBERATELY WRONG. An entry may be both reversal and adjustment.
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_adjusts_id_fkey FOREIGN KEY (adjusts_id) REFERENCES ledger_entries(id);
ALTER TABLE ledger_entries ADD CONSTRAINT adjustment_states_reason CHECK (adjusts_id IS NULL OR (reason IS NOT NULL AND length(trim(reason)) > 10));
CREATE FUNCTION ma() RETURNS trigger AS $$ BEGIN
  IF (SELECT society_id FROM ledger_entries WHERE id = coalesce(NEW.adjusts_id, NEW.reverses_id)) <> NEW.society_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'correction_same_society'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER ma BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION ma();
