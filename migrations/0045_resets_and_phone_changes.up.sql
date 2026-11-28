-- =============================================================================
-- ARGON — 0045: A FORGOTTEN PIN AND A CHANGED PHONE, AS THE REVIEWER DECIDED
--
-- Written against test/tier1/tier1_0045_test.go (2026-11-28); mutation-tested
-- with test/tier1/mutants_0045 before this file existed. LEDGER_CONTRACT §12.
-- 0003 modelled both acts and D-15; nothing called them. The reviewer's
-- decisions (2026-11-28) tighten 0003's rules; nothing of 0003 is loosened.
--
-- A PIN RESET.
--   AN ORDINARY MEMBER: the society's CURRENT SECRETARY alone, on their own
--     PIN, with a reason. (0003: any officer.) Their PIN opens their own
--     savings and nothing else: one officer who knows them is proportionate.
--   AN OFFICER (anyone holding a current post, signing or not; 0003: a
--     signatory only): a request with a reason, then TWO OTHER current
--     signatories each approving on their own PIN, EACH WITH A REASON (0003:
--     the approval said nothing). An officer's PIN opens more than their own
--     savings, so it has the weight of issuing their enrolment code (0026:
--     two officers).
--   NEVER ONE'S OWN (0003). The old PIN dies with the act that completes it:
--     the credential, every session and every device (0003's
--     auth_reset_person). A new PIN is set through the enrolment path: an
--     enrolment code, which only a person with no PIN can redeem.
--
-- A PHONE CHANGE.
--   THE CURRENT SECRETARY records it, on their own PIN, with a reason, AND
--     SAYS THE MEMBER IS PRESENT (member_present: the secretary's attestation,
--     recorded on the act). An officer's number: the two-signatory path, the
--     request saying the officer is present.
--   THE OLD NUMBER IS TOLD. A number is how a member signs in, so changing it
--     takes over the account, and the one person who would notice holds the
--     old phone. Every change of a registered number writes a NOTICE to the
--     old number, in the same transaction, naming the act (so who did it):
--     no number changes without one. The API sends it after the change
--     commits and records what came of it, once, never edited. A notice that
--     could not reach the old phone is RECORDED as such (refused, not reached,
--     unknown, no sender), never skipped: that is the case where
--     someone should look twice. One still waiting is alerted on by the
--     freshness timer (68-check-phone-notices.sh).
-- =============================================================================

BEGIN;

ALTER TABLE credential_acts ADD COLUMN member_present BOOLEAN;

-- The society's current secretary, by their member record there.
CREATE FUNCTION society_secretary(p_user UUID, p_society UUID) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
     WHERE m.user_id = p_user AND a.society_id = p_society AND a.post = 'secretary'
       AND a.held_from <= auth_today() AND (a.held_until IS NULL OR a.held_until >= auth_today()));
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION credential_act_admissible() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  pr     auth_proofs%ROWTYPE;
  rq     credential_acts%ROWTYPE;
  target UUID;
  soc    UUID;
