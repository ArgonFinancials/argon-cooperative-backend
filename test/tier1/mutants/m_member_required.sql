-- DELIBERATELY WRONG (over-strict). Every line must name a member.
ALTER TABLE ledger_entries DROP COLUMN member_id;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_member_id_fkey FOREIGN KEY (member_id) REFERENCES members(id);
ALTER TABLE ledger_lines ALTER member_id SET NOT NULL;
