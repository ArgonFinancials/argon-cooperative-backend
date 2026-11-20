-- DELIBERATELY WRONG (m_setter_may_issue). GENERATED (gen0034.py): a full copy of 0034 with
-- one rule broken, applied over 0034_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
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
  IF (NEW.id, NEW.user_id, NEW.member_id, NEW.first_half_digest, NEW.issued_at, NEW.expires_at, NEW.issued_by_act,
      NEW.issued_by_operator, NEW.operator_login, NEW.operator_reason, NEW.replaces_id)
     IS DISTINCT FROM
     (OLD.id, OLD.user_id, OLD.member_id, OLD.first_half_digest, OLD.issued_at, OLD.expires_at, OLD.issued_by_act,
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
DROP TABLE email_sends;
DROP FUNCTION email_send_append_only();
DROP FUNCTION member_email_setter(UUID);
ALTER TABLE enrolment_codes DROP COLUMN sent_to, DROP COLUMN delivery;
DROP FUNCTION enrolment_email_window();
DROP TRIGGER members_email_admissible ON members;
DROP FUNCTION member_email_admissible();
ALTER TABLE members DROP CONSTRAINT member_email_shape;
ALTER TABLE members DROP COLUMN email_set_at, DROP COLUMN email_set_by_act, DROP COLUMN email;
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution'));
COMMIT;

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email'));

-- ---- the email ----------------------------------------------------------------------
ALTER TABLE members ADD COLUMN email TEXT;
ALTER TABLE members ADD COLUMN email_set_by_act UUID REFERENCES credential_acts(id);
ALTER TABLE members ADD COLUMN email_set_at TIMESTAMPTZ;
ALTER TABLE members ADD CONSTRAINT member_email_shape
  CHECK (email IS NULL OR (length(email) <= 254 AND email = lower(email) AND email ~ '^[^@\s,;<>]+@[^@\s,;<>]+\.[^@\s,;<>.]+$'));

CREATE FUNCTION member_email_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
BEGIN
  IF NEW.email IS NOT NULL THEN
    NEW.email := lower(btrim(NEW.email));
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.email IS NOT DISTINCT FROM OLD.email THEN
    -- The record of who set it moves only with the email itself.
    IF (NEW.email_set_by_act, NEW.email_set_at) IS DISTINCT FROM (OLD.email_set_by_act, OLD.email_set_at) THEN
      PERFORM auth_refuse('email_act', 'who set a member''s email, and when, changes only when the email does');
    END IF;
    RETURN NEW;
  END IF;
  IF TG_OP = 'INSERT' AND NEW.email IS NULL AND NEW.email_set_by_act IS NULL THEN
    NEW.email_set_at := NULL;
    RETURN NEW;
  END IF;
  SELECT * INTO a FROM credential_acts WHERE id = NEW.email_set_by_act;
  IF NOT FOUND OR a.kind NOT IN ('member_email', 'member_register') OR a.act_xid IS DISTINCT FROM pg_current_xact_id()
     OR (TG_OP = 'UPDATE' AND NEW.email_set_by_act IS NOT DISTINCT FROM OLD.email_set_by_act) THEN
    PERFORM auth_refuse('email_act', 'a member''s email is set on the setter''s own member_email act (or the registration''s), taken with it');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members m ON m.id = ap.member_id
                  WHERE m.user_id = a.actor_user_id AND ap.society_id = NEW.society_id AND ap.post = 'secretary'
                    AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())) THEN
    PERFORM auth_refuse('email_by_secretary', 'a member''s email is set by the society''s current secretary');
  END IF;
  NEW.email_set_at := auth_now();
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER members_email_admissible BEFORE INSERT OR UPDATE ON members FOR EACH ROW EXECUTE FUNCTION member_email_admissible();

-- ---- an emailed code ----------------------------------------------------------------
-- PROVISIONAL, like the wallet's windows.
CREATE FUNCTION enrolment_email_window() RETURNS INTERVAL LANGUAGE sql IMMUTABLE AS $$ SELECT interval '72 hours' $$;

ALTER TABLE enrolment_codes ADD COLUMN delivery TEXT NOT NULL DEFAULT 'slip' CONSTRAINT enrolment_delivery_known CHECK (delivery IN ('slip', 'email'));
ALTER TABLE enrolment_codes ADD COLUMN sent_to TEXT;

