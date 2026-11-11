-- DELIBERATELY WRONG (c_every_claim_seen). GENERATED (gen0032.py): a full copy of 0032 with
-- one rule broken, applied over 0032_interface.sql, whose objects are removed first.
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

BEGIN;

CREATE TABLE contribution_claims (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id  UUID NOT NULL REFERENCES societies(id),
  member_id   UUID NOT NULL REFERENCES members(id),
  user_id     UUID NOT NULL REFERENCES users(id),
  session_id  TEXT NOT NULL,
  amount_kobo BIGINT NOT NULL,
  claimed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- PROVISIONAL: NIP session ids are 30 digits; VFD's own transfers may differ.
  CONSTRAINT claim_session_shape CHECK (session_id ~ '^[0-9A-Za-z]{10,64}$'),
  CONSTRAINT claim_positive CHECK (amount_kobo > 0)
);
CREATE UNIQUE INDEX claim_once_per_member ON contribution_claims (member_id, session_id);
CREATE INDEX contribution_claims_session ON contribution_claims (society_id, session_id);
CREATE TRIGGER contribution_claims_append_only BEFORE UPDATE OR DELETE ON contribution_claims FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- Written only by contribution_post_inflow (no role is granted it).
CREATE TABLE vfd_inflow_postings (
  id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  inflow_id UUID NOT NULL REFERENCES vfd_inflows(id),
  member_id UUID NOT NULL REFERENCES members(id),
  claim_id  UUID REFERENCES contribution_claims(id),
  how       TEXT NOT NULL CHECK (how IN ('claim', 'own_account')),
  posted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vfd_inflow_posted_once ON vfd_inflow_postings (inflow_id);
CREATE TRIGGER vfd_inflow_postings_append_only BEFORE UPDATE OR DELETE ON vfd_inflow_postings FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

ALTER TABLE ledger_entries ADD COLUMN vfd_inflow_id UUID REFERENCES vfd_inflows(id);
ALTER TABLE ledger_entries DROP CONSTRAINT entry_names_actor_or_vfd;
ALTER TABLE ledger_entries ADD CONSTRAINT entry_names_actor_or_vfd
  CHECK ((actor_user_id IS NULL) = (vfd_outcome_id IS NOT NULL OR vfd_inflow_id IS NOT NULL));
CREATE UNIQUE INDEX contribution_from_inflow_once ON ledger_entries (vfd_inflow_id) WHERE vfd_inflow_id IS NOT NULL;

-- PROVISIONAL, like 0028's windows: how long a claim waits for VFD before a
-- person is told.
CREATE FUNCTION claim_wait_window() RETURNS INTERVAL LANGUAGE sql IMMUTABLE AS $$ SELECT interval '1 day' $$;

-- ---- a claim is the member's own ---------------------------------------------------
CREATE FUNCTION contribution_claim_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND society_id = NEW.society_id AND user_id = NEW.user_id) THEN
    PERFORM auth_refuse('claim_is_the_members_own', 'a contribution is claimed by the member it belongs to, in their own society');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER contribution_claims_admissible BEFORE INSERT ON contribution_claims FOR EACH ROW EXECUTE FUNCTION contribution_claim_admissible();

-- ---- the posting --------------------------------------------------------------------
-- The contribution's lines: the bank up by what arrived, the member's savings
-- up by that less the charge, each charge component on its own account.
CREATE FUNCTION contribution_lines(p_amount BIGINT, p_member UUID, p_fee BIGINT, p_vat BIGINT, p_levy BIGINT, p_margin BIGINT)
RETURNS TABLE (role TEXT, kobo BIGINT, member UUID, charge_role TEXT) LANGUAGE sql IMMUTABLE AS $$
  VALUES ('bank', p_amount, NULL::uuid, 'principal'),
         ('member_savings', -(p_amount - p_fee - p_vat - p_levy - p_margin), p_member, 'principal'),
         ('charges_partner', -p_fee, NULL, 'partner_charge'),
         ('charges_vat', -p_vat, NULL, 'vat'),
         ('statutory_levy_payable', -p_levy, NULL, 'statutory_levy'),
         ('charges_margin', -p_margin, NULL, 'argon_margin')
$$;

