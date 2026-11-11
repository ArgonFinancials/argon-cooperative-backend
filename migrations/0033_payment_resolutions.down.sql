-- 0033 down: the officers' resolutions removed; 0032's posting, match and
-- views as they were. REFUSES while any payment_resolution act or any credit
-- two officers confirmed exists (the act kinds and the posting's "how" lose
-- them). Grants on the views are re-applied by grants.sql. Back up first.
BEGIN;
DROP VIEW wallet_exceptions;
DROP VIEW contribution_claim_states;
CREATE VIEW contribution_claim_states AS
  SELECT c.id AS claim_id, c.society_id, c.member_id, c.user_id, c.session_id, c.amount_kobo, c.claimed_at,
         CASE
           WHEN p.member_id = c.member_id AND i.amount_kobo = c.amount_kobo THEN 'posted'
           WHEN p.member_id IS NOT NULL AND p.member_id <> c.member_id THEN 'contested'
           WHEN EXISTS (SELECT 1 FROM contribution_claims o WHERE o.society_id = c.society_id AND o.session_id = c.session_id AND o.member_id <> c.member_id) THEN 'contested'
           WHEN i.id IS NOT NULL AND i.amount_kobo <> c.amount_kobo THEN 'amount_differs'
           ELSE 'waiting'
         END AS state,
         CASE WHEN p.member_id = c.member_id AND i.amount_kobo = c.amount_kobo
              THEN (SELECT e.id FROM ledger_entries e WHERE e.vfd_inflow_id = i.id) END AS entry_id
    FROM contribution_claims c
    LEFT JOIN LATERAL (SELECT * FROM vfd_inflows x WHERE x.society_id = c.society_id AND x.session_id = c.session_id
                        ORDER BY x.confirmed_at LIMIT 1) i ON true
    LEFT JOIN vfd_inflow_postings p ON p.inflow_id = i.id;
CREATE VIEW wallet_exceptions AS
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
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);
CREATE OR REPLACE FUNCTION contribution_match(p_society UUID, p_session TEXT) RETURNS VOID
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  i       vfd_inflows%ROWTYPE;
  c       contribution_claims%ROWTYPE;
  n       INT;
BEGIN
  IF p_session IS NULL THEN
    RETURN;
  END IF;
  SELECT count(*) INTO n FROM vfd_inflows WHERE society_id = p_society AND session_id = p_session;
  IF n <> 1 THEN
    RETURN;   -- no payment yet, or two with one session: a person's to decide
  END IF;
  SELECT * INTO i FROM vfd_inflows WHERE society_id = p_society AND session_id = p_session;
  -- One match at a time per payment: a second claim waits here, then sees the first's posting.
  PERFORM 1 FROM vfd_inflows WHERE id = i.id FOR UPDATE;
  IF EXISTS (SELECT 1 FROM vfd_inflow_postings WHERE inflow_id = i.id) THEN
    RETURN;
  END IF;
  SELECT count(DISTINCT member_id) INTO n FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;
  IF n <> 1 THEN
    RETURN;   -- nobody's claim yet, or two members': never a guess
  END IF;
  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session;
  IF c.amount_kobo <> i.amount_kobo THEN
    RETURN;
  END IF;
  PERFORM contribution_post_inflow(i.id, c.member_id, c.id, 'claim');
END $$ LANGUAGE plpgsql;
CREATE OR REPLACE FUNCTION contribution_post_inflow(p_inflow UUID, p_member UUID, p_claim UUID, p_how TEXT) RETURNS VOID
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  i      vfd_inflows%ROWTYPE;
  d      DATE;
  fee    BIGINT := 0;
  vat    BIGINT := 0;
  levy   BIGINT := 0;
  margin BIGINT := 0;
  e      UUID;
  want   INT;
  have   INT;
  acct   RECORD;
BEGIN
  SELECT * INTO i FROM vfd_inflows WHERE id = p_inflow;
  d := (i.confirmed_at AT TIME ZONE 'Africa/Lagos')::date;
  BEGIN
    SELECT q.partner_fee_kobo, q.vat_kobo, q.statutory_levy_kobo, q.argon_margin_kobo INTO fee, vat, levy, margin
      FROM charge_quote(i.society_id, 'contribution_in', i.amount_kobo, d) q;
  EXCEPTION WHEN check_violation THEN
    fee := 0; vat := 0; levy := 0; margin := 0;   -- undetermined: posted uncharged, and flagged when it settles
  END;
  -- What the entry needs, and the society's one account for each role.
  SELECT count(*) FILTER (WHERE l.kobo <> 0), count(a.id) FILTER (WHERE l.kobo <> 0) INTO want, have
    FROM contribution_lines(i.amount_kobo, p_member, fee, vat, levy, margin) l
    LEFT JOIN accounts a ON a.society_id = i.society_id AND a.role = l.role;
  IF have <> want THEN
    RETURN;
  END IF;
  INSERT INTO vfd_inflow_postings (inflow_id, member_id, claim_id, how) VALUES (i.id, p_member, p_claim, p_how);
  INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, client_reference, vfd_inflow_id)
    VALUES (i.society_id, 'contribution_in', d, NULL, NULL, 'Argon system, on VFD''s confirmation', 'vfd-inflow:' || i.vfd_reference, i.id)
    RETURNING id INTO e;
  FOR acct IN
    SELECT a.id, l.kobo, l.member, l.charge_role FROM contribution_lines(i.amount_kobo, p_member, fee, vat, levy, margin) l
      JOIN accounts a ON a.society_id = i.society_id AND a.role = l.role
     WHERE l.kobo <> 0
  LOOP
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role) VALUES (e, acct.id, acct.kobo, acct.member, acct.charge_role);
  END LOOP;
END $$ LANGUAGE plpgsql;
DROP TRIGGER payment_resolution_decisions_decided ON payment_resolution_decisions;
DROP FUNCTION payment_resolution_decided();
DROP TRIGGER payment_resolution_decisions_admissible ON payment_resolution_decisions;
DROP FUNCTION payment_resolution_decision_admissible();
DROP TRIGGER payment_resolutions_opened ON payment_resolutions;
DROP FUNCTION payment_resolution_opened();
DROP TRIGGER payment_resolutions_admissible ON payment_resolutions;
DROP FUNCTION payment_resolution_admissible();
DROP FUNCTION resolution_beneficiary(payment_resolutions);
DROP TABLE payment_resolutions_open;
DROP TABLE payment_resolution_decisions;
DROP TABLE payment_resolutions;
DROP FUNCTION claim_declined(UUID);
DROP FUNCTION claim_open(UUID);
DROP FUNCTION resolution_act_ok(UUID, UUID);
DROP FUNCTION resolution_officer(UUID, UUID);
ALTER TABLE vfd_inflow_postings DROP CONSTRAINT vfd_inflow_postings_how_check;
ALTER TABLE vfd_inflow_postings ADD CONSTRAINT vfd_inflow_postings_how_check CHECK (how IN ('claim', 'own_account'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register'));
COMMIT;
