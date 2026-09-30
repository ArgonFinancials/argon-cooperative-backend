-- DELIBERATELY WRONG (over-strict). 'Same transaction' tested by comparing the
-- entry's xmin with the top-level xid - wrong for an entry created inside a savepoint.
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF (SELECT xmin::text FROM ledger_entries WHERE id = NEW.entry_id) <> pg_current_xact_id()::text THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_lines_entry_open'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m();
