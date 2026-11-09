-- DELIBERATELY WRONG (m_first_half_replaceable). A full copy of 0026 with one rule broken.
--
-- Must turn red: TestHalves07.
BEGIN;
DROP TRIGGER enrolment_code_zz_assemble ON enrolment_codes;
DROP FUNCTION enrolment_code_assemble();
ALTER TABLE enrolment_codes
  ADD CONSTRAINT enrolment_code_one_shape CHECK (
       (first_half_digest IS NULL     AND second_half_digest IS NULL     AND second_by_act IS NULL     AND code_digest IS NOT NULL)
    OR (first_half_digest IS NOT NULL AND second_half_digest IS NULL     AND second_by_act IS NULL     AND code_digest IS NULL)
    OR (first_half_digest IS NOT NULL AND second_half_digest IS NOT NULL AND second_by_act IS NOT NULL AND code_digest IS NOT NULL));
CREATE UNIQUE INDEX enrolment_code_one_per_second_act ON enrolment_codes (second_by_act) WHERE second_by_act IS NOT NULL;

-- The assembled code: the digest of both halves' digests, in either order.
-- The service computes the same for whatever is typed (internal/auth).
CREATE OR REPLACE FUNCTION enrolment_assembled(p_a TEXT, p_b TEXT) RETURNS TEXT AS $$
  SELECT encode(sha256(convert_to(least(p_a, p_b) || greatest(p_a, p_b), 'UTF8')), 'hex');
$$ LANGUAGE sql IMMUTABLE STRICT;

-- ---- issuing: 0024's rules, and the halves ------------------------------------
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
  -- Born whole, or born waiting for its second half: never both, never complete.
  IF NOT ((NEW.code_digest IS NOT NULL AND NEW.first_half_digest IS NULL)
          OR (NEW.code_digest IS NULL AND NEW.first_half_digest IS NOT NULL))
     OR NEW.second_half_digest IS NOT NULL OR NEW.second_by_act IS NOT NULL THEN
    PERFORM auth_refuse('enrolment_code_one_shape', 'a code is born whole, or with its first half alone; the second half comes from a second signatory''s own act');
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
    -- §10's fifth reach: a signatory's code, from two other signatories.
    IF NEW.first_half_digest IS NULL AND person_holds_post(NEW.user_id, soc, true) THEN
      PERFORM auth_refuse('enrolment_signatory_two_halves', 'a code for someone who holds a signing post comes in two halves, from two other current signatories');
    END IF;
    IF NEW.first_half_digest IS NOT NULL AND NOT person_holds_post(a.actor_user_id, soc, true) THEN
      PERFORM auth_refuse('enrolment_half_by_signatory', 'each half of a code is issued by a current signatory of the member''s society');
    END IF;
  ELSE
    IF NEW.first_half_digest IS NOT NULL THEN
      PERFORM auth_refuse('enrolment_code_one_shape', 'the operator issues a founding officer''s code whole');
    END IF;
    IF btrim(NEW.issued_by_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
    IF btrim(coalesce(NEW.operator_login, '')) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
    IF btrim(coalesce(NEW.operator_reason, '')) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'an enrolment code from the operator says why'); END IF;
    IF NOT person_holds_post(NEW.user_id, soc) THEN
      PERFORM auth_refuse('enrolment_operator_for_officers', 'the operator issues codes to the founding officers only; everyone after gets theirs from an officer of the society');
    END IF;
  END IF;

  -- One live code (a pending one included): a new one voids it, and names it.
  UPDATE enrolment_codes SET voided_at = auth_now(), void_reason = 'reissued'
   WHERE user_id = NEW.user_id AND used_at IS NULL AND voided_at IS NULL
  RETURNING id INTO v_old;
  NEW.replaces_id := v_old;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- ---- forward only: 0025's rules, and completing a halved code -------------------
CREATE OR REPLACE FUNCTION enrolment_code_append_only() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  completing BOOLEAN;
  a          credential_acts%ROWTYPE;
  first_by   UUID;
  soc        UUID;
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code''s record is never removed');
  END IF;
  IF (NEW.id, NEW.user_id, NEW.member_id, NEW.issued_at, NEW.expires_at, NEW.issued_by_act,
      NEW.issued_by_operator, NEW.operator_login, NEW.operator_reason, NEW.replaces_id)
     IS DISTINCT FROM
     (OLD.id, OLD.user_id, OLD.member_id, OLD.issued_at, OLD.expires_at, OLD.issued_by_act,
      OLD.issued_by_operator, OLD.operator_login, OLD.operator_reason, OLD.replaces_id) THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code''s person, issuer, life and first half never change');
  END IF;

  completing := OLD.second_half_digest IS NULL AND NEW.second_half_digest IS NOT NULL;
  IF completing THEN
    -- Half two, once, on a live code waiting for it, and nothing else with it.
    IF OLD.first_half_digest IS NULL OR OLD.used_at IS NOT NULL OR OLD.voided_at IS NOT NULL OR auth_now() >= OLD.expires_at
       OR NEW.second_by_act IS NULL OR NEW.code_digest IS DISTINCT FROM OLD.code_digest
       OR NEW.wrong_tries <> OLD.wrong_tries OR NEW.used_at IS NOT NULL OR NEW.voided_at IS NOT NULL OR NEW.used_device_id IS NOT NULL THEN
      PERFORM auth_refuse('enrolment_code_append_only', 'a second half is added once, alone, to a live code waiting for it');
    END IF;
    SELECT * INTO a FROM credential_acts WHERE id = NEW.second_by_act;
    IF NOT FOUND OR a.kind <> 'enrolment_issue' OR a.target_member_id IS DISTINCT FROM NEW.member_id
       OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
      PERFORM auth_refuse('enrolment_code_rests_on_its_act', 'the second half rests on its issuer''s own step-up for THIS member, taken with it');
    END IF;
    SELECT society_id INTO soc FROM members WHERE id = NEW.member_id;
    IF NOT person_holds_post(a.actor_user_id, soc, true) THEN
      PERFORM auth_refuse('enrolment_half_by_signatory', 'each half of a code is issued by a current signatory of the member''s society');
    END IF;
    SELECT actor_user_id INTO first_by FROM credential_acts WHERE id = OLD.issued_by_act;
    IF a.actor_user_id = first_by THEN
      PERFORM auth_refuse('enrolment_halves_two_signatories', 'the two halves come from two different signatories');
    END IF;
    NEW.code_digest := enrolment_assembled(NEW.first_half_digest, NEW.second_half_digest);
    RETURN NEW;
  END IF;

  IF (NEW.code_digest, NEW.second_half_digest, NEW.second_by_act) IS DISTINCT FROM (OLD.code_digest, OLD.second_half_digest, OLD.second_by_act)
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

-- ---- redeeming: a pending code is not a code -------------------------------------
CREATE OR REPLACE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT, p_device_digest TEXT,
                                 OUT code_id UUID, OUT device_id UUID, OUT already_used BOOLEAN)
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c    enrolment_codes%ROWTYPE;
  live BOOLEAN;
BEGIN
  already_used := false;
  SELECT * INTO c FROM enrolment_codes WHERE user_id = p_user AND used_at IS NULL AND voided_at IS NULL FOR UPDATE;
  live := FOUND AND auth_now() < c.expires_at AND c.code_digest IS NOT NULL;
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
COMMIT;
