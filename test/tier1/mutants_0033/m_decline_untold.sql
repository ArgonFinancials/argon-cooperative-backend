-- DELIBERATELY WRONG (m_decline_untold). GENERATED (gen0033.py): a full copy of 0033 with
-- one rule broken, applied over 0033_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
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

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution'));

CREATE TABLE payment_resolutions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id       UUID NOT NULL REFERENCES societies(id),
  decision         TEXT NOT NULL CHECK (decision IN ('credit', 'decline')),
  inflow_id        UUID REFERENCES vfd_inflows(id),
  claim_id         UUID REFERENCES contribution_claims(id),
  member_id        UUID REFERENCES members(id),
  reason           TEXT NOT NULL,
  proposed_by      UUID NOT NULL REFERENCES users(id),
  proposed_act     UUID NOT NULL REFERENCES credential_acts(id),
  client_reference TEXT NOT NULL,
  proposed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT resolution_says_why CHECK (length(btrim(reason)) BETWEEN 10 AND 500)
);
CREATE UNIQUE INDEX resolution_recorded_once ON payment_resolutions (society_id, client_reference);
CREATE TRIGGER payment_resolutions_append_only BEFORE UPDATE OR DELETE ON payment_resolutions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

CREATE TABLE payment_resolution_decisions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  resolution_id UUID NOT NULL REFERENCES payment_resolutions(id),
  confirmed     BOOLEAN NOT NULL,
  decided_by    UUID NOT NULL REFERENCES users(id),
  decided_act   UUID NOT NULL REFERENCES credential_acts(id),
  reason        TEXT,
  decided_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT resolution_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)
);
CREATE UNIQUE INDEX resolution_decided_once ON payment_resolution_decisions (resolution_id);
CREATE TRIGGER payment_resolution_decisions_append_only BEFORE UPDATE OR DELETE ON payment_resolution_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- What is open: a row per undecided proposal, removed by its decision. Written
-- only by this file's triggers. Its two indexes are the one-open rules.
CREATE TABLE payment_resolutions_open (
  resolution_id UUID PRIMARY KEY REFERENCES payment_resolutions(id),
  inflow_id     UUID,
  claim_id      UUID
);
CREATE UNIQUE INDEX resolution_open_once_per_payment ON payment_resolutions_open (inflow_id) WHERE inflow_id IS NOT NULL;
CREATE UNIQUE INDEX resolution_open_once_per_claim ON payment_resolutions_open (claim_id) WHERE claim_id IS NOT NULL;

ALTER TABLE vfd_inflow_postings DROP CONSTRAINT vfd_inflow_postings_how_check;
ALTER TABLE vfd_inflow_postings ADD CONSTRAINT vfd_inflow_postings_how_check CHECK (how IN ('claim', 'own_account', 'officers'));

-- ---- who may take part ------------------------------------------------------------
-- A current officer of the society, not the internal auditor.
CREATE FUNCTION resolution_officer(p_society UUID, p_user UUID) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
                  WHERE a.society_id = p_society AND m.user_id = p_user AND a.post <> 'internal_auditor'
                    AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))
$$;

-- The act: the person's own payment_resolution act, in this transaction.
CREATE FUNCTION resolution_act_ok(p_act UUID, p_user UUID) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM credential_acts WHERE id = p_act AND actor_user_id = p_user AND kind = 'payment_resolution' AND act_xid = pg_current_xact_id())
$$;

-- The person whose money it is: the member credited, or the claimant declined.
CREATE FUNCTION resolution_beneficiary(r payment_resolutions) RETURNS UUID LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE r.decision
           WHEN 'credit' THEN (SELECT user_id FROM members WHERE id = r.member_id)
           ELSE (SELECT user_id FROM contribution_claims WHERE id = r.claim_id)
         END
$$;

-- A claim still open: not credited, not declined by two officers.
CREATE FUNCTION claim_open(p_claim UUID) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM contribution_claim_states WHERE claim_id = p_claim AND state NOT IN ('posted', 'not_credited'))
$$;

