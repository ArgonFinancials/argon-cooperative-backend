-- DELIBERATELY WRONG. Foreign key only.
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_adjusts_id_fkey FOREIGN KEY (adjusts_id) REFERENCES ledger_entries(id);
