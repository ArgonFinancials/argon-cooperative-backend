-- DELIBERATELY WRONG. The figure goes below zero.
--
-- Must turn red: TestPosition05_NeverBelowZero.
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
  SELECT s.v, h.v, f.v, o.v, CASE WHEN f.v IS NULL THEN NULL ELSE s.v - h.v - f.v - o.v END FROM s, h, f, o
$$;
COMMIT;
