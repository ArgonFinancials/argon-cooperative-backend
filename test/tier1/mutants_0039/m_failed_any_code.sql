-- DELIBERATELY WRONG (m_failed_any_code). GENERATED (gen0039.py): a full copy of 0039 with
-- one rule broken, applied over 0039_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
CREATE OR REPLACE VIEW wallet_exceptions AS
  -- No answer from VFD 30 minutes after sending (§13, PROVISIONAL).
  SELECT 'transfer_stuck'::text AS kind, h.society_id, t.id AS subject_id, s.at AS since,
         format('transfer %s: sent %s, VFD has given no definitive answer', t.reference, s.at) AS detail
    FROM vfd_transfers t JOIN withdrawal_holds h ON h.id = t.hold_id
    JOIN vfd_transfer_events s ON s.transfer_id = t.id AND s.kind = 'sent'
   WHERE NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o WHERE o.transfer_id = t.id)
     AND s.at < now() - interval '30 minutes'
  UNION ALL
  -- Failed, and not yet sent again (one signatory's action sends it again, §13).
  SELECT 'transfer_failed', h.society_id, t.id, o.at,
         format('transfer %s failed; the hold was released; a signatory may send it again', t.reference)
    FROM vfd_transfer_outcomes o JOIN vfd_transfers t ON t.id = o.transfer_id JOIN withdrawal_holds h ON h.id = t.hold_id
   WHERE o.result = 'failed'
     AND NOT EXISTS (SELECT 1 FROM withdrawal_holds h2 WHERE h2.request_id = h.request_id AND h2.held_at > h.held_at)
  UNION ALL
  -- A hold the last signature placed that has not gone out (no VFD account,
  -- VFD not confirming the member's account, the worker not running).
  SELECT 'hold_not_sent', h.society_id, h.id, h.held_at,
         format('hold %s: approved, no transfer sent', h.id)
    FROM withdrawal_holds h JOIN credential_acts a ON a.id = h.act_id AND a.kind = 'approval_signature'
   WHERE NOT EXISTS (SELECT 1 FROM vfd_transfers t WHERE t.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id)
     AND h.held_at < now() - interval '10 minutes'
  UNION ALL
  -- A notification VFD sent that is not confirmed, ten minutes on.
  SELECT 'notification_unconfirmed', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = n.account_number AND a.member_id IS NULL),
         n.id, n.received_at,
         format('notification %s (reference %s): %s', n.id, coalesce(n.reference, 'none'),
                coalesce((SELECT at.result || ': ' || coalesce(at.detail, '') FROM vfd_notification_attempts at WHERE at.notification_id = n.id ORDER BY at.at DESC LIMIT 1), 'never attempted'))
    FROM vfd_notifications n
   WHERE n.kind = 'inflow'
     AND NOT EXISTS (SELECT 1 FROM vfd_notification_attempts at WHERE at.notification_id = n.id AND at.result IN ('confirmed', 'duplicate'))
     AND n.received_at < now() - interval '10 minutes'
  UNION ALL
  -- Money arrived and nobody has claimed it (§12: it waits for an officer).
  SELECT 'inflow_unclaimed', i.society_id, i.id, i.confirmed_at,
         format('%s kobo arrived (VFD reference %s) from %s: nobody has claimed it', i.amount_kobo, i.vfd_reference, coalesce(i.originator_name, 'an unnamed sender'))
    FROM vfd_inflows i
   WHERE NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)
  UNION ALL
  -- Two members claim one payment, or a payment credited to one is claimed by
  -- another: nobody is credited on a guess (§12); a person decides.
  SELECT 'claim_contested', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo, session %s, is contested: another member claims the same payment, or it was credited to them', s.member_id, s.amount_kobo, s.session_id)
    FROM contribution_claim_states s WHERE s.state = 'contested'
  UNION ALL
  -- What arrived is not what was claimed.
  SELECT 'claim_amount_differs', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo claimed, session %s; VFD confirmed a different amount', s.member_id, s.amount_kobo, s.session_id)
    FROM contribution_claim_states s WHERE s.state = 'amount_differs'
  UNION ALL
  -- A claim VFD has not confirmed a day on (claim_wait_window, PROVISIONAL).
  SELECT 'claim_unconfirmed', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo, session %s, made %s: VFD has confirmed no such payment into the society''s account', s.member_id, s.amount_kobo, s.session_id, s.claimed_at)
    FROM contribution_claim_states s WHERE s.state = 'waiting' AND s.claimed_at < now() - claim_wait_window()
  UNION ALL
  -- One officer proposed; a second must confirm or reject it.
  SELECT 'resolution_awaiting_confirmation', r.society_id, r.id, r.proposed_at,
         format('%s proposed by an officer: %s. A second officer confirms or rejects it', CASE r.decision WHEN 'credit' THEN format('crediting payment %s to member %s', r.inflow_id, r.member_id) ELSE format('declining claim %s', r.claim_id) END, r.reason)
    FROM payment_resolutions r JOIN payment_resolutions_open o ON o.resolution_id = r.id
  UNION ALL
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);
ALTER TABLE vfd_notifications DROP CONSTRAINT IF EXISTS vfd_notification_source_known;
ALTER TABLE vfd_notifications DROP COLUMN IF EXISTS source;
CREATE OR REPLACE FUNCTION vfd_outcome_recorded() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t     vfd_transfers%ROWTYPE;
  h     withdrawal_holds%ROWTYPE;
  ev    vfd_transfer_events%ROWTYPE;
  sent  TIMESTAMPTZ;
  e     UUID;
  acct  RECORD;
