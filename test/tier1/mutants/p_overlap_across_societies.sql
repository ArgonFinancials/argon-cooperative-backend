-- DELIBERATELY WRONG (over-strict). Overlap forbidden across ALL societies.
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM accounting_periods p WHERE p.id <> NEW.id
     AND daterange(p.starts_on, p.ends_on, '[]') && daterange(NEW.starts_on, NEW.ends_on, '[]'))
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'accounting_periods_no_overlap'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON accounting_periods FOR EACH ROW EXECUTE FUNCTION m();