-- ---- a proposal --------------------------------------------------------------------
CREATE FUNCTION payment_resolution_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT resolution_act_ok(NEW.proposed_act, NEW.proposed_by) THEN
    PERFORM auth_refuse('resolution_act', 'a proposal rests on the proposer''s own payment_resolution act, in its transaction');
  END IF;
  IF NOT resolution_officer(NEW.society_id, NEW.proposed_by) THEN
    PERFORM auth_refuse('resolution_by_an_officer', 'a payment is resolved by the society''s current officers, not the internal auditor');
  END IF;
  IF NEW.decision = 'credit' THEN
    IF NEW.inflow_id IS NULL OR NEW.member_id IS NULL
       OR NOT EXISTS (SELECT 1 FROM vfd_inflows WHERE id = NEW.inflow_id AND society_id = NEW.society_id)
       OR NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND society_id = NEW.society_id)
       OR (NEW.claim_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM contribution_claims WHERE id = NEW.claim_id AND society_id = NEW.society_id AND member_id = NEW.member_id)) THEN
      PERFORM auth_refuse('resolution_subject', 'a credit names a payment into this society''s account and a member of this society (and, if any, that member''s claim)');
    END IF;
    IF EXISTS (SELECT 1 FROM vfd_inflow_postings WHERE inflow_id = NEW.inflow_id) THEN
      PERFORM auth_refuse('resolution_payment_uncredited', 'this payment is credited already');
    END IF;
  ELSE
    IF NEW.claim_id IS NULL OR NEW.inflow_id IS NOT NULL OR NEW.member_id IS NOT NULL
       OR NOT EXISTS (SELECT 1 FROM contribution_claims WHERE id = NEW.claim_id AND society_id = NEW.society_id) THEN
      PERFORM auth_refuse('resolution_subject', 'a decline names one claim of this society, and nothing else');
    END IF;
    IF NOT claim_open(NEW.claim_id) THEN
      PERFORM auth_refuse('resolution_claim_open', 'this claim is closed: credited, or declined already');
    END IF;
  END IF;
  IF resolution_beneficiary(NEW) = NEW.proposed_by THEN
    PERFORM auth_refuse('resolution_not_beneficiary', 'nobody resolves a payment in their own favour, or their own claim (§10)');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER payment_resolutions_admissible BEFORE INSERT ON payment_resolutions FOR EACH ROW EXECUTE FUNCTION payment_resolution_admissible();

CREATE FUNCTION payment_resolution_opened() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO payment_resolutions_open (resolution_id, inflow_id, claim_id)
    VALUES (NEW.id, NEW.inflow_id, CASE WHEN NEW.decision = 'decline' THEN NEW.claim_id END);
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER payment_resolutions_opened AFTER INSERT ON payment_resolutions FOR EACH ROW EXECUTE FUNCTION payment_resolution_opened();

-- ---- a decision -------------------------------------------------------------------
CREATE FUNCTION payment_resolution_decision_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r payment_resolutions%ROWTYPE;
BEGIN
  SELECT * INTO r FROM payment_resolutions WHERE id = NEW.resolution_id;
  IF NOT resolution_act_ok(NEW.decided_act, NEW.decided_by) THEN
    PERFORM auth_refuse('resolution_act', 'a decision rests on the officer''s own payment_resolution act, in its transaction');
  END IF;
  IF NOT resolution_officer(r.society_id, NEW.decided_by) THEN
    PERFORM auth_refuse('resolution_by_an_officer', 'a payment is resolved by the society''s current officers, not the internal auditor');
  END IF;
  IF resolution_beneficiary(r) = NEW.decided_by THEN
    PERFORM auth_refuse('resolution_not_beneficiary', 'nobody resolves a payment in their own favour, or their own claim (§10)');
  END IF;
  IF NEW.confirmed THEN
    IF NEW.decided_by = r.proposed_by THEN
      PERFORM auth_refuse('resolution_two_officers', 'a second officer confirms: the proposer cannot confirm their own proposal');
    END IF;
    IF r.decision = 'credit' AND EXISTS (SELECT 1 FROM vfd_inflow_postings WHERE inflow_id = r.inflow_id) THEN
      PERFORM auth_refuse('resolution_payment_uncredited', 'this payment was credited since it was proposed');
    END IF;
    IF r.decision = 'decline' AND NOT claim_open(r.claim_id) THEN
      PERFORM auth_refuse('resolution_claim_open', 'this claim was closed since it was proposed: credited, or declined');
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER payment_resolution_decisions_admissible BEFORE INSERT ON payment_resolution_decisions FOR EACH ROW EXECUTE FUNCTION payment_resolution_decision_admissible();

CREATE FUNCTION payment_resolution_decided() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r payment_resolutions%ROWTYPE;
BEGIN
  SELECT * INTO r FROM payment_resolutions WHERE id = NEW.resolution_id;
  DELETE FROM payment_resolutions_open WHERE resolution_id = r.id;
  IF NEW.confirmed AND r.decision = 'credit' THEN
    PERFORM contribution_post_inflow(r.inflow_id, r.member_id, r.claim_id, 'officers');
    IF NOT EXISTS (SELECT 1 FROM vfd_inflow_postings WHERE inflow_id = r.inflow_id) THEN
      PERFORM auth_refuse('resolution_posted', 'the credit could not be posted: the society''s chart lacks an account it needs (bank, member savings, or a charge account)');
    END IF;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER payment_resolution_decisions_decided AFTER INSERT ON payment_resolution_decisions FOR EACH ROW EXECUTE FUNCTION payment_resolution_decided();

