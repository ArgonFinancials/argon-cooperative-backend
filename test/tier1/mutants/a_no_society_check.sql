-- DELIBERATELY WRONG. Everything but the same-society check.
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_adjusts_id_fkey FOREIGN KEY (adjusts_id) REFERENCES ledger_entries(id);
ALTER TABLE ledger_entries ADD CONSTRAINT adjustment_states_reason CHECK (adjusts_id IS NULL OR (reason IS NOT NULL AND length(trim(reason)) > 10));
ALTER TABLE ledger_entries ADD CONSTRAINT correction_is_reversal_or_adjustment CHECK (reverses_id IS NULL OR adjusts_id IS NULL);