BEGIN
  SELECT * INTO t FROM vfd_transfers WHERE id = NEW.transfer_id;
  SELECT * INTO h FROM withdrawal_holds WHERE id = t.hold_id;
  SELECT * INTO ev FROM vfd_transfer_events WHERE id = NEW.evidence_id;
  IF NEW.result = 'failed' THEN
    INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason, vfd_outcome_id)
      VALUES (h.id, NULL, format('VFD: the transfer %s failed (%s %s%s)', t.reference, ev.kind, ev.result, coalesce(', code ' || ev.code, '')), NEW.id);
    RETURN NULL;
  END IF;
  SELECT at INTO sent FROM vfd_transfer_events WHERE transfer_id = t.id AND kind = 'sent';
  INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, request_id, hold_id, payout_reference, vfd_outcome_id)
    VALUES (h.society_id, 'withdrawal_out', (sent AT TIME ZONE 'Africa/Lagos')::date, NULL, NULL,
            'Argon system, on VFD''s confirmation', h.request_id, h.id, t.reference, NEW.id)
    RETURNING id INTO e;
  FOR acct IN
    SELECT a.id, l.kobo, l.member, l.charge_role FROM (VALUES
        ('member_savings', ev.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo, h.member_id, 'principal'::text),
        ('bank', -ev.amount_kobo, NULL::uuid, 'principal'),
        ('charges_partner', -h.partner_fee_kobo, NULL, 'partner_charge'),
        ('charges_vat', -h.vat_kobo, NULL, 'vat'),
        ('statutory_levy_payable', -h.statutory_levy_kobo, NULL, 'statutory_levy'),
        ('charges_margin', -h.argon_margin_kobo, NULL, 'argon_margin')) AS l(role, kobo, member, charge_role)
      JOIN accounts a ON a.society_id = h.society_id AND a.role = l.role
     WHERE l.kobo <> 0
  LOOP
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role) VALUES (e, acct.id, acct.kobo, acct.member, acct.charge_role);
  END LOOP;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION vfd_outcome_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  ev   vfd_transfer_events%ROWTYPE;
  sent TIMESTAMPTZ;