-- ---- the posting names who credited it (0032's, the label by how) ----
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
    VALUES (i.society_id, 'contribution_in', d, NULL, NULL, CASE p_how WHEN 'officers' THEN 'Credited by two officers, on VFD''s confirmation' ELSE 'Argon system, on VFD''s confirmation' END, 'vfd-inflow:' || i.vfd_reference, i.id)
    RETURNING id INTO e;
  FOR acct IN
    SELECT a.id, l.kobo, l.member, l.charge_role FROM contribution_lines(i.amount_kobo, p_member, fee, vat, levy, margin) l
      JOIN accounts a ON a.society_id = i.society_id AND a.role = l.role
     WHERE l.kobo <> 0
  LOOP
    INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role) VALUES (e, acct.id, acct.kobo, acct.member, acct.charge_role);
  END LOOP;
END $$ LANGUAGE plpgsql;


-- A claim two officers declined: closed for good (it never matches later).
CREATE FUNCTION claim_declined(p_claim UUID) RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM payment_resolutions r JOIN payment_resolution_decisions d ON d.resolution_id = r.id
                  WHERE r.claim_id = p_claim AND r.decision = 'decline' AND d.confirmed)
$$;

-- ---- what the member is told (0032's view: officers' credits, declines, and why) ----
--   posted        credited to them: matched, from their own account, or by two officers (at VFD's amount)
--   not_credited  declined by two officers (detail: their reason), or the payment credited to another
--                 member by two officers (detail: a fixed sentence naming nobody)
CREATE OR REPLACE VIEW contribution_claim_states AS
  SELECT c.id AS claim_id, c.society_id, c.member_id, c.user_id, c.session_id, c.amount_kobo, c.claimed_at,
         CASE
           WHEN p.member_id = c.member_id AND (i.amount_kobo = c.amount_kobo OR p.how = 'officers') THEN 'posted'
           WHEN p.how = 'officers' AND p.member_id <> c.member_id THEN 'not_credited'
           WHEN p.member_id IS NOT NULL AND p.member_id <> c.member_id THEN 'contested'
           WHEN EXISTS (SELECT 1 FROM contribution_claims o WHERE o.society_id = c.society_id AND o.session_id = c.session_id AND o.member_id <> c.member_id AND NOT claim_declined(o.id)) THEN 'contested'
           WHEN i.id IS NOT NULL AND i.amount_kobo <> c.amount_kobo THEN 'amount_differs'
           ELSE 'waiting'
         END AS state,
         CASE WHEN p.member_id = c.member_id AND (i.amount_kobo = c.amount_kobo OR p.how = 'officers')
              THEN (SELECT e.id FROM ledger_entries e WHERE e.vfd_inflow_id = i.id) END AS entry_id,
         CASE
           WHEN p.member_id = c.member_id AND (i.amount_kobo = c.amount_kobo OR p.how = 'officers') THEN NULL
           WHEN dr.reason IS NOT NULL THEN dr.reason
           WHEN p.how = 'officers' AND p.member_id <> c.member_id THEN 'This payment was credited to another member after two officers checked it.'
         END AS detail
    FROM contribution_claims c
    LEFT JOIN LATERAL (SELECT * FROM vfd_inflows x WHERE x.society_id = c.society_id AND x.session_id = c.session_id
                        ORDER BY x.confirmed_at LIMIT 1) i ON true
    LEFT JOIN vfd_inflow_postings p ON p.inflow_id = i.id
    LEFT JOIN LATERAL (SELECT r.reason FROM payment_resolutions r JOIN payment_resolution_decisions d ON d.resolution_id = r.id
                        WHERE r.claim_id = c.id AND r.decision = 'decline' AND d.confirmed LIMIT 1) dr ON true;

-- ---- a declined claim never matches (0032's match, declined claims left out) ----
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
  SELECT count(DISTINCT member_id) INTO n FROM contribution_claims WHERE society_id = p_society AND session_id = p_session AND NOT claim_declined(id);
  IF n <> 1 THEN
    RETURN;   -- nobody's claim yet, or two members': never a guess
  END IF;
  SELECT * INTO c FROM contribution_claims WHERE society_id = p_society AND session_id = p_session AND NOT claim_declined(id);
  IF c.amount_kobo <> i.amount_kobo THEN
    RETURN;
  END IF;
  PERFORM contribution_post_inflow(i.id, c.member_id, c.id, 'claim');
END $$ LANGUAGE plpgsql;

-- ---- what a person must act on (0032's view, with proposals waiting for a second officer) ----
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

-- Run only by this file's triggers. claim_declined stays callable: the
-- claim-state view calls it, and a function in a view is checked against the
-- view's CALLER (argon_app reading a member's claims). It reads one boolean.
REVOKE EXECUTE ON FUNCTION resolution_officer(UUID, UUID), resolution_act_ok(UUID, UUID), claim_open(UUID) FROM PUBLIC;

COMMIT;
