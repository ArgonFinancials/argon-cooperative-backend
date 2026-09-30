-- DELIBERATELY WRONG. Nullable, but an unknown date is quietly filled with the posting date.
ALTER TABLE ledger_entries ALTER value_date DROP NOT NULL;
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  NEW.value_date := coalesce(NEW.value_date, NEW.posted_at::date);
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION m();