BEGIN
  PERFORM auth_server_time(NEW.at, 'credential_acts_at_is_now');
  IF NEW.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('credential_acts_at_is_now', 'act_xid is written by the server');
  END IF;

  -- The actor's own, fresh, unused PIN proof. A code is not an attribution.
  SELECT * INTO pr FROM auth_proofs WHERE id = NEW.proof_id FOR UPDATE;
  IF NOT FOUND OR pr.method <> 'pin' OR pr.user_id <> NEW.actor_user_id
     OR pr.at < auth_now() - interval '5 minutes' OR pr.consumed_at IS NOT NULL THEN
    PERFORM auth_refuse('act_needs_pin_proof',
      'an attributable act needs the actor''s own PIN, entered in the last five minutes and not already used');
  END IF;
  UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = pr.id;

  target := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));

  CASE NEW.kind
  WHEN 'pin_change' THEN
    IF target IS DISTINCT FROM NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'a PIN change is the person''s own act');
    END IF;
    IF NEW.new_pin_hash IS NULL OR NEW.new_pin_hash !~ '^\$argon2id\$v=19\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$' THEN
      PERFORM auth_refuse('pin_is_argon2id', 'the new PIN must be an argon2id hash');
    END IF;
    IF (SELECT user_id FROM auth_sessions WHERE id = NEW.actor_session_id AND ended_at IS NULL)
       IS DISTINCT FROM NEW.actor_user_id THEN
      PERFORM auth_refuse('session_ended_by_owner', 'a PIN change is made from the person''s own live session');
    END IF;

  WHEN 'member_reset' THEN
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a reset states its reason');
    END IF;
    IF NOT officer_over(NEW.actor_user_id, target) THEN
      PERFORM auth_refuse('reset_by_current_officer',
        'a reset is made by a current officer of the member''s society (not the read-only auditor)');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'an officer never resets themselves, through any member record');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM members tm WHERE tm.user_id = target AND society_secretary(NEW.actor_user_id, tm.society_id)) THEN
      PERFORM auth_refuse('reset_by_secretary', 'a member''s PIN is reset by the society''s current secretary (2026-11-28)');
    END IF;
    IF person_holds_post(target, NULL, false) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'an officer''s PIN is reset by two OTHER current signatories (D-15, 2026-11-28)');
    END IF;

  WHEN 'phone_change' THEN
    IF NEW.target_member_id IS NULL OR NEW.new_phone IS NULL THEN
      PERFORM auth_refuse('phone_change_attributable', 'a phone change names the member record and the new number');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('phone_change_not_self',
        'a member never changes their own registered number: a code to the old phone is not identity');
    END IF;
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a phone change states its reason');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = NEW.target_member_id);
    IF NOT person_holds_post(NEW.actor_user_id, soc, false, true) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a phone change is made by a current officer of the society');
    END IF;
    IF NOT society_secretary(NEW.actor_user_id, soc) THEN
      PERFORM auth_refuse('phone_change_by_secretary', 'a phone change is recorded by the society''s current secretary (2026-11-28)');
    END IF;
    IF NEW.member_present IS NOT TRUE THEN
      PERFORM auth_refuse('phone_change_member_present', 'a phone change is recorded with the member present, and says so');
    END IF;
    IF person_holds_post(target, soc, false) THEN
      PERFORM auth_refuse('signatory_phone_needs_two', 'an officer''s number is changed by two OTHER current signatories');
    END IF;

  WHEN 'lock_release' THEN
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a release states its reason');
    END IF;
    IF NOT officer_over(NEW.actor_user_id, target) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a lock is released by a current officer of the member''s society');
    END IF;
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('release_not_self', 'an officer never releases their own lock');
    END IF;

  WHEN 'signatory_reset_request', 'signatory_phone_request' THEN
    IF NEW.target_member_id IS NULL THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'a request names the officer''s member record');
    END IF;
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('reset_states_reason', 'a request states its reason');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = NEW.target_member_id);
    IF target = NEW.actor_user_id THEN
      PERFORM auth_refuse('reset_not_self', 'nobody raises their own reset');
    END IF;
    IF NOT person_holds_post(NEW.actor_user_id, soc, false, true) THEN
      PERFORM auth_refuse('reset_by_current_officer', 'a request is raised by a current officer of the society');
    END IF;
    IF NOT person_holds_post(target, soc, false) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'this path is for a current officer; a member''s is the secretary''s');
    END IF;
    IF NEW.kind = 'signatory_phone_request' AND NEW.new_phone IS NULL THEN
      PERFORM auth_refuse('phone_change_attributable', 'a phone request names the new number');
    END IF;
    IF NEW.kind = 'signatory_phone_request' AND NEW.member_present IS NOT TRUE THEN
      PERFORM auth_refuse('phone_change_member_present', 'a phone request is raised with the officer present, and says so');
    END IF;

  WHEN 'signatory_approval' THEN
    SELECT * INTO rq FROM credential_acts WHERE id = NEW.request_id;
    IF NOT FOUND OR rq.kind NOT IN ('signatory_reset_request', 'signatory_phone_request') THEN
      PERFORM auth_refuse('reset_approver_is_signatory', 'an approval approves a signatory request');
    END IF;
    IF NEW.actor_user_id = rq.target_user_id
       OR NEW.actor_user_id = (SELECT user_id FROM members WHERE id = rq.target_member_id) THEN
      PERFORM auth_refuse('reset_approver_not_target', 'the signatory concerned never approves their own request');
    END IF;
    soc := (SELECT society_id FROM members WHERE id = rq.target_member_id);
    IF NOT person_holds_post(NEW.actor_user_id, soc, true) THEN
      PERFORM auth_refuse('reset_approver_is_signatory',
        'an approver holds a current signing post in the signatory''s society');
    END IF;
    IF EXISTS (SELECT 1 FROM credential_acts WHERE request_id = NEW.request_id AND kind = 'signatory_approval'
                  AND actor_user_id = NEW.actor_user_id) THEN
      PERFORM auth_refuse('reset_one_approval_per_person', 'one approval per person');
    END IF;
    IF auth_now() > rq.at + interval '48 hours' THEN
      PERFORM auth_refuse('reset_request_expired', 'approvals count only within 48 hours of the request');
    END IF;
    IF pr.at < rq.at THEN
      PERFORM auth_refuse('approval_after_request', 'an approval rests on a PIN entered after the request existed');
    END IF;
    IF NEW.reason IS NULL OR length(trim(NEW.reason)) <= 10 THEN
      PERFORM auth_refuse('approval_states_reason', 'each approving signatory states their own reason (2026-11-28)');
    END IF;
  ELSE
    NULL;   -- an unknown kind is refused by the column's CHECK, by name
  END CASE;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- ---- the old number is told ----------------------------------------------------
