-- DELIBERATELY WRONG (over-strict). Lines accepted only for the entry most recently
-- created in this transaction - breaks a batch built in any other order.
CREATE FUNCTION me() RETURNS trigger AS $$ BEGIN PERFORM set_config('argon.last_entry', NEW.id::text, true); RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER me AFTER INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION me();
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF current_setting('argon.last_entry', true) IS DISTINCT FROM NEW.entry_id::text THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_lines_entry_open'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m();
