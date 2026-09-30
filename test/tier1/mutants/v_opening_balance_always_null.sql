-- DELIBERATELY WRONG (over-strict). An opening balance may NEVER carry a date.
ALTER TABLE ledger_entries ALTER value_date DROP NOT NULL;
ALTER TABLE ledger_entries ADD CONSTRAINT value_date_known_unless_opening_balance CHECK ((kind = 'opening_balance') = (value_date IS NULL));
