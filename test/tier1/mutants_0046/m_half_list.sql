-- DELIBERATELY WRONG (m_half_list). GENERATED (gen0046.py): a full copy of 0046 with
-- one rule broken, applied over 0046_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
DROP VIEW IF EXISTS vfd_banks_current;
DROP TABLE IF EXISTS vfd_banks;
DROP TABLE IF EXISTS vfd_bank_lists;
DROP FUNCTION IF EXISTS vfd_banks_append_only();
DROP FUNCTION IF EXISTS vfd_bank_list_whole();
DROP FUNCTION IF EXISTS vfd_bank_lists_admissible();
COMMIT;

BEGIN;

CREATE TABLE vfd_bank_lists (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  taken_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  bank_count INTEGER NOT NULL CONSTRAINT vfd_bank_list_not_empty CHECK (bank_count > 0)
);

CREATE TABLE vfd_banks (
  list_id UUID NOT NULL REFERENCES vfd_bank_lists(id),
  code    TEXT NOT NULL CONSTRAINT vfd_bank_code_said CHECK (btrim(code) <> ''),
  name    TEXT NOT NULL CONSTRAINT vfd_bank_name_said CHECK (btrim(name) <> ''),
  CONSTRAINT vfd_bank_once_in_a_list PRIMARY KEY (list_id, code)
);

CREATE FUNCTION vfd_bank_lists_admissible() RETURNS trigger AS $$
BEGIN
  IF NEW.taken_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'a list is taken at the server''s time' USING ERRCODE = 'check_violation', CONSTRAINT = 'vfd_bank_list_at_is_now';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_bank_lists_admissible BEFORE INSERT ON vfd_bank_lists
  FOR EACH ROW EXECUTE FUNCTION vfd_bank_lists_admissible();

-- At commit, a list holds exactly the banks it says.
CREATE FUNCTION vfd_bank_list_whole() RETURNS trigger AS $$
DECLARE
  l vfd_bank_lists%ROWTYPE;
  n INTEGER;
BEGIN
  SELECT * INTO l FROM vfd_bank_lists WHERE id = NEW.id;
  SELECT count(*) INTO n FROM vfd_banks WHERE list_id = NEW.id;
  IF n <> l.bank_count THEN
    RAISE EXCEPTION 'a list holds the % banks it says, not %', l.bank_count, n USING ERRCODE = 'check_violation', CONSTRAINT = 'vfd_bank_list_whole';
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE FUNCTION vfd_banks_append_only() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'a bank list is never edited: the next one replaces it' USING ERRCODE = 'check_violation', CONSTRAINT = 'vfd_bank_list_append_only';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_bank_lists_append_only BEFORE UPDATE OR DELETE ON vfd_bank_lists
  FOR EACH ROW EXECUTE FUNCTION vfd_banks_append_only();
CREATE TRIGGER vfd_banks_append_only BEFORE UPDATE OR DELETE ON vfd_banks
  FOR EACH ROW EXECUTE FUNCTION vfd_banks_append_only();

-- The newest list's banks, by name.
CREATE VIEW vfd_banks_current AS
  SELECT b.code, b.name, l.taken_at
    FROM vfd_banks b JOIN vfd_bank_lists l ON l.id = b.list_id
   WHERE l.id = (SELECT id FROM vfd_bank_lists ORDER BY taken_at DESC, id DESC LIMIT 1);

COMMIT;
