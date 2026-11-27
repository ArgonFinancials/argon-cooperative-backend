-- DELIBERATELY WRONG (m_any_member). GENERATED (gen0043.py): a full copy of 0043 with
-- one rule broken, applied over 0043_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
DROP TABLE IF EXISTS entry_correction_decisions;
DROP FUNCTION IF EXISTS entry_correction_decision_admissible();
DROP FUNCTION IF EXISTS entry_correction_post(UUID, UUID, UUID);
DROP TABLE IF EXISTS entry_corrections;
DROP FUNCTION IF EXISTS entry_correction_admissible();
DROP FUNCTION IF EXISTS entry_names_user(UUID, UUID);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision'));
COMMIT;

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));

CREATE TABLE entry_corrections (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id   UUID NOT NULL REFERENCES societies(id),
  entry_id     UUID NOT NULL REFERENCES ledger_entries(id),
  kind         TEXT NOT NULL CONSTRAINT entry_correction_kind CHECK (kind IN ('reversal', 'adjustment')),
  lines        JSONB,
  reason       TEXT NOT NULL CONSTRAINT entry_correction_says_why CHECK (length(btrim(reason)) BETWEEN 10 AND 500),
  proposed_by  UUID NOT NULL REFERENCES users(id),
  proposed_act UUID NOT NULL REFERENCES credential_acts(id),
  at           TIMESTAMPTZ NOT NULL,
  CONSTRAINT entry_correction_lines_for_adjustment CHECK ((kind = 'adjustment') = (lines IS NOT NULL))
);
CREATE INDEX entry_corrections_society ON entry_corrections (society_id, at DESC);

CREATE TABLE entry_correction_decisions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  correction_id UUID NOT NULL REFERENCES entry_corrections(id),
  confirmed     BOOLEAN NOT NULL,
  decided_by    UUID NOT NULL REFERENCES users(id),
  decided_act   UUID NOT NULL REFERENCES credential_acts(id),
  reason        TEXT,
  entry_id      UUID REFERENCES ledger_entries(id),   -- what a confirmation posted
  at            TIMESTAMPTZ NOT NULL,
  CONSTRAINT entry_correction_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)
);
CREATE UNIQUE INDEX entry_correction_decided_once ON entry_correction_decisions (correction_id);

-- The members an entry names: none of them proposes or decides its correction.
CREATE FUNCTION entry_names_user(p_entry UUID, p_user UUID) RETURNS BOOLEAN STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT EXISTS (SELECT 1 FROM ledger_lines l JOIN members m ON m.id = l.member_id WHERE l.entry_id = p_entry AND m.user_id = p_user)
$$;

CREATE FUNCTION entry_correction_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
  o ledger_entries%ROWTYPE;
  l JSONB;
  total BIGINT := 0;
BEGIN
  SELECT * INTO o FROM ledger_entries WHERE id = NEW.entry_id FOR UPDATE;   -- one correction decision at a time per entry
  NEW.society_id := o.society_id;
  NEW.at := clock_timestamp();
  SELECT * INTO a FROM credential_acts WHERE id = NEW.proposed_act;
  IF NOT FOUND OR a.kind <> 'entry_correction' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('entry_correction_act', 'a correction is proposed on the proposer''s own entry_correction act, taken with it');
  END IF;
  NEW.proposed_by := a.actor_user_id;
  IF NOT dispute_officer(NEW.proposed_by, o.society_id) THEN
    PERFORM auth_refuse('entry_correction_by_officer', 'a correction is proposed by a current officer of the society, not the internal auditor');
  END IF;
  IF entry_names_user(o.id, NEW.proposed_by) THEN
    PERFORM auth_refuse('entry_correction_not_own', 'nobody corrects an entry that names them');
  END IF;
  IF NOT dispute_correctable(o.id) THEN
    PERFORM auth_refuse('entry_correctable', 'this entry is not corrected by officers: it was posted on VFD''s word (the money moved at the bank), is itself a correction, or is already reversed');
  END IF;
  IF EXISTS (SELECT 1 FROM entry_corrections c WHERE c.entry_id = o.id
              AND NOT EXISTS (SELECT 1 FROM entry_correction_decisions k WHERE k.correction_id = c.id)) THEN
    PERFORM auth_refuse('entry_correction_waits', 'a correction of this entry already waits for a second officer');
  END IF;
  IF NEW.kind = 'adjustment' THEN
    IF jsonb_typeof(NEW.lines) <> 'array' OR jsonb_array_length(NEW.lines) < 2 THEN
      PERFORM auth_refuse('adjustment_lines_balance', 'an adjustment is two or more lines that sum to zero');
    END IF;
    FOR l IN SELECT * FROM jsonb_array_elements(NEW.lines) LOOP
      IF coalesce((l->>'amount_kobo')::bigint, 0) = 0 THEN
        PERFORM auth_refuse('adjustment_lines_balance', 'an adjustment''s every line moves money: none is zero');
      END IF;
      total := total + (l->>'amount_kobo')::bigint;
      IF NOT EXISTS (SELECT 1 FROM ledger_lines x WHERE x.entry_id = o.id AND x.account_id = (l->>'account_id')::uuid) THEN
        PERFORM auth_refuse('adjustment_on_the_entrys_accounts', 'an adjustment touches only the accounts and members of the entry it adjusts: it corrects its figures, it never moves money elsewhere');
      END IF;
    END LOOP;
    IF total <> 0 THEN
      PERFORM auth_refuse('adjustment_lines_balance', format('an adjustment''s lines sum to zero; these sum to %s kobo', total));
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER entry_corrections_admissible BEFORE INSERT ON entry_corrections FOR EACH ROW EXECUTE FUNCTION entry_correction_admissible();

