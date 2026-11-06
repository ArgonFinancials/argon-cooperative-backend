-- DELIBERATELY WRONG (m_born_used_ok). A full copy of 0024 with one rule broken.
--
-- Must turn red: TestEnrol03.
BEGIN;
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue'));

-- ---- the officer's act ----------------------------------------------------------
CREATE FUNCTION enrolment_issue_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  target UUID;
  soc    UUID;
BEGIN
  IF NEW.kind <> 'enrolment_issue' THEN RETURN NEW; END IF;
  SELECT user_id, society_id INTO target, soc FROM members WHERE id = NEW.target_member_id;
  IF NEW.target_member_id IS NULL OR target IS NULL THEN
    PERFORM auth_refuse('enrolment_code_for_the_member', 'an enrolment code is issued for a member record that belongs to a person');
  END IF;
  IF target = NEW.actor_user_id THEN
    PERFORM auth_refuse('enrolment_not_self', 'nobody issues their own enrolment code, through any member record they hold');
  END IF;
  IF NOT person_holds_post(NEW.actor_user_id, soc, false, true) THEN
    PERFORM auth_refuse('enrolment_by_current_officer', 'an enrolment code is issued by a current officer of the member''s society (not the read-only auditor)');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_enrolment_issue BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION enrolment_issue_admissible();

-- ---- the code -------------------------------------------------------------------
ALTER TABLE enrolment_codes ADD CONSTRAINT enrolment_code_is_digest CHECK (code_digest ~ '^[0-9a-f]{64}$');
ALTER TABLE enrolment_codes ADD CONSTRAINT enrolment_codes_void_reason_check CHECK (void_reason IN ('reissued', 'wrong_tries'));
CREATE INDEX enrolment_codes_user_idx ON enrolment_codes (user_id, issued_at DESC);
CREATE UNIQUE INDEX enrolment_code_one_live ON enrolment_codes (user_id) WHERE used_at IS NULL AND voided_at IS NULL;
CREATE UNIQUE INDEX enrolment_code_one_per_act ON enrolment_codes (issued_by_act) WHERE issued_by_act IS NOT NULL;

CREATE FUNCTION enrolment_code_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a      credential_acts%ROWTYPE;
  soc    UUID;
  v_old  UUID;
BEGIN
  PERFORM auth_server_time(NEW.issued_at, 'enrolment_code_born_fresh');
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
CREATE TRIGGER enrolment_code_admissible BEFORE INSERT ON enrolment_codes
  FOR EACH ROW EXECUTE FUNCTION enrolment_code_admissible();

-- Append-only, forward only: one more try, used once, voided once, and only while live.
CREATE FUNCTION enrolment_code_append_only() RETURNS trigger AS $$
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
CREATE TRIGGER enrolment_code_append_only BEFORE UPDATE OR DELETE ON enrolment_codes
  FOR EACH ROW EXECUTE FUNCTION enrolment_code_append_only();

-- ---- the operator's path: the founding officers ---------------------------------
CREATE OR REPLACE FUNCTION operator_issue_enrolment(p_member UUID, p_digest TEXT, p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_operator, operator_login, operator_reason)
    SELECT user_id, id, p_digest, p_operator, p_login, p_reason FROM members WHERE id = p_member
    RETURNING id INTO v_id;
  IF v_id IS NULL THEN
    PERFORM auth_refuse('enrolment_code_for_the_member', 'no member record with that id belongs to a person');
  END IF;
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_issue_enrolment(UUID, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

-- ---- redeeming it: the auth service ----------------------------------------------
-- The person's live code. The right one inside 30 days is used and returned; a
-- wrong one is a try (the fifth voids it, append_only above); an expired code
-- is neither. NOTHING here writes a sign-in attempt: the tries are the code's.
CREATE OR REPLACE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT) RETURNS uuid
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
