-- Reverses 0018: the two triggers get back their own inline arithmetic, exactly
-- as 0013 and 0016 wrote it, and the functions go. Nothing is stored by 0018;
-- dropping it erases nothing, but the API's read uses member_position, so a
-- server built for 0018 fails its reads after this.
BEGIN;
CREATE OR REPLACE FUNCTION withdrawal_hold_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r        approval_requests%ROWTYPE;
  a        RECORD;
  q        RECORD;
  v_floor  BIGINT;
  v_before BIGINT;
  v_held   BIGINT;
  v_open   BIGINT;
  v_person UUID;
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
  SELECT account_floor_kobo INTO v_floor FROM societies WHERE id = NEW.society_id;
  IF v_floor IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;

  PERFORM 1 FROM members WHERE id = NEW.member_id FOR UPDATE;   -- one authorisation at a time per member
  SELECT coalesce(-sum(l.amount_kobo), 0) INTO v_before
    FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
   WHERE ac.role = 'member_savings' AND l.member_id = NEW.member_id;
  SELECT user_id INTO v_person FROM members WHERE id = NEW.member_id;
  SELECT coalesce(sum(greatest(loan_owed(g.loan_id), 0)), 0) INTO v_held
    FROM loan_guarantees g JOIN loans lo ON lo.id = g.loan_id
   WHERE (g.guarantor_member_id = NEW.member_id OR g.guarantor_user_id = v_person) AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id);
  SELECT coalesce(sum(h.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo), 0) INTO v_open
    FROM withdrawal_holds h
   WHERE h.member_id = NEW.member_id AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM ledger_entries le WHERE le.hold_id = h.id);
  IF NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo > v_before - v_held - v_floor - v_open THEN
    PERFORM auth_refuse('withdrawal_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo, v_before - v_held - v_floor - v_open, v_before, v_held, v_floor, v_open));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION withdrawal_request_within_withdrawable() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q        RECORD;
  v_floor  BIGINT;
  v_before BIGINT;
  v_held   BIGINT;
  v_open   BIGINT;
  v_person UUID;
  v_out    BIGINT;
BEGIN
  IF NEW.kind <> 'withdrawal_out' OR NEW.member_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  SELECT account_floor_kobo INTO v_floor FROM societies WHERE id = NEW.society_id;
  IF v_floor IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  SELECT coalesce(-sum(l.amount_kobo), 0) INTO v_before FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
   WHERE ac.role = 'member_savings' AND l.member_id = NEW.member_id;
  SELECT user_id INTO v_person FROM members WHERE id = NEW.member_id;
  SELECT coalesce(sum(greatest(loan_owed(g.loan_id), 0)), 0) INTO v_held FROM loan_guarantees g
   WHERE (g.guarantor_member_id = NEW.member_id OR g.guarantor_user_id = v_person)
     AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id);
  SELECT coalesce(sum(h.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo), 0) INTO v_open
    FROM withdrawal_holds h
   WHERE h.member_id = NEW.member_id AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
     AND NOT EXISTS (SELECT 1 FROM ledger_entries le WHERE le.hold_id = h.id);
  v_out := NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo;
  IF v_out > v_before - v_held - v_floor - v_open THEN
    PERFORM auth_refuse('withdrawal_request_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', v_out, v_before - v_held - v_floor - v_open, v_before, v_held, v_floor, v_open));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP FUNCTION member_position(UUID);
DROP FUNCTION member_guarantees_held(UUID);
COMMIT;
