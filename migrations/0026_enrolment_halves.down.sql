-- 0026 down: 0024's issuing rule, 0025's append-only rule and redemption, and
-- no halves. Refused while any code was issued in halves: dropping the
-- columns would erase who issued each half.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM enrolment_codes WHERE first_half_digest IS NOT NULL) THEN
    RAISE EXCEPTION '0026 down: codes were issued in halves; dropping the columns would erase who issued each half';
  END IF;
END $$;
CREATE OR REPLACE FUNCTION enrolment_code_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a      credential_acts%ROWTYPE;
  soc    UUID;
  v_old  UUID;
BEGIN
  PERFORM auth_server_time(NEW.issued_at, 'enrolment_code_born_fresh');
  IF NEW.wrong_tries <> 0 OR NEW.used_at IS NOT NULL OR NEW.voided_at IS NOT NULL OR NEW.void_reason IS NOT NULL OR NEW.replaces_id IS NOT NULL THEN
    PERFORM auth_refuse('enrolment_code_born_fresh', 'an enrolment code is born untried, unused and live');
  END IF;
  NEW.expires_at := NEW.issued_at + interval '30 days';   -- the server's, whatever was asked

  SELECT society_id INTO soc FROM members WHERE id = NEW.member_id AND user_id = NEW.user_id;
  IF NOT FOUND THEN
    PERFORM auth_refuse('enrolment_code_for_the_member', 'an enrolment code is for the person the member record belongs to');
  END IF;

  IF (NEW.issued_by_act IS NULL) = (NEW.issued_by_operator IS NULL) THEN
    PERFORM auth_refuse('enrolment_code_has_issuer', 'an enrolment code names one issuer: an officer''s act, or the operator');
  END IF;
  IF NEW.issued_by_act IS NOT NULL THEN
    SELECT * INTO a FROM credential_acts WHERE id = NEW.issued_by_act;
    IF NOT FOUND OR a.kind <> 'enrolment_issue' OR a.target_member_id IS DISTINCT FROM NEW.member_id
       OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
      PERFORM auth_refuse('enrolment_code_rests_on_its_act', 'an officer''s code rests on their own step-up for THIS member, taken with it');
    END IF;
  ELSE
    IF btrim(NEW.issued_by_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
    IF btrim(coalesce(NEW.operator_login, '')) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
    IF btrim(coalesce(NEW.operator_reason, '')) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'an enrolment code from the operator says why'); END IF;
    IF NOT person_holds_post(NEW.user_id, soc) THEN
      PERFORM auth_refuse('enrolment_operator_for_officers', 'the operator issues codes to the founding officers only; everyone after gets theirs from an officer of the society');
    END IF;
  END IF;

  -- One live code: a new one voids the live one, and names it.
  UPDATE enrolment_codes SET voided_at = auth_now(), void_reason = 'reissued'
   WHERE user_id = NEW.user_id AND used_at IS NULL AND voided_at IS NULL
  RETURNING id INTO v_old;
  NEW.replaces_id := v_old;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
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
     OR (NEW.used_at IS NOT NULL AND NEW.voided_at IS NOT NULL)
     OR (NEW.used_device_id IS NOT NULL) <> (NEW.used_at IS NOT NULL) THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code only moves forward: tried, used (with the device it trusted) or voided, once, while live');
  END IF;
  IF NEW.wrong_tries >= 5 AND NEW.voided_at IS NULL THEN
    NEW.voided_at := auth_now(); NEW.void_reason := 'wrong_tries';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT, p_device_digest TEXT,
                                 OUT code_id UUID, OUT device_id UUID, OUT already_used BOOLEAN)
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c    enrolment_codes%ROWTYPE;
  live BOOLEAN;
BEGIN
  already_used := false;
  SELECT * INTO c FROM enrolment_codes WHERE user_id = p_user AND used_at IS NULL AND voided_at IS NULL FOR UPDATE;
  live := FOUND AND auth_now() < c.expires_at;
  IF live AND c.code_digest = p_digest THEN
    INSERT INTO auth_devices (user_id, token_digest) VALUES (p_user, p_device_digest) RETURNING id INTO device_id;
    UPDATE enrolment_codes SET used_at = auth_now(), used_device_id = device_id WHERE id = c.id;
    code_id := c.id;
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM enrolment_codes WHERE user_id = p_user AND used_at IS NOT NULL AND code_digest = p_digest) THEN
    already_used := true;
    RETURN;
  END IF;
  IF live THEN
    UPDATE enrolment_codes SET wrong_tries = wrong_tries + 1 WHERE id = c.id;
  END IF;
END $$ LANGUAGE plpgsql VOLATILE;
REVOKE EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT, TEXT) FROM PUBLIC;
DROP INDEX enrolment_code_one_per_second_act;
ALTER TABLE enrolment_codes DROP CONSTRAINT enrolment_code_one_shape,
  DROP COLUMN pending, DROP COLUMN second_by_act, DROP COLUMN second_half_digest, DROP COLUMN first_half_digest;
ALTER TABLE enrolment_codes ALTER COLUMN code_digest SET NOT NULL;
DROP FUNCTION enrolment_assembled(TEXT, TEXT);
COMMIT;