BEGIN
  SELECT * INTO ev FROM vfd_transfer_events WHERE id = NEW.evidence_id;
  IF ev.transfer_id IS DISTINCT FROM NEW.transfer_id THEN
    PERFORM auth_refuse('vfd_outcome_evidence_is_its_own', 'an outcome rests on what VFD said about THIS transfer');
  END IF;
  IF NEW.result = 'paid' AND NOT (ev.kind = 'requeried' AND ev.result = 'succeeded') THEN
    PERFORM auth_refuse('vfd_paid_on_success', format('paid only when VFD says the transfer succeeded, not on %s %s', ev.kind, ev.result));
  END IF;
  IF NEW.result = 'failed' THEN
    SELECT at INTO sent FROM vfd_transfer_events WHERE transfer_id = NEW.transfer_id AND kind = 'sent';
    IF NOT ((ev.kind = 'sent' AND ev.result IN ('refused', 'not_reached'))
         OR (ev.kind = 'requeried' AND ev.result = 'not_found' AND ev.at >= sent + vfd_settlement_window())) THEN
      PERFORM auth_refuse('vfd_failed_only_definitive', format('failed only on a definitive answer: refused or never reached at the send, or not found %s after it; not %s %s at %s', vfd_settlement_window(), ev.kind, ev.result, ev.at - sent));
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h withdrawal_holds%ROWTYPE;
BEGIN
  SELECT * INTO h FROM withdrawal_holds WHERE id = NEW.hold_id FOR UPDATE;   -- one transfer decision at a time per hold
  IF NEW.reference IS DISTINCT FROM vfd_transfer_reference(h.id) THEN
    PERFORM auth_refuse('vfd_reference_is_the_holds', format('a transfer''s reference is derived from its hold: %s, not %s', vfd_transfer_reference(h.id), NEW.reference));
  END IF;
  IF NEW.amount_kobo IS DISTINCT FROM h.amount_kobo THEN
    PERFORM auth_refuse('vfd_transfer_is_the_holds', format('a transfer moves what was held: %s kobo, not %s', h.amount_kobo, NEW.amount_kobo));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE member_id = h.member_id AND account_number = NEW.to_account) THEN
    PERFORM auth_refuse('vfd_transfer_to_members_account', 'a payout goes into the member''s own VFD account and no other (§12, 2026-11-10); this member has none, or this is not it');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE society_id = h.society_id AND member_id IS NULL AND account_number = NEW.from_account) THEN
    PERFORM auth_refuse('vfd_transfer_from_society_account', 'a payout leaves from the society''s own VFD account');
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id) THEN
    PERFORM auth_refuse('vfd_transfer_on_open_hold', 'a transfer is sent on an open hold: this one was released or paid');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION vfd_settlement_window() RETURNS INTERVAL LANGUAGE sql IMMUTABLE AS $$ SELECT interval '2 hours' $$;
DROP TABLE IF EXISTS vfd_pool;
ALTER TABLE vfd_transfer_events DROP COLUMN IF EXISTS reply;
ALTER TABLE vfd_transfer_events DROP COLUMN IF EXISTS http_status;
COMMIT;

BEGIN;

ALTER TABLE vfd_transfer_events ADD COLUMN http_status INT;
ALTER TABLE vfd_transfer_events ADD COLUMN reply TEXT;

CREATE TABLE vfd_pool (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_number TEXT NOT NULL CONSTRAINT vfd_pool_number_shape CHECK (account_number ~ '^[0-9]{10}$'),
  account_id     TEXT NOT NULL,
  client_id      TEXT NOT NULL,
  client_name    TEXT NOT NULL,
  recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vfd_pool_once ON vfd_pool ((true));
CREATE TRIGGER vfd_pool_append_only BEFORE UPDATE OR DELETE ON vfd_pool FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- 108 counts only 30 minutes or more after the send (§1.6).
CREATE OR REPLACE FUNCTION vfd_settlement_window() RETURNS INTERVAL LANGUAGE sql IMMUTABLE AS $$ SELECT interval '30 minutes' $$;

CREATE OR REPLACE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h withdrawal_holds%ROWTYPE;
BEGIN
  SELECT * INTO h FROM withdrawal_holds WHERE id = NEW.hold_id FOR UPDATE;   -- one transfer decision at a time per hold
  IF NEW.reference IS DISTINCT FROM vfd_transfer_reference(h.id) THEN
    PERFORM auth_refuse('vfd_reference_is_the_holds', format('a transfer''s reference is derived from its hold: %s, not %s', vfd_transfer_reference(h.id), NEW.reference));
  END IF;
  IF NEW.amount_kobo IS DISTINCT FROM h.amount_kobo THEN
    PERFORM auth_refuse('vfd_transfer_is_the_holds', format('a transfer moves what was held: %s kobo, not %s', h.amount_kobo, NEW.amount_kobo));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE member_id = h.member_id AND account_number = NEW.to_account) THEN
    PERFORM auth_refuse('vfd_transfer_to_members_account', 'a payout goes into the member''s own VFD account and no other (§12, 2026-11-10); this member has none, or this is not it');
  END IF;
  -- From the pool (0039, §1.1): every transfer leaves Argon's VFD pool, the
  -- account VFD's enquiry names; a society's account holds nothing (VFD sweeps
  -- its deposits into the pool).
  IF NOT EXISTS (SELECT 1 FROM vfd_pool WHERE account_number = NEW.from_account) THEN
    PERFORM auth_refuse('vfd_transfer_from_pool', 'a payout leaves from Argon''s VFD pool, the account VFD''s enquiry names (§1.1); none is recorded, or this is not it');
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id) THEN
    PERFORM auth_refuse('vfd_transfer_on_open_hold', 'a transfer is sent on an open hold: this one was released or paid');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION vfd_outcome_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  ev   vfd_transfer_events%ROWTYPE;
  prev vfd_transfer_events%ROWTYPE;
  sent TIMESTAMPTZ;
