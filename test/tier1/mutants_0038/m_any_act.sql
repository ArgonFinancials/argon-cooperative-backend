-- DELIBERATELY WRONG (m_any_act). GENERATED (gen0038.py): a full copy of 0038 with
-- one rule broken, applied over 0038_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
DROP TABLE member_status_changes;
DROP FUNCTION member_status_change_append_only();
DROP FUNCTION member_status_change_applies();
DROP FUNCTION member_status_change_admissible();
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email'));
COMMIT;

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
  IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members sm ON sm.id = ap.member_id
                  WHERE sm.user_id = a.actor_user_id AND ap.society_id = m.society_id AND ap.post = 'secretary'
                    AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())) THEN
    PERFORM auth_refuse('member_status_by_secretary', 'a member is suspended, reinstated or recorded as leaving by the society''s current secretary');
  END IF;
  IF m.user_id IS NOT DISTINCT FROM a.actor_user_id THEN
    PERFORM auth_refuse('member_status_not_self', 'the secretary never changes their own status');
  END IF;
  IF btrim(coalesce(NEW.reason, '')) = '' THEN
    PERFORM auth_refuse('member_status_says_why', 'a suspension, a reinstatement or a departure says why');
  END IF;
  IF (m.status, NEW.to_status) NOT IN (('active', 'suspended'), ('suspended', 'active'), ('active', 'exited'), ('suspended', 'exited')) THEN
    PERFORM auth_refuse('member_status_move', format('a member who is %s is not made %s: active and suspended move to each other, either may leave, and leaving is final', m.status, NEW.to_status));
  END IF;
  IF NEW.to_status IN ('suspended', 'exited') AND EXISTS (
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
  UPDATE members SET status = NEW.to_status WHERE id = NEW.member_id;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER member_status_change_applies AFTER INSERT ON member_status_changes FOR EACH ROW EXECUTE FUNCTION member_status_change_applies();

CREATE FUNCTION member_status_change_append_only() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM auth_refuse('member_status_change_append_only', 'a status change is a record: never edited, never deleted');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER member_status_change_append_only BEFORE UPDATE OR DELETE ON member_status_changes FOR EACH ROW EXECUTE FUNCTION member_status_change_append_only();

COMMIT;
