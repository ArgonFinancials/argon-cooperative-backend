-- =============================================================================
-- ARGON — 0046: THE BANKS, AS VFD LISTS THEM
--
-- Written against test/tier1/tier1_0046_test.go (2026-11-28); mutation-tested
-- with test/tier1/mutants_0046 before this file existed. The reviewer
-- (2026-11-28): "VFD's own list, cached. A list in our code is a list that
-- drifts, and we have found six of those."
--
-- The wallet worker takes VFD's list (GET /bank) once a day, in one
-- transaction: a LIST and its banks. The API serves the newest list (the
-- view vfd_banks_current) for the member choosing their bank's code, and
-- says when it was taken. Nothing here is typed by a person.
--
--   A LIST is the wallet's (argon_wallet writes; argon_app reads), taken at
--   the server's time, and holds the number of banks it says (checked at
--   commit: a list half written is never served).
--   A BANK is a code and a name, each said; one code once in a list.
--   NEVER EDITED: a list that is wrong is replaced by the next one.
-- =============================================================================

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
CREATE CONSTRAINT TRIGGER vfd_bank_list_whole AFTER INSERT ON vfd_bank_lists
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION vfd_bank_list_whole();

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
