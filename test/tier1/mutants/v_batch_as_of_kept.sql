-- DELIBERATELY WRONG. value_date rules right, but ledger_batches.as_of KEPT —
-- two records of one as-at date.
ALTER TABLE ledger_entries ALTER value_date DROP NOT NULL;
ALTER TABLE ledger_entries ADD CONSTRAINT value_date_known_unless_opening_balance CHECK (value_date IS NOT NULL OR kind = 'opening_balance');
