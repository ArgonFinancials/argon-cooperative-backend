-- DELIBERATELY WRONG (m_any_period). A full copy of 0029 with one rule broken,
-- applied over 0029_interface.sql, whose objects are removed first.
BEGIN;
DROP VIEW wallet_exceptions;
DROP TABLE vfd_reconciliation_runs, vfd_inflows, vfd_notification_attempts, vfd_notifications;
COMMIT;
-- =============================================================================
-- ARGON — 0029: MONEY ARRIVING. VFD's NOTIFICATIONS, KEPT; INFLOWS, CONFIRMED
-- ONCE; RECONCILIATION THAT SAYS WHEN IT COULD NOT RUN; AND WHAT A PERSON MUST
-- ACT ON.
--
-- Written against test/tier1/tier1_0029_test.go (stage 3, 2026-11-10);
-- mutation-tested with test/tier1/mutants_0029 before this file existed.
-- docs/VFD_INTEGRATION.md §2, requirements 2–6; §12 (2026-11-10): a member
-- pays into the society's VFD account and claims it; an inflow nobody claimed
-- waits for an officer, never credited to a guessed member.
--
-- A NOTIFICATION is kept exactly as received, every delivery (a retry is
-- evidence too), never edited: written by the webhook (the app) BEFORE VFD is
-- answered. It is never trusted alone: VFD does not sign inflow notifications
-- (the wallet platform's own finding), so nothing is believed until the
-- wallet confirms the reference with VFD itself.
--
-- AN ATTEMPT to process one is recorded each time, saying why when it did not
-- confirm. A failure part-way leaves a recorded, unconfirmed notification for
-- the next attempt: never a half-recorded inflow.
--
-- AN INFLOW is VFD's confirmation: ONCE PER VFD REFERENCE (a unique index, not
-- a hash of the body), positive, into a society's OWN VFD account, VFD's
-- amount and not the notification's, never edited. Written only by the wallet
-- (argon_wallet). Posting it as a contribution is the member's claim (stage 4).
--
-- RECONCILIATION records every run and what it found; a run that could not
-- run (no statement endpoint is known yet) says so, never "nothing missed".
--
-- wallet_exceptions: everything a person must act on, derived, never stored:
-- a transfer with no answer 30 minutes after sending (PROVISIONAL, §13), a
-- failed one, a hold the last signature placed that has not gone out, a
-- notification not confirmed, an inflow nobody has claimed, reconciliation
-- that could not run.
-- =============================================================================

BEGIN;

CREATE TABLE vfd_notifications (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind           TEXT NOT NULL,
  raw            TEXT NOT NULL,
  signed         BOOLEAN NOT NULL,
  reference      TEXT,
  account_number TEXT,
  CONSTRAINT vfd_notification_kind_known CHECK (kind IN ('inflow', 'bvn_consent', 'unknown'))
);
CREATE INDEX vfd_notifications_reference ON vfd_notifications (reference);

CREATE TABLE vfd_notification_attempts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  notification_id UUID NOT NULL REFERENCES vfd_notifications(id),
  at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  result          TEXT NOT NULL,
  detail          TEXT,
  CONSTRAINT vfd_attempt_result_known CHECK (result IN ('confirmed', 'duplicate', 'not_confirmed', 'unmatched', 'malformed')),
  CONSTRAINT vfd_attempt_says_why CHECK (result IN ('confirmed', 'duplicate') OR btrim(coalesce(detail, '')) <> '')
);
CREATE INDEX vfd_notification_attempts_notification ON vfd_notification_attempts (notification_id, at);

CREATE TABLE vfd_inflows (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  vfd_reference      TEXT NOT NULL,
  society_id         UUID NOT NULL REFERENCES societies(id),
  account_number     TEXT NOT NULL,
  amount_kobo        BIGINT NOT NULL,
  session_id         TEXT,
  originator_name    TEXT,
  originator_account TEXT,
  originator_bank    TEXT,
  narration          TEXT,
  vfd_time           TEXT,
  notification_id    UUID REFERENCES vfd_notifications(id),
  confirmed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT vfd_inflow_positive CHECK (amount_kobo > 0)
);
CREATE UNIQUE INDEX vfd_inflow_once ON vfd_inflows (vfd_reference);

CREATE TABLE vfd_reconciliation_runs (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_number TEXT NOT NULL,
  period_from    TIMESTAMPTZ NOT NULL,
  period_to      TIMESTAMPTZ NOT NULL,
  ran_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  outcome        TEXT NOT NULL CHECK (outcome IN ('could_not_run', 'matched', 'missing_found')),
  detail         TEXT,
  CONSTRAINT vfd_reconciliation_says_what CHECK (btrim(coalesce(detail, '')) <> '')
);

-- ---- never edited (0028's rule) -------------------------------------------------
CREATE TRIGGER vfd_notifications_append_only BEFORE UPDATE OR DELETE ON vfd_notifications FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_notification_attempts_append_only BEFORE UPDATE OR DELETE ON vfd_notification_attempts FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_inflows_append_only BEFORE UPDATE OR DELETE ON vfd_inflows FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_reconciliation_runs_append_only BEFORE UPDATE OR DELETE ON vfd_reconciliation_runs FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- ---- an inflow lands in a society's own account ------------------------------------
CREATE FUNCTION vfd_inflow_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE society_id = NEW.society_id AND member_id IS NULL AND account_number = NEW.account_number) THEN
    PERFORM auth_refuse('vfd_inflow_to_society_account', 'an inflow is money arriving in the society''s own VFD account: this is not it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_inflows_admissible BEFORE INSERT ON vfd_inflows FOR EACH ROW EXECUTE FUNCTION vfd_inflow_admissible();

-- ---- what a person must act on ----------------------------------------------------
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
  UNION ALL
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);

COMMIT;
