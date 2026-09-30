-- DELIBERATELY WRONG. Line-level member added correctly; the entry-level one KEPT.
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_member_id_fkey FOREIGN KEY (member_id) REFERENCES members(id);
CREATE FUNCTION msoc() RETURNS trigger AS $$ BEGIN
  IF NEW.member_id IS NOT NULL AND (SELECT society_id FROM members WHERE id = NEW.member_id)
     <> (SELECT society_id FROM ledger_entries WHERE id = NEW.entry_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_line_member_in_society'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER msoc BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION msoc();
