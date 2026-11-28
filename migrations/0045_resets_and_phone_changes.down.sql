-- 0045 down: 0003's rules again (its credential_act_admissible, copied verbatim),
-- and the notices removed.
BEGIN;
DROP TRIGGER IF EXISTS phone_change_notice_sends_append_only ON phone_change_notice_sends;
DROP TRIGGER IF EXISTS phone_change_notices_append_only ON phone_change_notices;
DROP TRIGGER IF EXISTS members_phone_change_notice ON members;
DROP TABLE IF EXISTS phone_change_notice_sends;
DROP TABLE IF EXISTS phone_change_notices;
DROP FUNCTION IF EXISTS phone_notices_append_only();
DROP FUNCTION IF EXISTS phone_change_notice_sends_admissible();
DROP FUNCTION IF EXISTS phone_change_notices_admissible();
DROP FUNCTION IF EXISTS phone_change_notice();
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
    IF person_holds_post(target, NULL, true) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'a signatory is reset by two OTHER current signatories (D-15)');
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
    IF person_holds_post(target, soc, true) THEN
      PERFORM auth_refuse('signatory_phone_needs_two', 'a signatory''s number is changed by two OTHER current signatories');
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
      PERFORM auth_refuse('signatory_reset_needs_two', 'a request names the signatory''s member record');
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
    IF NOT person_holds_post(target, soc, true) THEN
      PERFORM auth_refuse('signatory_reset_needs_two', 'this path is for a current signatory; use a member reset');
    END IF;
    IF NEW.kind = 'signatory_phone_request' AND NEW.new_phone IS NULL THEN
      PERFORM auth_refuse('phone_change_attributable', 'a phone request names the new number');
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
  ELSE
    NULL;   -- an unknown kind is refused by the column's CHECK, by name
  END CASE;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP FUNCTION IF EXISTS society_secretary(UUID, UUID);
ALTER TABLE credential_acts DROP COLUMN IF EXISTS member_present;
COMMIT;
