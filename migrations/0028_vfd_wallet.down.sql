-- 0028 down: the wallet's records and rules removed; the hold and settling
-- rules back to 0018's and 0013's. Refused while the system has recorded
-- anything (an entry or a release with no user cannot be made NOT NULL):
-- those are records of money that moved, and are not dropped by a rollback.
BEGIN;
DROP TRIGGER withdrawal_hold_releases_vfd ON withdrawal_hold_releases;
DROP FUNCTION vfd_release_admissible();
DROP TRIGGER ledger_entries_vfd ON ledger_entries;
DROP FUNCTION vfd_entry_admissible();
ALTER TABLE withdrawal_hold_releases DROP CONSTRAINT release_names_actor_or_vfd;
ALTER TABLE withdrawal_hold_releases DROP COLUMN vfd_outcome_id;
ALTER TABLE withdrawal_hold_releases ALTER COLUMN actor_user_id SET NOT NULL;
DROP INDEX vfd_payout_once;
ALTER TABLE ledger_entries DROP CONSTRAINT entry_names_actor_or_vfd;
ALTER TABLE ledger_entries DROP COLUMN vfd_outcome_id;
ALTER TABLE ledger_entries ALTER COLUMN actor_user_id SET NOT NULL;
DROP TABLE vfd_transfer_outcomes;
DROP TABLE vfd_transfer_events;
DROP TABLE vfd_transfers;
DROP TABLE vfd_accounts;
DROP FUNCTION vfd_outcome_recorded();
DROP FUNCTION vfd_outcome_admissible();
DROP FUNCTION vfd_event_admissible();
DROP FUNCTION vfd_transfer_admissible();
DROP FUNCTION vfd_account_admissible();
DROP FUNCTION vfd_records_append_only();
DROP FUNCTION operator_record_society_vfd_account(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT);
DROP FUNCTION vfd_settlement_window();
DROP FUNCTION vfd_transfer_reference(UUID);
DELETE FROM society_settings_log WHERE setting = 'vfd_account';

CREATE OR REPLACE FUNCTION withdrawal_hold_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r        approval_requests%ROWTYPE;
  a        RECORD;
  q        RECORD;
  p        RECORD;
BEGIN
  IF NEW.act_id IS NULL THEN
    PERFORM auth_refuse('hold_needs_step_up', 'a payout is authorised on the authoriser''s own PIN step-up: the hold names its act');
  ELSE
    SELECT kind, actor_user_id, act_xid INTO a FROM credential_acts WHERE id = NEW.act_id;
    IF a.actor_user_id IS DISTINCT FROM NEW.actor_user_id THEN PERFORM auth_refuse('hold_act_is_holders', 'a hold rests on its authoriser''s own step-up, not someone else''s'); END IF;
    IF a.kind IS DISTINCT FROM 'withdrawal_authorisation' THEN PERFORM auth_refuse('hold_act_kind', format('a hold rests on a withdrawal_authorisation act, not %s', a.kind)); END IF;
    IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN PERFORM auth_refuse('hold_act_same_transaction', 'the act and the hold are one event: the act is written in the hold''s transaction'); END IF;
  END IF;
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id FOR UPDATE;   -- one authorisation at a time per request
  IF r.kind IS DISTINCT FROM 'withdrawal_out' OR approval_status(r.id) <> 'approved' THEN
    PERFORM auth_refuse('hold_request_approved', format('request %s is %s, not an approved withdrawal', r.id, approval_status(r.id)));
  END IF;
  IF NEW.amount_kobo <> r.amount_kobo OR NEW.member_id IS DISTINCT FROM r.member_id OR NEW.society_id <> r.society_id THEN
    PERFORM auth_refuse('hold_matches_request', format('a hold is for its request: %s kobo for member %s, not %s for %s', r.amount_kobo, r.member_id, NEW.amount_kobo, NEW.member_id));
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_holds h WHERE h.request_id = r.id AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)) THEN
    PERFORM auth_refuse('hold_once_open', 'this request already has an open hold: release it before authorising the payout again');
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  NEW.partner_fee_kobo := q.partner_fee_kobo;
  NEW.vat_kobo := q.vat_kobo;
  NEW.statutory_levy_kobo := q.statutory_levy_kobo;
  NEW.argon_margin_kobo := q.argon_margin_kobo;
  PERFORM 1 FROM members WHERE id = NEW.member_id FOR UPDATE;   -- one authorisation at a time per member
  SELECT * INTO p FROM member_position(NEW.member_id);              -- the one arithmetic (0018)
  IF p.floor_kobo IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  IF NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo > p.withdrawable_kobo THEN
    PERFORM auth_refuse('withdrawal_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo, p.withdrawable_kobo, p.savings_kobo, p.held_kobo, p.floor_kobo, p.open_holds_kobo));
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
  -- The step-up: who records it (0011, unchanged).
  IF e.batch_id IS NULL THEN
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

COMMIT;
