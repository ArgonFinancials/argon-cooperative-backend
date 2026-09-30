-- DELIBERATELY WRONG. Nullable for every kind of entry.
ALTER TABLE ledger_entries ALTER value_date DROP NOT NULL;
