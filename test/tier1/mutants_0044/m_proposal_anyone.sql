-- DELIBERATELY WRONG (m_proposal_anyone). GENERATED (gen0044.py): a full copy of 0044 with
-- one rule broken, applied over 0044_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
ALTER FUNCTION ledger_entry_not_in_closed_period() SECURITY INVOKER RESET search_path;
DROP TRIGGER IF EXISTS ledger_entries_outside_closed_period ON ledger_entries;
DROP FUNCTION IF EXISTS entry_outside_closed_period();
DROP TABLE IF EXISTS period_close_decisions;
DROP FUNCTION IF EXISTS period_close_decision_admissible();
DROP TABLE IF EXISTS period_closes;
DROP FUNCTION IF EXISTS period_close_admissible();
DROP FUNCTION IF EXISTS society_closed_through(UUID);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));
COMMIT;

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision',
  'period_close', 'period_close_decision'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision',
  'period_close', 'period_close_decision'));

CREATE TABLE period_closes (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id   UUID NOT NULL REFERENCES societies(id),
  through      DATE NOT NULL,
  proposed_by  UUID NOT NULL REFERENCES users(id),
  proposed_act UUID NOT NULL REFERENCES credential_acts(id),
  at           TIMESTAMPTZ NOT NULL
);
CREATE INDEX period_closes_society ON period_closes (society_id, at DESC);

CREATE TABLE period_close_decisions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  close_id    UUID NOT NULL REFERENCES period_closes(id),
  confirmed   BOOLEAN NOT NULL,
  decided_by  UUID NOT NULL REFERENCES users(id),
  decided_act UUID NOT NULL REFERENCES credential_acts(id),
  reason      TEXT,
  trial_balance JSONB,   -- a confirmation: every account's balance through the date
  at          TIMESTAMPTZ NOT NULL,
  CONSTRAINT period_close_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)
);
CREATE UNIQUE INDEX period_close_decided_once ON period_close_decisions (close_id);

-- Through when the society's books are closed: the latest confirmed close.
CREATE FUNCTION society_closed_through(p_society UUID) RETURNS DATE STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT max(c.through) FROM period_closes c JOIN period_close_decisions k ON k.close_id = c.id AND k.confirmed
   WHERE c.society_id = p_society
$$;

CREATE FUNCTION period_close_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
BEGIN
  PERFORM 1 FROM societies WHERE id = NEW.society_id FOR UPDATE;   -- one close decision at a time per society
  NEW.at := clock_timestamp();
  SELECT * INTO a FROM credential_acts WHERE id = NEW.proposed_act;
  IF NOT FOUND OR a.kind <> 'period_close' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('period_close_act', 'a close is proposed on the proposer''s own period_close act, taken with it');
  END IF;
  NEW.proposed_by := a.actor_user_id;
  IF false THEN
    PERFORM auth_refuse('period_close_by_officer', 'a close is proposed by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.through > society_today() THEN
    PERFORM auth_refuse('period_close_not_future', 'the books are closed through a day that has ended or is today, never a day to come');
  END IF;
  IF NEW.through <= coalesce(society_closed_through(NEW.society_id), '-infinity'::date) THEN
    PERFORM auth_refuse('period_close_forward', format('the books are already closed through %s: a close only moves forward', society_closed_through(NEW.society_id)));
  END IF;
  IF EXISTS (SELECT 1 FROM period_closes c WHERE c.society_id = NEW.society_id
              AND NOT EXISTS (SELECT 1 FROM period_close_decisions k WHERE k.close_id = c.id)) THEN
    PERFORM auth_refuse('period_close_waits', 'a close of this society''s books already waits for a second officer');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER period_closes_admissible BEFORE INSERT ON period_closes FOR EACH ROW EXECUTE FUNCTION period_close_admissible();

CREATE FUNCTION period_close_decision_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c period_closes%ROWTYPE;
  a credential_acts%ROWTYPE;
  total BIGINT;
BEGIN
  SELECT * INTO c FROM period_closes WHERE id = NEW.close_id;
  PERFORM 1 FROM societies WHERE id = c.society_id FOR UPDATE;
  NEW.at := clock_timestamp();
  NEW.trial_balance := NULL;
  SELECT * INTO a FROM credential_acts WHERE id = NEW.decided_act;
  IF NOT FOUND OR a.kind <> 'period_close_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('period_close_decision_act', 'a close is decided on the decider''s own period_close_decision act, taken with it');
  END IF;
  NEW.decided_by := a.actor_user_id;
  IF NOT dispute_officer(NEW.decided_by, c.society_id) THEN
    PERFORM auth_refuse('period_close_decision_by_officer', 'a close is decided by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.decided_by = c.proposed_by THEN
    PERFORM auth_refuse('period_close_second_officer', 'a close needs a second officer: whoever proposed it does not decide it');
  END IF;
  IF NEW.confirmed THEN
    IF c.through <= coalesce(society_closed_through(c.society_id), '-infinity'::date) THEN
      PERFORM auth_refuse('period_close_forward', 'the books were closed further meanwhile: a close only moves forward');
    END IF;
    SELECT coalesce(sum(l.amount_kobo), 0) INTO total
      FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id
     WHERE e.society_id = c.society_id AND (e.value_date IS NULL OR e.value_date <= c.through);
    IF total <> 0 THEN
      PERFORM auth_refuse('period_close_balances', format('the trial balance through %s sums to %s kobo, not zero: a period is never closed over books that do not balance', c.through, total));
    END IF;
    SELECT coalesce(jsonb_agg(jsonb_build_object('code', t.code, 'name', t.name, 'kind', t.kind, 'balance_kobo', t.bal) ORDER BY t.code), '[]'::jsonb)
      INTO NEW.trial_balance
      FROM (SELECT a2.code, a2.name, a2.kind::text AS kind, coalesce(sum(l.amount_kobo), 0) AS bal
              FROM accounts a2
              LEFT JOIN (ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id AND (e.value_date IS NULL OR e.value_date <= c.through))
                     ON l.account_id = a2.id
             WHERE a2.society_id = c.society_id
             GROUP BY a2.id, a2.code, a2.name, a2.kind) t;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER period_close_decisions_admissible BEFORE INSERT ON period_close_decisions FOR EACH ROW EXECUTE FUNCTION period_close_decision_admissible();

-- CLOSED MEANS READ-ONLY: nothing posts into a closed period.
CREATE FUNCTION entry_outside_closed_period() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  closed DATE := society_closed_through(NEW.society_id);
BEGIN
  IF closed IS NOT NULL AND (NEW.value_date IS NULL OR NEW.value_date <= closed) THEN
    PERFORM auth_refuse('period_closed', format('the books are closed through %s: nothing is posted with a value date in a closed period. A correction posts today, referencing what it corrects', closed));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_entries_outside_closed_period BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION entry_outside_closed_period();

-- 0002's closed-period check locks the period it reads (FOR SHARE), which
-- needs UPDATE on accounting_periods; it ran as the caller. The app no longer
-- closes a period that way (grants.sql revokes its UPDATE, P100), so the
-- check runs as the owner, or every posting the app made would be refused.
ALTER FUNCTION ledger_entry_not_in_closed_period() SECURITY DEFINER SET search_path = public, pg_temp;

CREATE TRIGGER period_closes_append_only BEFORE UPDATE OR DELETE ON period_closes FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER period_close_decisions_append_only BEFORE UPDATE OR DELETE ON period_close_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

COMMIT;