-- What a confirmation posts: today, its actor the confirming officer on the
-- decision's act; the proposer named in the label.
CREATE FUNCTION entry_correction_post(p_correction UUID, p_act UUID, p_by UUID) RETURNS UUID SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c entry_corrections%ROWTYPE;
  o ledger_entries%ROWTYPE;
  e UUID;
  post officer_post;
BEGIN
  SELECT * INTO c FROM entry_corrections WHERE id = p_correction;
  SELECT * INTO o FROM ledger_entries WHERE id = c.entry_id;
  SELECT ap.post INTO post FROM appointments ap JOIN members om ON om.id = ap.member_id
   WHERE om.user_id = p_by AND ap.society_id = o.society_id
     AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())
   ORDER BY ap.is_signatory DESC, ap.held_from DESC LIMIT 1;   -- a description: who may confirm is the decision's rule
  INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, reverses_id, adjusts_id, reason, client_reference, act_id)
    VALUES (o.society_id, o.kind, society_today(), p_by, post,
            coalesce(initcap(replace(post::text, '_', ' ')) || ', confirming', 'Confirming') || ' a ' || c.kind || ' proposed by '
              || coalesce((SELECT full_name FROM users WHERE id = c.proposed_by), 'another officer'),
            CASE WHEN c.kind = 'reversal' THEN o.id END, CASE WHEN c.kind = 'adjustment' THEN o.id END,
            c.reason, 'correction:' || c.id, p_act)
    RETURNING id INTO e;
  IF c.kind = 'reversal' THEN
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role)
      SELECT e, l.account_id, -l.amount_kobo, l.member_id, l.charge_role FROM ledger_lines l WHERE l.entry_id = o.id;
  ELSE
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id)
      SELECT e, (x->>'account_id')::uuid, (x->>'amount_kobo')::bigint, (x->>'member_id')::uuid FROM jsonb_array_elements(c.lines) x;
  END IF;
  RETURN e;
END $$ LANGUAGE plpgsql;
REVOKE ALL ON FUNCTION entry_correction_post(UUID, UUID, UUID) FROM PUBLIC;

CREATE FUNCTION entry_correction_decision_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c entry_corrections%ROWTYPE;
  a credential_acts%ROWTYPE;
BEGIN
  SELECT * INTO c FROM entry_corrections WHERE id = NEW.correction_id;
  PERFORM 1 FROM ledger_entries WHERE id = c.entry_id FOR UPDATE;
  NEW.at := clock_timestamp();
  NEW.entry_id := NULL;
  SELECT * INTO a FROM credential_acts WHERE id = NEW.decided_act;
  IF NOT FOUND OR a.kind <> 'entry_correction_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('entry_decision_act', 'a correction is decided on the decider''s own entry_correction_decision act, taken with it');
  END IF;
  NEW.decided_by := a.actor_user_id;
  IF NOT dispute_officer(NEW.decided_by, c.society_id) THEN
    PERFORM auth_refuse('entry_decision_by_officer', 'a correction is decided by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.decided_by = c.proposed_by THEN
    PERFORM auth_refuse('entry_second_officer', 'a correction needs a second officer: whoever proposed it does not decide it');
  END IF;
  IF entry_names_user(c.entry_id, NEW.decided_by) THEN
    PERFORM auth_refuse('entry_correction_not_own', 'nobody decides a correction of an entry that names them');
  END IF;
  IF NEW.confirmed THEN
    IF NOT dispute_correctable(c.entry_id) THEN
      PERFORM auth_refuse('entry_correctable', 'this entry can no longer be corrected this way: it is already reversed');
    END IF;
    NEW.entry_id := entry_correction_post(c.id, NEW.decided_act, NEW.decided_by);
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER entry_correction_decisions_admissible BEFORE INSERT ON entry_correction_decisions FOR EACH ROW EXECUTE FUNCTION entry_correction_decision_admissible();

CREATE TRIGGER entry_corrections_append_only BEFORE UPDATE OR DELETE ON entry_corrections FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER entry_correction_decisions_append_only BEFORE UPDATE OR DELETE ON entry_correction_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

COMMIT;
