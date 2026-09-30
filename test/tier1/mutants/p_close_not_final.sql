-- DELIBERATELY WRONG. Posting rule right, but a closed period can be edited or reopened.
CREATE FUNCTION mp() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM accounting_periods p WHERE p.society_id = NEW.society_id AND p.closed_at IS NOT NULL AND NEW.value_date BETWEEN p.starts_on AND p.ends_on)
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'entry_not_in_closed_period'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mp BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION mp();