CREATE TABLE phone_change_notices (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  member_id  UUID NOT NULL REFERENCES members(id),
  old_phone  TEXT NOT NULL,
  new_phone  TEXT NOT NULL,
  act_id     UUID NOT NULL REFERENCES credential_acts(id),   -- the act that changed it: who did it
  at         TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);
CREATE INDEX phone_change_notices_member_idx ON phone_change_notices (member_id, at);

-- Written by the change itself, never by a caller: no number changes without
-- a notice to the old one, naming the act in this transaction that changed it.
CREATE FUNCTION phone_change_notice() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  act UUID;
BEGIN
  IF NEW.phone IS NOT DISTINCT FROM OLD.phone THEN
    RETURN NEW;
  END IF;
  SELECT c.id INTO act FROM credential_acts c
   WHERE c.act_xid = pg_current_xact_id() AND c.kind = 'phone_change'
     AND c.target_member_id = NEW.id AND c.new_phone = NEW.phone;
  IF act IS NULL THEN
    SELECT a.id INTO act FROM credential_acts a JOIN credential_acts rq ON rq.id = a.request_id
     WHERE a.act_xid = pg_current_xact_id() AND a.kind = 'signatory_approval'
       AND rq.kind = 'signatory_phone_request' AND rq.target_member_id = NEW.id AND rq.new_phone = NEW.phone;
  END IF;
  INSERT INTO phone_change_notices (member_id, old_phone, new_phone, act_id) VALUES (NEW.id, OLD.phone, NEW.phone, act);
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER members_phone_change_notice AFTER UPDATE OF phone ON members
  FOR EACH ROW EXECUTE FUNCTION phone_change_notice();

CREATE FUNCTION phone_change_notices_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.at, 'phone_notice_at_is_now');
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER phone_change_notices_admissible BEFORE INSERT ON phone_change_notices
  FOR EACH ROW EXECUTE FUNCTION phone_change_notices_admissible();

-- What came of telling the old number: once, never edited. Anything but
-- 'accepted' is the old phone NOT told, recorded as such.
CREATE TABLE phone_change_notice_sends (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  notice_id           UUID NOT NULL REFERENCES phone_change_notices(id),
  outcome             TEXT NOT NULL CONSTRAINT phone_notice_outcome_known
                        CHECK (outcome IN ('accepted', 'refused', 'not_reached', 'unknown', 'no_sender')),
  provider_message_id TEXT,
  provider_code       TEXT,
  at                  TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  CONSTRAINT phone_notice_accepted_has_message_id CHECK ((outcome = 'accepted') = (coalesce(provider_message_id, '') <> '')),
  CONSTRAINT phone_notice_provider_code_on_refusal CHECK (provider_code IS NULL OR outcome = 'refused')
);
CREATE UNIQUE INDEX phone_notice_sent_once ON phone_change_notice_sends (notice_id);

CREATE FUNCTION phone_change_notice_sends_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.at, 'phone_notice_at_is_now');
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER phone_change_notice_sends_admissible BEFORE INSERT ON phone_change_notice_sends
  FOR EACH ROW EXECUTE FUNCTION phone_change_notice_sends_admissible();

CREATE FUNCTION phone_notices_append_only() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('phone_notice_append_only', 'a notice to the old number, and what came of it, are never changed or removed');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER phone_change_notices_append_only BEFORE UPDATE OR DELETE ON phone_change_notices
  FOR EACH ROW EXECUTE FUNCTION phone_notices_append_only();
CREATE TRIGGER phone_change_notice_sends_append_only BEFORE UPDATE OR DELETE ON phone_change_notice_sends
  FOR EACH ROW EXECUTE FUNCTION phone_notices_append_only();

COMMIT;
