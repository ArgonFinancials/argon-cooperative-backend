-- DELIBERATELY WRONG (over-strict). Applies the closed-period rule to the ORIGINAL's
-- date, so the §5 case - adjusting a closed-period entry - is refused.
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_adjusts_id_fkey FOREIGN KEY (adjusts_id) REFERENCES ledger_entries(id);
ALTER TABLE ledger_entries ADD CONSTRAINT adjustment_states_reason CHECK (adjusts_id IS NULL OR (reason IS NOT NULL AND length(trim(reason)) > 10));
ALTER TABLE ledger_entries ADD CONSTRAINT correction_is_reversal_or_adjustment CHECK (reverses_id IS NULL OR adjusts_id IS NULL);
CREATE FUNCTION ma() RETURNS trigger AS $$ BEGIN
  IF (SELECT society_id FROM ledger_entries WHERE id = coalesce(NEW.adjusts_id, NEW.reverses_id)) <> NEW.society_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'correction_same_society'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER ma BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION ma();
CREATE FUNCTION mo() RETURNS trigger AS $$ BEGIN
  IF NEW.adjusts_id IS NOT NULL AND EXISTS (SELECT 1 FROM accounting_periods p, ledger_entries o
     WHERE o.id = NEW.adjusts_id AND p.society_id = o.society_id AND p.closed_at IS NOT NULL
       AND o.value_date BETWEEN p.starts_on AND p.ends_on)
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'entry_not_in_closed_period'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mo BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION mo();
