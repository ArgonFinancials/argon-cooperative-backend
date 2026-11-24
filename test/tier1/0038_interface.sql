-- 0038's INTERFACE: "M0" for suite 0038, GENERATED (gen0038.py) from the migration
-- with every rule off: any act, anyone, their own, no reason, any move, an officer
-- in post, an editable record, and the member never moved. The record exists: the
-- control needs it. Applied on top of the real 0001-0037.
BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status'));

CREATE TABLE member_status_changes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  member_id   UUID NOT NULL REFERENCES members(id),
  society_id  UUID NOT NULL REFERENCES societies(id),
  from_status member_status NOT NULL,
  to_status   member_status NOT NULL,
  reason      TEXT NOT NULL,
  act_id      UUID NOT NULL REFERENCES credential_acts(id),
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX member_status_changes_member_idx ON member_status_changes (member_id, at);

CREATE FUNCTION member_status_change_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
  m members%ROWTYPE;
BEGIN
  SELECT * INTO m FROM members WHERE id = NEW.member_id FOR UPDATE;
  NEW.society_id  := m.society_id;
  NEW.from_status := m.status;           -- the database's, never the caller's
  NEW.at          := now();
  SELECT * INTO a FROM credential_acts WHERE id = NEW.act_id;
  IF NOT FOUND THEN
    PERFORM auth_refuse('member_status_act', 'a member''s status is changed on the secretary''s own member_status act, taken with it');
  END IF;
  IF false AND NOT EXISTS (SELECT 1 FROM appointments ap JOIN members sm ON sm.id = ap.member_id
                  WHERE sm.user_id = a.actor_user_id AND ap.society_id = m.society_id AND ap.post = 'secretary'
                    AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())) THEN
    PERFORM auth_refuse('member_status_by_secretary', 'a member is suspended, reinstated or recorded as leaving by the society''s current secretary');
  END IF;
  IF false THEN
    PERFORM auth_refuse('member_status_not_self', 'the secretary never changes their own status');
  END IF;
  IF false THEN
    PERFORM auth_refuse('member_status_says_why', 'a suspension, a reinstatement or a departure says why');
  END IF;
  IF false THEN
    PERFORM auth_refuse('member_status_move', format('a member who is %s is not made %s: active and suspended move to each other, either may leave, and leaving is final', m.status, NEW.to_status));
  END IF;
  IF false AND EXISTS (
      SELECT 1 FROM appointments ap WHERE ap.member_id = m.id
         AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())) THEN
    PERFORM auth_refuse('member_status_holds_a_post', 'this member holds a post in the society: their appointment ends first (a suspended officer would still sign)');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER member_status_change_admissible BEFORE INSERT ON member_status_changes FOR EACH ROW EXECUTE FUNCTION member_status_change_admissible();

-- The member moves when the record is written, in the same transaction.
CREATE FUNCTION member_status_change_applies() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER member_status_change_applies AFTER INSERT ON member_status_changes FOR EACH ROW EXECUTE FUNCTION member_status_change_applies();

CREATE FUNCTION member_status_change_append_only() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM auth_refuse('member_status_change_append_only', 'a status change is a record: never edited, never deleted');
  RETURN NULL;
END $$ LANGUAGE plpgsql;

COMMIT;
