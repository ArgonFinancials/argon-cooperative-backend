-- DELIBERATELY WRONG. posted_at is server-generated (caller-supplied values
-- refused), and lines are refused where the entry's posted_at < now(). Closes
-- the forgery; still open to a transaction that began before the entry was
-- committed, whose now() is EARLIER than the entry's posted_at.
CREATE FUNCTION mp() RETURNS trigger AS $$ BEGIN
  IF NEW.posted_at IS DISTINCT FROM now() THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_entries_posted_at_is_now'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mp BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION mp();
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF (SELECT posted_at FROM ledger_entries WHERE id = NEW.entry_id) < now() THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_lines_entry_open'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m();
