-- 0035's INTERFACE: "M0" for suite 0035, GENERATED (gen0035.py) from the migration
-- with every rule off: a society is inserted marked, unmarked again, marked with
-- people in it and any name; the operator sets anyone's PIN, as itself, naming
-- nobody. The mark, the function and the PIN still exist: the control needs them.
-- Applied on top of the real 0001-0034.
BEGIN;

ALTER TABLE societies ADD COLUMN demo BOOLEAN NOT NULL DEFAULT false;

CREATE FUNCTION society_demo_rule() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF false THEN
      PERFORM auth_refuse('demo_by_its_function', 'a demonstration society is made by operator_create_demo_society, never inserted marked');
    END IF;
    RETURN NEW;
  END IF;
  IF false THEN
    PERFORM auth_refuse('demo_is_permanent', 'a demonstration society stays one: its people were given PINs by the operator');
  END IF;
  IF NEW.demo AND NOT OLD.demo THEN
    IF false THEN
      PERFORM auth_refuse('demo_only_when_empty', 'only a society nobody belongs to yet can be made a demonstration');
    END IF;
  END IF;
  IF false THEN
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
  IF false THEN
    PERFORM auth_refuse('demo_pin_only_for_demo', 'the operator sets a PIN only for someone who belongs to demonstration societies and nothing else');
  END IF;
  -- A PIN as itself is refused by auth_credentials' own pin_is_argon2id (0003).
  INSERT INTO auth_credentials (user_id, pin_hash) VALUES (p_user, p_pin_hash)
    ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = auth_now();
  INSERT INTO demo_pins (user_id, operator, login, reason) VALUES (p_user, p_operator, p_login, p_reason);
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_set_demo_pin(UUID, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

COMMIT;