BEGIN
  SELECT * INTO ev FROM vfd_transfer_events WHERE id = NEW.evidence_id;
  IF ev.transfer_id IS DISTINCT FROM NEW.transfer_id THEN
    PERFORM auth_refuse('vfd_outcome_evidence_is_its_own', 'an outcome rests on what VFD said about THIS transfer');
  END IF;
  -- Paid (§1.6): VFD's 00 to the send, or 00 with transactionStatus 00 to a
  -- requery. Nothing else: never 99, 91, 25 or a word.
  IF NEW.result = 'paid' AND NOT ((ev.kind = 'sent' AND ev.result = 'accepted' AND ev.code = '00')
                               OR (ev.kind = 'requeried' AND ev.result = 'succeeded' AND ev.code = '00' AND ev.txn_status = '00')) THEN
    PERFORM auth_refuse('vfd_paid_on_success', format('paid only on VFD''s 00 to the send, or 00 with transaction status 00 to a requery; not on %s %s (status %s, transaction status %s)', ev.kind, ev.result, coalesce(ev.code, 'none'), coalesce(ev.txn_status, 'none')));
  END IF;
  IF NEW.result = 'failed' THEN
    -- Failed (§1.6): ONLY 108, seen twice in a row, both at least
    -- vfd_settlement_window() after the send. A refused send is not a failure:
    -- in 3 of 32 such sends VFD had already created the transfer and sent it.
    SELECT at INTO sent FROM vfd_transfer_events WHERE transfer_id = NEW.transfer_id AND kind = 'sent';
    SELECT * INTO prev FROM vfd_transfer_events
     WHERE transfer_id = NEW.transfer_id AND kind = 'requeried' AND id <> ev.id AND (at, id::text) < (ev.at, ev.id::text)
     ORDER BY at DESC, id::text DESC LIMIT 1;
    -- coalesce: no earlier requery is no second 108 (a NULL would let it through).
    IF NOT coalesce(ev.kind = 'requeried' AND ev.result = 'not_found' AND ev.at >= sent + vfd_settlement_window()
            AND prev.result = 'not_found' AND prev.at >= sent + vfd_settlement_window(), false) THEN
      PERFORM auth_refuse('vfd_failed_only_definitive', format('failed only on VFD''s 108, twice in a row, both %s or more after the send; not %s %s (status %s) at %s', vfd_settlement_window(), ev.kind, ev.result, coalesce(ev.code, 'none'), ev.at - sent));
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION vfd_outcome_recorded() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t     vfd_transfers%ROWTYPE;
  h     withdrawal_holds%ROWTYPE;
  ev    vfd_transfer_events%ROWTYPE;
  sent  TIMESTAMPTZ;
  e     UUID;
  acct  RECORD;
