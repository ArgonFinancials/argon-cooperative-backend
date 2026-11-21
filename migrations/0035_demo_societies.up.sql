-- 0035: DEMONSTRATION SOCIETIES (2026-11-21, the reviewer: "demonstration
-- accounts I can hand to people, with no activation step and no slips ... Build
-- it so it cannot be aimed at anything real").
--
-- A demo society is made by ONE operator function, and only that function
-- marks it. The mark is:
--   · set once, on a society nobody belongs to yet, named "TEST DATA ...";
--   · permanent (never unmarked), and the name keeps saying TEST DATA;
--   · the ONLY thing that lets a PIN be set by the operator, or a device be
--     trusted on the PIN alone (internal/auth, sign-in).
-- A person is "demo-only" when every society they belong to is a demo society
-- (and they belong to one). Anyone who is also a member of a real society is
-- not: their PIN is never set here, and no device is trusted for them without
-- the real path. A real society's officers still enrol with a slip.
BEGIN;

ALTER TABLE societies ADD COLUMN demo BOOLEAN NOT NULL DEFAULT false;

CREATE FUNCTION society_demo_rule() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.demo THEN
      PERFORM auth_refuse('demo_by_its_function', 'a demonstration society is made by operator_create_demo_society, never inserted marked');
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.demo AND NOT NEW.demo THEN
    PERFORM auth_refuse('demo_is_permanent', 'a demonstration society stays one: its people were given PINs by the operator');
  END IF;
  IF NEW.demo AND NOT OLD.demo THEN
    IF EXISTS (SELECT 1 FROM members WHERE society_id = NEW.id) THEN
      PERFORM auth_refuse('demo_only_when_empty', 'only a society nobody belongs to yet can be made a demonstration');
    END IF;
  END IF;
  IF NEW.demo AND NEW.registered_name NOT LIKE 'TEST DATA%' THEN
    PERFORM auth_refuse('demo_says_test_data', 'a demonstration society''s name begins TEST DATA, so nobody mistakes it for a real one');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER society_demo_rule BEFORE INSERT OR UPDATE ON societies FOR EACH ROW EXECUTE FUNCTION society_demo_rule();

-- Demo-only: a member of at least one society, and of no real one.
CREATE FUNCTION auth_demo_only(p_user UUID) RETURNS boolean SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM members m WHERE m.user_id = p_user)
     AND NOT EXISTS (SELECT 1 FROM members m JOIN societies s ON s.id = m.society_id WHERE m.user_id = p_user AND NOT s.demo);
$$ LANGUAGE sql STABLE;
REVOKE EXECUTE ON FUNCTION auth_demo_only(UUID) FROM PUBLIC;

CREATE FUNCTION operator_create_demo_society(p_code TEXT, p_name TEXT, p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  v_id := operator_create_society(p_code, 'TEST DATA: ' || btrim(p_name), 'TEST DATA', 'TEST DATA', 2, 2, 2,
                                  p_operator, p_login, 'TEST DATA: a demonstration, no resolution', current_date, p_reason);
  UPDATE societies SET demo = true WHERE id = v_id;
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_create_demo_society(TEXT, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

-- Who set which demo PIN, from where, and why: the normal path never sets a
-- PIN for someone, so this one says who did.
CREATE TABLE demo_pins (
  id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id  UUID NOT NULL REFERENCES users(id),
  operator TEXT NOT NULL,
  login    TEXT NOT NULL,
  reason   TEXT NOT NULL,
  at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION operator_set_demo_pin(p_user UUID, p_pin_hash TEXT, p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'setting a PIN for someone says why'); END IF;
  IF NOT auth_demo_only(p_user) THEN
    PERFORM auth_refuse('demo_pin_only_for_demo', 'the operator sets a PIN only for someone who belongs to demonstration societies and nothing else');
  END IF;
  -- A PIN as itself is refused by auth_credentials' own pin_is_argon2id (0003).
  INSERT INTO auth_credentials (user_id, pin_hash) VALUES (p_user, p_pin_hash)
    ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = auth_now();
  INSERT INTO demo_pins (user_id, operator, login, reason) VALUES (p_user, p_operator, p_login, p_reason);
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_set_demo_pin(UUID, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

COMMIT;