-- The inflow credited to p_member: the posting record, then the contribution.
-- A chart without the accounts it needs posts nothing (the inflow then waits,
-- seen), rather than refusing VFD's confirmation being recorded at all.
CREATE FUNCTION contribution_post_inflow(p_inflow UUID, p_member UUID, p_claim UUID, p_how TEXT) RETURNS VOID
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

-- ---- the match ------------------------------------------------------------------------
CREATE FUNCTION contribution_match(p_society UUID, p_session TEXT) RETURNS VOID
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

CREATE FUNCTION contribution_claim_recorded() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM contribution_match(NEW.society_id, NEW.session_id);
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER contribution_claims_recorded AFTER INSERT ON contribution_claims FOR EACH ROW EXECUTE FUNCTION contribution_claim_recorded();

CREATE FUNCTION vfd_inflow_recorded() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  m UUID;
BEGIN
  SELECT member_id INTO m FROM vfd_accounts
   WHERE account_number = NEW.originator_account AND society_id = NEW.society_id AND member_id IS NOT NULL;
  IF m IS NOT NULL THEN
    PERFORM contribution_post_inflow(NEW.id, m, NULL, 'own_account');
    RETURN NULL;
  END IF;
  PERFORM contribution_match(NEW.society_id, NEW.session_id);
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_inflows_recorded AFTER INSERT ON vfd_inflows FOR EACH ROW EXECUTE FUNCTION vfd_inflow_recorded();

-- What a race missed: every unposted inflow with a claim on its session.
CREATE FUNCTION contribution_rematch() RETURNS INT SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r      RECORD;
  posted INT := 0;
BEGIN
  FOR r IN
    SELECT DISTINCT i.id, i.society_id, i.session_id FROM vfd_inflows i
      JOIN contribution_claims c ON c.society_id = i.society_id AND c.session_id = i.session_id
     WHERE NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)
  LOOP
    PERFORM contribution_match(r.society_id, r.session_id);
    IF EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = r.id) THEN
      posted := posted + 1;
    END IF;
  END LOOP;
  RETURN posted;
END $$ LANGUAGE plpgsql;

-- ---- only the match posts from an inflow -----------------------------------------------
CREATE OR REPLACE FUNCTION vfd_entry_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  o vfd_transfer_outcomes%ROWTYPE;
  t vfd_transfers%ROWTYPE;
BEGIN
  IF NEW.vfd_inflow_id IS NOT NULL THEN
    IF NEW.kind <> 'contribution_in' OR NEW.vfd_outcome_id IS NOT NULL
       OR NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = NEW.vfd_inflow_id) THEN
      PERFORM auth_refuse('contribution_from_inflow_is_the_matchs', 'a contribution naming VFD''s inflow is posted by its match (a claim VFD confirmed, or the member''s own account), and by nothing else');
    END IF;
    RETURN NEW;
  END IF;
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

-- ---- the system's contribution needs no person's step-up (0028's rule, one condition added) --
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
  -- outcome instead (0028, vfd_entry_admissible), and the system's
  -- contribution on VFD's inflow and its match (0032): no person to step up.
  IF e.batch_id IS NULL AND e.vfd_outcome_id IS NULL AND e.vfd_inflow_id IS NULL THEN
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

-- ---- what the member is told about each claim ---------------------------------------
--   posted          credited to them (from this claim, or from their own account)
--   contested       another member claims the same payment, or it was credited to another
--   amount_differs  VFD confirmed this session with a different amount
--   waiting         VFD has confirmed no such payment yet
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

-- ---- what a person must act on (0029's view: posted inflows leave it; claims join it) ----
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
    FROM contribution_claim_states s
  UNION ALL
  -- The latest reconciliation of an account that could not run.
  SELECT 'reconciliation_not_run', (SELECT a.society_id FROM vfd_accounts a WHERE a.account_number = r.account_number AND a.member_id IS NULL),
         r.id, r.ran_at, r.detail
    FROM vfd_reconciliation_runs r
   WHERE r.outcome = 'could_not_run'
     AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r2 WHERE r2.account_number = r.account_number AND r2.ran_at > r.ran_at);

-- The posting and the match are the database's own, run by its triggers. A
-- role that could call them could credit any inflow to any member (P72, P76).
REVOKE EXECUTE ON FUNCTION contribution_post_inflow(UUID, UUID, UUID, TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION contribution_match(UUID, TEXT) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION contribution_rematch() FROM PUBLIC;

COMMIT;
