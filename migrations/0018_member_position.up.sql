-- =============================================================================
-- ARGON — 0018: ONE ARITHMETIC FOR WHAT A MEMBER MAY WITHDRAW
--
-- Written against test/tier1/tier1_0018_test.go (decided 2026-10-28, balance
-- handlers piece 1; mutation-tested with test/tier1/mutants_0018 before this
-- file existed). LEDGER_CONTRACT.md §12.
--
-- "Savings, less what guarantees hold, less the floor, less payouts authorised
-- and not yet paid" was written twice inline: the raise check (0016) and the
-- hold check (0012, as 0013 left it). The two had already drifted (the hold's
-- joined loans, the raise's did not; the same result only because every
-- guarantee has its loan). The read of a member's position would have been a
-- third copy. Now:
--
--   member_guarantees_held(member): one row per guarantee holding money: its
--     loan, the borrower, the amount still owed. A repaid loan has no row.
--   member_position(member): the PARTS (savings, held, floor, open holds) and
--     the figure: NULL when the society has set no floor, never below zero.
--
-- Both triggers call it, and so does the API: the figure a member is SHOWN and
-- the figure they are REFUSED BY cannot differ. The triggers keep their
-- refusals and messages; 0012's and 0016's suites, run on every migration,
-- guard the rewrite.
-- =============================================================================
BEGIN;
CREATE OR REPLACE FUNCTION member_guarantees_held(p_member UUID)
RETURNS TABLE (loan_id UUID, borrower_member_id UUID, borrower_name TEXT, owed_kobo BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT g.loan_id, lo.member_id, coalesce(u.full_name, bm.full_name), greatest(loan_owed(g.loan_id), 0)
    FROM loan_guarantees g
    JOIN loans lo ON lo.id = g.loan_id
    JOIN members bm ON bm.id = lo.member_id
    LEFT JOIN users u ON u.id = bm.user_id
   WHERE (g.guarantor_member_id = p_member OR g.guarantor_user_id = (SELECT user_id FROM members WHERE id = p_member))
     AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id)
     AND loan_owed(g.loan_id) > 0
   ORDER BY g.loan_id
$$;

CREATE OR REPLACE FUNCTION member_position(p_member UUID)
RETURNS TABLE (savings_kobo BIGINT, held_kobo BIGINT, floor_kobo BIGINT, open_holds_kobo BIGINT, withdrawable_kobo BIGINT)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH s AS (SELECT coalesce(-sum(l.amount_kobo), 0)::bigint AS v FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
              WHERE ac.role = 'member_savings' AND l.member_id = p_member),
       h AS (SELECT (SELECT coalesce(sum(owed_kobo), 0) FROM member_guarantees_held(p_member))::bigint AS v),
       f AS (SELECT so.account_floor_kobo AS v FROM members m JOIN societies so ON so.id = m.society_id WHERE m.id = p_member),
       o AS (SELECT coalesce(sum(h.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo), 0)::bigint AS v
               FROM withdrawal_holds h
              WHERE h.member_id = p_member
                AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
         AND NOT EXISTS (SELECT 1 FROM ledger_entries le WHERE le.hold_id = h.id))
  SELECT s.v, h.v, f.v, o.v, CASE WHEN f.v IS NULL THEN NULL ELSE greatest(s.v - h.v - f.v - o.v, 0) END FROM s, h, f, o
$$;

-- ---- the hold (0012, as 0013 left it): the arithmetic is member_position ------
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

-- ---- the raise (0016): the same ---------------------------------------------
CREATE OR REPLACE FUNCTION withdrawal_request_within_withdrawable() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q        RECORD;
  p        RECORD;
  v_out    BIGINT;
BEGIN
  IF NEW.kind <> 'withdrawal_out' OR NEW.member_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  SELECT * INTO p FROM member_position(NEW.member_id);              -- the one arithmetic (0018)
  IF p.floor_kobo IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  v_out := NEW.amount_kobo + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo;
  IF v_out > p.withdrawable_kobo THEN
    PERFORM auth_refuse('withdrawal_request_within_withdrawable', format('%s kobo out (the amount and its charges) against %s withdrawable: savings %s, held for guarantees %s, floor %s, already authorised %s', v_out, p.withdrawable_kobo, p.savings_kobo, p.held_kobo, p.floor_kobo, p.open_holds_kobo));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
COMMIT;