-- Who set the member's email: the actor of its act, or NULL.
CREATE FUNCTION member_email_setter(p_member UUID) RETURNS UUID LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.actor_user_id FROM members m JOIN credential_acts a ON a.id = m.email_set_by_act WHERE m.id = p_member
$$;

-- ---- a send ---------------------------------------------------------------------------
CREATE TABLE email_sends (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  member_id   UUID NOT NULL REFERENCES members(id),
  code_id     UUID REFERENCES enrolment_codes(id),
  kind        TEXT NOT NULL CHECK (kind IN ('whole', 'first_half', 'second_half')),
  sent_to     TEXT NOT NULL,
  outcome     TEXT NOT NULL CHECK (outcome IN ('sent', 'failed')),
  provider_id TEXT,
  detail      TEXT,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT email_send_says_what CHECK (CASE outcome WHEN 'sent' THEN code_id IS NOT NULL ELSE btrim(coalesce(detail, '')) <> '' END)
);
CREATE FUNCTION email_send_append_only() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM auth_refuse('email_send_append_only', 'a record of an email sent, or not sent, is never edited or removed');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER email_sends_append_only BEFORE UPDATE OR DELETE ON email_sends FOR EACH ROW EXECUTE FUNCTION email_send_append_only();

-- ---- 0026's rules, with the email's -------------------------------------------
CREATE OR REPLACE FUNCTION enrolment_code_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a      credential_acts%ROWTYPE;
  soc    UUID;
  v_old  UUID;
  v_email TEXT;
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
  -- The server's, whatever was asked: 30 days for a slip, the email window for an email.
  NEW.expires_at := NEW.issued_at + CASE NEW.delivery WHEN 'email' THEN enrolment_email_window() ELSE interval '30 days' END;

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

  -- An emailed code: to the member's own recorded email, from an officer who
  -- did not set it. A slip names no address.
  IF NEW.delivery = 'email' THEN
    IF NEW.issued_by_act IS NULL THEN
      PERFORM auth_refuse('enrolment_email_by_officer', 'the operator issues a founding officer''s code on a slip; codes are emailed by the society''s officers');
    END IF;
    SELECT email INTO v_email FROM members WHERE id = NEW.member_id;
    IF v_email IS NULL THEN
      PERFORM auth_refuse('enrolment_email_needs_address', 'this member has no email recorded: the secretary sets it, or the code is issued on a slip');
    END IF;
    IF false THEN
      PERFORM auth_refuse('enrolment_email_set_by_issuer', 'the officer who set this member''s email does not email their code: another officer does');
    END IF;
    NEW.sent_to := v_email;
  ELSE
    NEW.sent_to := NULL;
  END IF;

  -- One live code (a pending one included): a new one voids it, and names it.
  UPDATE enrolment_codes SET voided_at = auth_now(), void_reason = 'reissued'
   WHERE user_id = NEW.user_id AND used_at IS NULL AND voided_at IS NULL
  RETURNING id INTO v_old;
  NEW.replaces_id := v_old;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

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
  IF (NEW.id, NEW.user_id, NEW.member_id, NEW.first_half_digest, NEW.issued_at, NEW.expires_at, NEW.issued_by_act,
      NEW.issued_by_operator, NEW.operator_login, NEW.operator_reason, NEW.replaces_id, NEW.delivery, NEW.sent_to)
     IS DISTINCT FROM
     (OLD.id, OLD.user_id, OLD.member_id, OLD.first_half_digest, OLD.issued_at, OLD.expires_at, OLD.issued_by_act,
      OLD.issued_by_operator, OLD.operator_login, OLD.operator_reason, OLD.replaces_id, OLD.delivery, OLD.sent_to) THEN
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
    IF NEW.delivery = 'email' AND member_email_setter(NEW.member_id) = a.actor_user_id THEN
      PERFORM auth_refuse('enrolment_email_set_by_issuer', 'the officer who set this member''s email does not email their code: another officer does');
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

REVOKE EXECUTE ON FUNCTION member_email_setter(UUID) FROM PUBLIC;

COMMIT;