BEGIN
  -- Paid on the send's 00 (0039): what was sent, the transfer's amount; on a
  -- requery's 00/00: what VFD says it paid.
  SELECT * INTO t FROM vfd_transfers WHERE id = NEW.transfer_id;
  SELECT * INTO h FROM withdrawal_holds WHERE id = t.hold_id;
  SELECT * INTO ev FROM vfd_transfer_events WHERE id = NEW.evidence_id;
  IF NEW.result = 'failed' THEN
    INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason, vfd_outcome_id)
      VALUES (h.id, NULL, format('VFD: the transfer %s failed (%s %s%s)', t.reference, ev.kind, ev.result, coalesce(', code ' || ev.code, '')), NEW.id);
    RETURN NULL;
  END IF;
  SELECT at INTO sent FROM vfd_transfer_events WHERE transfer_id = t.id AND kind = 'sent';
  INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, request_id, hold_id, payout_reference, vfd_outcome_id)
    VALUES (h.society_id, 'withdrawal_out', (sent AT TIME ZONE 'Africa/Lagos')::date, NULL, NULL,
            'Argon system, on VFD''s confirmation', h.request_id, h.id, t.reference, NEW.id)
    RETURNING id INTO e;
  FOR acct IN
    SELECT a.id, l.kobo, l.member, l.charge_role FROM (VALUES
        ('member_savings', coalesce(ev.amount_kobo, t.amount_kobo) + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo, h.member_id, 'principal'::text),
        ('bank', -coalesce(ev.amount_kobo, t.amount_kobo), NULL::uuid, 'principal'),
        ('charges_partner', -h.partner_fee_kobo, NULL, 'partner_charge'),
        ('charges_vat', -h.vat_kobo, NULL, 'vat'),
        ('statutory_levy_payable', -h.statutory_levy_kobo, NULL, 'statutory_levy'),
        ('charges_margin', -h.argon_margin_kobo, NULL, 'argon_margin')) AS l(role, kobo, member, charge_role)
      JOIN accounts a ON a.society_id = h.society_id AND a.role = l.role
     WHERE l.kobo <> 0
  LOOP
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role) VALUES (e, acct.id, acct.kobo, acct.member, acct.charge_role);
  END LOOP;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

-- Where a notification came from: VFD's webhook, or an account's history.
ALTER TABLE vfd_notifications DISABLE TRIGGER USER;
ALTER TABLE vfd_notifications ADD COLUMN source TEXT;
UPDATE vfd_notifications SET source = 'webhook';
ALTER TABLE vfd_notifications ALTER COLUMN source SET NOT NULL;
ALTER TABLE vfd_notifications ADD CONSTRAINT vfd_notification_source_known CHECK (source IN ('webhook', 'history'));
ALTER TABLE vfd_notifications ENABLE TRIGGER USER;

