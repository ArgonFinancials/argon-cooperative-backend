-- DELIBERATELY WRONG. Foreign key, but no check that the member is in the entry's society.
ALTER TABLE ledger_entries DROP COLUMN member_id;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_member_id_fkey FOREIGN KEY (member_id) REFERENCES members(id);
