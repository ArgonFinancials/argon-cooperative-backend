-- 0025 down: 0024's redemption (no device) and its append-only rule. Refused
-- while any code names a device: dropping the column would lose that record.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM enrolment_codes WHERE used_device_id IS NOT NULL) THEN
    RAISE EXCEPTION '0025 down: codes name the devices they trusted; dropping the column would erase that record';
  END IF;
END $$;
DROP FUNCTION enrolment_redeem(UUID, TEXT, TEXT);
CREATE OR REPLACE FUNCTION enrolment_code_append_only() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code''s record is never removed');
  END IF;
  IF (NEW.id, NEW.user_id, NEW.member_id, NEW.code_digest, NEW.issued_at, NEW.expires_at, NEW.issued_by_act,
      NEW.issued_by_operator, NEW.operator_login, NEW.operator_reason, NEW.replaces_id)
     IS DISTINCT FROM
     (OLD.id, OLD.user_id, OLD.member_id, OLD.code_digest, OLD.issued_at, OLD.expires_at, OLD.issued_by_act,
      OLD.issued_by_operator, OLD.operator_login, OLD.operator_reason, OLD.replaces_id)
     OR OLD.used_at IS NOT NULL OR OLD.voided_at IS NOT NULL
     OR NEW.wrong_tries NOT IN (OLD.wrong_tries, OLD.wrong_tries + 1)
     OR (NEW.used_at IS NOT NULL AND NEW.voided_at IS NOT NULL) THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code only moves forward: tried, used or voided, once, while live');
  END IF;
  IF NEW.wrong_tries >= 5 AND NEW.voided_at IS NULL THEN
    NEW.voided_at := auth_now(); NEW.void_reason := 'wrong_tries';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
ALTER TABLE enrolment_codes DROP COLUMN used_device_id;
CREATE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT) RETURNS uuid
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c enrolment_codes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM enrolment_codes WHERE user_id = p_user AND used_at IS NULL AND voided_at IS NULL FOR UPDATE;
  IF NOT FOUND OR auth_now() >= c.expires_at THEN RETURN NULL; END IF;
  IF c.code_digest = p_digest THEN
    UPDATE enrolment_codes SET used_at = auth_now() WHERE id = c.id;
    RETURN c.id;
  END IF;
  UPDATE enrolment_codes SET wrong_tries = wrong_tries + 1 WHERE id = c.id;
  RETURN NULL;
END $$ LANGUAGE plpgsql VOLATILE;
REVOKE EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT) FROM PUBLIC;
COMMIT;