CREATE OR REPLACE VIEW wallet_exceptions AS
  -- No answer from VFD 30 minutes after sending (§13, PROVISIONAL).
  SELECT 'transfer_stuck'::text AS kind, h.society_id, t.id AS subject_id, s.at AS since,
         format('transfer %s: sent %s, VFD has given no definitive answer', t.reference, s.at) AS detail
    FROM vfd_transfers t JOIN withdrawal_holds h ON h.id = t.hold_id
    JOIN vfd_transfer_events s ON s.transfer_id = t.id AND s.kind = 'sent'
   WHERE NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o WHERE o.transfer_id = t.id)
     AND s.at < now() - interval '30 minutes'
  UNION ALL
  -- VFD answered something other than 00/00 or 108 (§1.6: 91, 25, 99, ...): it
  -- may have created and sent the transfer. Held, never released by the
  -- system; a person asks VFD to confirm a reversal (0039).
  SELECT 'transfer_needs_vfd', h.society_id, t.id, ev.at,
         format('transfer %s: VFD answered status %s, transaction status %s. VFD may have created and sent it: the money stays held until VFD confirms a reversal, and is never released automatically', t.reference, coalesce(ev.code, 'none'), coalesce(ev.txn_status, 'none'))
    FROM vfd_transfers t JOIN withdrawal_holds h ON h.id = t.hold_id
    JOIN LATERAL (SELECT * FROM vfd_transfer_events e WHERE e.transfer_id = t.id AND e.kind = 'requeried' ORDER BY e.at DESC, e.id::text DESC LIMIT 1) ev ON true
   WHERE ev.result = 'pending'
     AND NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o WHERE o.transfer_id = t.id)
  UNION ALL
  -- Failed, and not yet sent again (one signatory's action sends it again, §13).
  SELECT 'transfer_failed', h.society_id, t.id, o.at,
         format('transfer %s failed; the hold was released; a signatory may send it again', t.reference)
    FROM vfd_transfer_outcomes o JOIN vfd_transfers t ON t.id = o.transfer_id JOIN withdrawal_holds h ON h.id = t.hold_id
   WHERE o.result = 'failed'
     AND NOT EXISTS (SELECT 1 FROM withdrawal_holds h2 WHERE h2.request_id = h.request_id AND h2.held_at > h.held_at)
  UNION ALL
  -- A hold the last signature placed that has not gone out (no VFD account,
  -- VFD not confirming the member's account, the worker not running).
  SELECT 'hold_not_sent', h.society_id, h.id, h.held_at,
         format('hold %s: approved, no transfer sent', h.id)
    FROM withdrawal_holds h JOIN credential_acts a ON a.id = h.act_id AND a.kind = 'approval_signature'
   WHERE NOT EXISTS (SELECT 1 FROM vfd_transfers t WHERE t.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id)
     AND h.held_at < now() - interval '10 minutes'
  UNION ALL
  -- A notification VFD sent that is not confirmed, ten minutes on.
  SELECT 'notification_unconfirmed', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = n.account_number AND a.member_id IS NULL),
         n.id, n.received_at,
         format('notification %s (reference %s): %s', n.id, coalesce(n.reference, 'none'),
                coalesce((SELECT at.result || ': ' || coalesce(at.detail, '') FROM vfd_notification_attempts at WHERE at.notification_id = n.id ORDER BY at.at DESC LIMIT 1), 'never attempted'))
    FROM vfd_notifications n
   WHERE n.kind = 'inflow'
     AND NOT EXISTS (SELECT 1 FROM vfd_notification_attempts at WHERE at.notification_id = n.id AND at.result IN ('confirmed', 'duplicate'))
     AND n.received_at < now() - interval '10 minutes'
  UNION ALL
  -- Money arrived and nobody has claimed it (§12: it waits for an officer).
  SELECT 'inflow_unclaimed', i.society_id, i.id, i.confirmed_at,
         format('%s kobo arrived (VFD reference %s) from %s: nobody has claimed it', i.amount_kobo, i.vfd_reference, coalesce(i.originator_name, 'an unnamed sender'))
    FROM vfd_inflows i
   WHERE NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)
  UNION ALL
  -- Two members claim one payment, or a payment credited to one is claimed by
  -- another: nobody is credited on a guess (§12); a person decides.
  SELECT 'claim_contested', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo, session %s, is contested: another member claims the same payment, or it was credited to them', s.member_id, s.amount_kobo, s.session_id)
    FROM contribution_claim_states s WHERE s.state = 'contested'
  UNION ALL
  -- What arrived is not what was claimed.
  SELECT 'claim_amount_differs', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo claimed, session %s; VFD confirmed a different amount', s.member_id, s.amount_kobo, s.session_id)
    FROM contribution_claim_states s WHERE s.state = 'amount_differs'
  UNION ALL
  -- A claim VFD has not confirmed a day on (claim_wait_window, PROVISIONAL).
  SELECT 'claim_unconfirmed', s.society_id, s.claim_id, s.claimed_at,
         format('claim by member %s: %s kobo, session %s, made %s: VFD has confirmed no such payment into the society''s account', s.member_id, s.amount_kobo, s.session_id, s.claimed_at)
    FROM contribution_claim_states s WHERE s.state = 'waiting' AND s.claimed_at < now() - claim_wait_window()
  UNION ALL
  -- One officer proposed; a second must confirm or reject it.
  SELECT 'resolution_awaiting_confirmation', r.society_id, r.id, r.proposed_at,
         format('%s proposed by an officer: %s. A second officer confirms or rejects it', CASE r.decision WHEN 'credit' THEN format('crediting payment %s to member %s', r.inflow_id, r.member_id) ELSE format('declining claim %s', r.claim_id) END, r.reason)
    FROM payment_resolutions r JOIN payment_resolutions_open o ON o.resolution_id = r.id
  UNION ALL
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);

COMMIT;
