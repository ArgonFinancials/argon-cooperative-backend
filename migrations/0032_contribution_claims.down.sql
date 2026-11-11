-- 0032 down: claims, postings and the match removed; 0028's and 0029's
-- functions and view as they were. REFUSES while any contribution posted from
-- an inflow exists: those entries name no user, and 0028's rule allows that
-- only for VFD's payouts. They are permanent ledger rows; back up first.
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
  UNION ALL
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);
DROP VIEW contribution_claim_states;
CREATE OR REPLACE FUNCTION vfd_entry_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  o vfd_transfer_outcomes%ROWTYPE;
  t vfd_transfers%ROWTYPE;
BEGIN
  IF NEW.vfd_outcome_id IS NOT NULL THEN
    SELECT * INTO o FROM vfd_transfer_outcomes WHERE id = NEW.vfd_outcome_id;
    SELECT * INTO t FROM vfd_transfers WHERE id = o.transfer_id;
    IF o.result IS DISTINCT FROM 'paid' OR NEW.kind <> 'withdrawal_out' OR NEW.hold_id IS DISTINCT FROM t.hold_id OR NEW.payout_reference IS DISTINCT FROM t.reference THEN
      PERFORM auth_refuse('vfd_payout_is_the_outcomes', 'an entry naming VFD''s outcome is that outcome''s payout: paid, on its hold, under its reference');
    END IF;
  ELSIF NEW.hold_id IS NOT NULL AND EXISTS (SELECT 1 FROM vfd_transfers WHERE hold_id = NEW.hold_id) THEN
    PERFORM auth_refuse('payout_at_vfd_by_system', 'this hold''s transfer is at VFD: the system records its payout on VFD''s confirmation, not a person');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION ledger_entry_settles() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e        ledger_entries%ROWTYPE;
  a        RECORD;
  q        RECORD;
  h        withdrawal_holds%ROWTYPE;
  base     BIGINT;
  c_fee    BIGINT;
  c_vat    BIGINT;
  c_levy   BIGINT;
  c_margin BIGINT;
  v_quoted BOOLEAN;
  v_member UUID;
  v_after  BIGINT;
  v_held   BIGINT;
  v_floor  BIGINT;
  v_person UUID;
  v_con    TEXT;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.id;
  IF e.kind NOT IN ('contribution_in', 'withdrawal_out') OR e.reverses_id IS NOT NULL OR e.adjusts_id IS NOT NULL THEN
    RETURN NULL;
  END IF;
  -- The step-up: who records it (0011). The system's payout rests on VFD's
  -- outcome instead (0028, vfd_entry_admissible): no person to step up.
  IF e.batch_id IS NULL AND e.vfd_outcome_id IS NULL THEN
    IF e.act_id IS NULL THEN
      PERFORM auth_refuse('entry_needs_step_up', 'a single contribution or payout rests on its poster''s own PIN step-up: it names its act');
    ELSE
      SELECT kind, actor_user_id, act_xid INTO a FROM credential_acts WHERE id = e.act_id;
      IF a.actor_user_id IS DISTINCT FROM e.actor_user_id THEN
        PERFORM auth_refuse('entry_act_is_actors', 'an entry rests on its poster''s own step-up, not someone else''s');
      END IF;
      IF a.kind IS DISTINCT FROM (CASE e.kind WHEN 'contribution_in' THEN 'contribution_record' ELSE 'withdrawal_payout' END) THEN
        PERFORM auth_refuse('entry_act_kind', format('a %s rests on its own kind of act, not %s', e.kind, a.kind));
      END IF;
      IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
        PERFORM auth_refuse('entry_act_same_transaction', 'the act and the entry are one event: the act is written in the entry''s transaction');
      END IF;
    END IF;
  END IF;

  SELECT coalesce(abs(sum(l.amount_kobo)), 0) INTO base
    FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id WHERE l.entry_id = e.id AND ac.role = 'bank';
  SELECT coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'partner_charge'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'vat'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'statutory_levy'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'argon_margin'), 0) INTO c_fee, c_vat, c_levy, c_margin
    FROM ledger_lines WHERE entry_id = e.id;

  -- A contribution: the money arrived. Its charge is the series' where the
  -- series can say; where they cannot, it is recorded uncharged and flagged.
  IF e.kind = 'contribution_in' THEN
    BEGIN
      SELECT * INTO q FROM charge_quote(e.society_id, e.kind, base, e.value_date);
      v_quoted := true;
    EXCEPTION WHEN check_violation THEN
      v_quoted := false;
      GET STACKED DIAGNOSTICS v_con = CONSTRAINT_NAME;
    END;
    IF v_quoted THEN
      IF c_fee <> q.partner_fee_kobo OR c_vat <> q.vat_kobo OR c_levy <> q.statutory_levy_kobo OR c_margin <> q.argon_margin_kobo THEN
        PERFORM auth_refuse('entry_charge_is_the_schedules', format('by the series this %s of %s kobo is charged %s fee, %s VAT, %s levy and %s margin; the entry carries %s, %s, %s and %s', e.kind, base, q.partner_fee_kobo, q.vat_kobo, q.statutory_levy_kobo, q.argon_margin_kobo, c_fee, c_vat, c_levy, c_margin));
      END IF;
    ELSE
      IF c_fee <> 0 OR c_vat <> 0 OR c_levy <> 0 OR c_margin <> 0 THEN
        PERFORM auth_refuse('entry_charge_is_the_schedules', 'this society''s series cannot determine the charge, so the entry carries none');
      END IF;
      INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, NULL, 'charge_undetermined', format('the charge on %s kobo from the society''s series, whose %s is not set', base, CASE v_con WHEN 'charge_partner_fee_unset' THEN 'partner_fee' WHEN 'charge_vat_unset' THEN 'vat_on_argon_fee' WHEN 'charge_levy_unset' THEN 'statutory_levy' WHEN 'charge_margin_unset' THEN 'argon_margin' WHEN 'charge_band_missing' THEN 'band covering this amount' ELSE coalesce(v_con, 'series') END), 'recorded uncharged: the charge is UNDETERMINED, not zero');
    END IF;
    RETURN NULL;
  END IF;

  -- A payout: the money left. Record it; flag every difference from what was
  -- authorised. Refuse only a malformed record.
  IF e.hold_id IS NULL THEN
    INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, NULL, 'payout_without_hold', 'a payout authorised to execute (a hold) before the money left', format('%s kobo left with no hold behind it', base));
  ELSE
    SELECT * INTO h FROM withdrawal_holds WHERE id = e.hold_id;
    IF EXISTS (SELECT 1 FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
                  WHERE l.entry_id = e.id AND ac.role = 'member_savings' AND l.member_id IS DISTINCT FROM h.member_id) THEN
        PERFORM auth_refuse('payout_from_hold_member', format('a held payout comes out of the hold''s member''s savings (%s)', h.member_id));
      END IF;
    IF c_fee <> h.partner_fee_kobo OR c_vat <> h.vat_kobo OR c_levy <> h.statutory_levy_kobo OR c_margin <> h.argon_margin_kobo THEN
        PERFORM auth_refuse('entry_charge_is_the_holds', format('the charges were fixed when the payout was authorised: %s, %s, %s, %s; the entry carries %s, %s, %s, %s', h.partner_fee_kobo, h.vat_kobo, h.statutory_levy_kobo, h.argon_margin_kobo, c_fee, c_vat, c_levy, c_margin));
      END IF;
    IF base <> h.amount_kobo THEN
      INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, h.id, 'payout_amount_differs', format('%s kobo, as held', h.amount_kobo), format('%s kobo paid', base));
    END IF;
    IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) THEN
      INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, h.id, 'payout_on_released_hold', format('no payout on hold %s: it was released', h.id), format('%s kobo paid on it after the release', base));
    END IF;
    IF EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id AND id <> e.id) THEN
      INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, h.id, 'hold_paid_twice', format('one payout on hold %s', h.id), format('%s recorded payouts on it, this one %s kobo', (SELECT count(*) FROM ledger_entries WHERE hold_id = h.id), base));
    END IF;
  END IF;
  SELECT l.member_id INTO v_member FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
   WHERE l.entry_id = e.id AND ac.role = 'member_savings' LIMIT 1;
  IF v_member IS NOT NULL THEN
    SELECT coalesce(-sum(l.amount_kobo), 0) INTO v_after FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
     WHERE ac.role = 'member_savings' AND l.member_id = v_member;
    SELECT user_id INTO v_person FROM members WHERE id = v_member;
    SELECT coalesce(sum(greatest(loan_owed(g.loan_id), 0)), 0) INTO v_held FROM loan_guarantees g
     WHERE (g.guarantor_member_id = v_member OR g.guarantor_user_id = v_person)
       AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id);
    SELECT coalesce(account_floor_kobo, 0) INTO v_floor FROM societies WHERE id = e.society_id;
    IF v_after - v_held - v_floor < 0 THEN
      INSERT INTO ledger_discrepancies (entry_id, hold_id, kind, expected, observed) VALUES (e.id, e.hold_id, 'payout_overdraws', format('at least %s kobo left in savings (held for guarantees %s, floor %s)', v_held + v_floor, v_held, v_floor), format('%s kobo in savings after this payout', v_after));
    END IF;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
DROP FUNCTION contribution_rematch();
DROP TRIGGER vfd_inflows_recorded ON vfd_inflows;
DROP FUNCTION vfd_inflow_recorded();
DROP TRIGGER contribution_claims_recorded ON contribution_claims;
DROP FUNCTION contribution_claim_recorded();
DROP FUNCTION contribution_match(UUID, TEXT);
DROP FUNCTION contribution_post_inflow(UUID, UUID, UUID, TEXT);
DROP FUNCTION contribution_lines(BIGINT, UUID, BIGINT, BIGINT, BIGINT, BIGINT);
DROP FUNCTION claim_wait_window();
DROP INDEX contribution_from_inflow_once;
ALTER TABLE ledger_entries DROP CONSTRAINT entry_names_actor_or_vfd;
ALTER TABLE ledger_entries DROP COLUMN vfd_inflow_id;
ALTER TABLE ledger_entries ADD CONSTRAINT entry_names_actor_or_vfd CHECK ((actor_user_id IS NULL) = (vfd_outcome_id IS NOT NULL));
DROP TABLE vfd_inflow_postings;
DROP TABLE contribution_claims;
DROP FUNCTION contribution_claim_admissible();
COMMIT;
