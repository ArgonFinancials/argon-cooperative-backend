-- =============================================================================
-- ARGON — 0028: THE WALLET. VFD ACCOUNTS, AND THE LAST APPROVAL PAYS.
--
-- Written against test/tier1/tier1_0028_test.go (stage 3, 2026-11-10);
-- mutation-tested with test/tier1/mutants_0028 before this file existed.
-- LEDGER_CONTRACT §13 (the last approval pays) and the reviewer's rulings of
-- 2026-11-10 in §12 (each member's own VFD account, opened here; a payout
-- goes into it and no other; the society's account recorded by the operator).
--
-- ACCOUNTS: VFD's ten digits, once, with VFD's client id; one each for a
-- member (in their own society) and a society; never edited.
--
-- THE LAST SIGNATURE CARRIES THE HOLD: a hold may rest on the act of the
-- signature on THIS request, in its transaction. No second human act (§13).
--
-- A TRANSFER is its hold's: the reference derived from the hold (a retry can
-- never be a second payment), the hold's amount, from the society's account
-- to the member's own, on an open hold, one per hold, sent once.
--
-- WHAT VFD SAID is recorded each time it is asked and never edited. An
-- OUTCOME rests on it: paid only on VFD's success; failed only on a
-- definitive answer (refused or never reached at the send, or not found after
-- the settlement window). A timeout is not a failure.
--
-- THE SYSTEM RECORDS, NO USER STANDS IN FOR IT: in the outcome's transaction a
-- failure releases the hold and a success posts the payout, each naming the
-- outcome, with no user. Every 0012 recording rule applies to the system's
-- payout as to a person's. A person cannot release, or record a payout on, a
-- hold whose transfer exists.
--
-- WHO WRITES IT: the wallet (argon_wallet, deploy/sql/grants.sql), the only
-- role that writes VFD's evidence. The database cannot know what VFD said; a
-- role that could write "paid" could debit a member for money that never
-- left, and an account number decides where money goes. The API reads it.
-- =============================================================================

BEGIN;

-- ---- the shape -----------------------------------------------------------------
CREATE TABLE vfd_accounts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id     UUID NOT NULL REFERENCES societies(id),
  member_id      UUID REFERENCES members(id),          -- null: the society's own account
  account_number TEXT NOT NULL,
  vfd_client_id  TEXT NOT NULL,
  vfd_account_id TEXT,
  account_name   TEXT,
  how            TEXT NOT NULL CHECK (how IN ('opened_here', 'recorded_by_operator')),
  recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT vfd_account_number_shape CHECK (account_number ~ '^[0-9]{10}$'),
  CONSTRAINT vfd_account_names_client CHECK (btrim(vfd_client_id) <> '')
);
CREATE UNIQUE INDEX vfd_account_number_once ON vfd_accounts (account_number);
CREATE UNIQUE INDEX vfd_account_one_each ON vfd_accounts (society_id, coalesce(member_id, '00000000-0000-0000-0000-000000000000'::uuid));

CREATE TABLE vfd_transfers (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  hold_id      UUID NOT NULL REFERENCES withdrawal_holds(id),
  reference    TEXT NOT NULL,
  from_account TEXT NOT NULL,
  to_account   TEXT NOT NULL,
  amount_kobo  BIGINT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One per hold. The reference is unique with it: it is derived from the hold
-- (vfd_reference_is_the_holds), so a second index on it would be the same
-- fact stored twice, and no break of either could be seen alone.
CREATE UNIQUE INDEX vfd_transfer_once ON vfd_transfers (hold_id);

CREATE TABLE vfd_transfer_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id UUID NOT NULL REFERENCES vfd_transfers(id),
  kind        TEXT NOT NULL,
  result      TEXT NOT NULL,
  code        TEXT,
  txn_status  TEXT,
  txn_id      TEXT,
  session_id  TEXT,
  amount_kobo BIGINT,
  vfd_time    TEXT,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT vfd_event_result_known CHECK (
    (kind = 'sent' AND result IN ('accepted', 'refused', 'not_reached', 'unknown')) OR
    (kind = 'requeried' AND result IN ('succeeded', 'not_found', 'pending', 'unknown'))),
  -- A success is for an amount: the payout posts what VFD paid.
  CONSTRAINT vfd_success_names_amount CHECK (NOT (kind = 'requeried' AND result = 'succeeded') OR coalesce(amount_kobo, 0) > 0)
);
CREATE INDEX vfd_transfer_events_transfer ON vfd_transfer_events (transfer_id, at);

CREATE TABLE vfd_transfer_outcomes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  transfer_id UUID NOT NULL REFERENCES vfd_transfers(id),
  result      TEXT NOT NULL CHECK (result IN ('paid', 'failed')),
  evidence_id UUID NOT NULL REFERENCES vfd_transfer_events(id),
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX vfd_outcome_once ON vfd_transfer_outcomes (transfer_id);

-- The system's own records: no user stands in for it (§13). A user, or an
-- outcome of VFD's, never both and never neither.
ALTER TABLE ledger_entries ALTER COLUMN actor_user_id DROP NOT NULL;
ALTER TABLE ledger_entries ADD COLUMN vfd_outcome_id UUID REFERENCES vfd_transfer_outcomes(id);
ALTER TABLE ledger_entries ADD CONSTRAINT entry_names_actor_or_vfd CHECK ((actor_user_id IS NULL) = (vfd_outcome_id IS NOT NULL));
CREATE UNIQUE INDEX vfd_payout_once ON ledger_entries (vfd_outcome_id) WHERE vfd_outcome_id IS NOT NULL;
ALTER TABLE withdrawal_hold_releases ALTER COLUMN actor_user_id DROP NOT NULL;
ALTER TABLE withdrawal_hold_releases ADD COLUMN vfd_outcome_id UUID REFERENCES vfd_transfer_outcomes(id);
ALTER TABLE withdrawal_hold_releases ADD CONSTRAINT release_names_actor_or_vfd CHECK ((actor_user_id IS NULL) = (vfd_outcome_id IS NOT NULL));

-- The reference VFD is given: derived from the hold, so the same payout asked
-- twice is the same reference, and VFD refuses or finds it.
CREATE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;

-- "Not found" this long after the send is a failure; sooner, settlement lag.
-- PROVISIONAL, chosen before VFD's real timings are measured (§13's 30-minute
-- figure is when it becomes an exception for a person, not a failure).
CREATE FUNCTION vfd_settlement_window() RETURNS INTERVAL LANGUAGE sql IMMUTABLE AS $$ SELECT interval '2 hours' $$;

-- ---- the rules: never edited ------------------------------------------------------
CREATE FUNCTION vfd_records_append_only() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM auth_refuse('vfd_records_append_only', format('%s is a record of what VFD said or did: never edited, never deleted', TG_TABLE_NAME));
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_accounts_append_only BEFORE UPDATE OR DELETE ON vfd_accounts FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_transfers_append_only BEFORE UPDATE OR DELETE ON vfd_transfers FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_transfer_events_append_only BEFORE UPDATE OR DELETE ON vfd_transfer_events FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER vfd_transfer_outcomes_append_only BEFORE UPDATE OR DELETE ON vfd_transfer_outcomes FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- ---- the rules: accounts -----------------------------------------------------------
CREATE FUNCTION vfd_account_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.member_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND society_id = NEW.society_id) THEN
    PERFORM auth_refuse('vfd_account_members_society', 'a member''s VFD account is recorded under their own society');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_accounts_admissible BEFORE INSERT ON vfd_accounts FOR EACH ROW EXECUTE FUNCTION vfd_account_admissible();

CREATE FUNCTION operator_record_society_vfd_account(p_society UUID, p_number TEXT, p_client TEXT, p_account TEXT, p_name TEXT,
    p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE v UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'recording a society''s VFD account says why'); END IF;
  INSERT INTO vfd_accounts (society_id, account_number, vfd_client_id, vfd_account_id, account_name, how)
    VALUES (p_society, coalesce(p_number, ''), coalesce(p_client, ''), p_account, p_name, 'recorded_by_operator') RETURNING id INTO v;
  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, reason)
    VALUES (p_society, 'vfd_account', NULL, p_number, p_operator, p_login, p_reason);
  RETURN v;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_record_society_vfd_account(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

-- ---- the rules: the transfer is the hold's -------------------------------------------
CREATE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE TRIGGER vfd_transfers_admissible BEFORE INSERT ON vfd_transfers FOR EACH ROW EXECUTE FUNCTION vfd_transfer_admissible();

-- ---- the rules: what VFD said ---------------------------------------------------------
CREATE FUNCTION vfd_event_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM 1 FROM vfd_transfers WHERE id = NEW.transfer_id FOR UPDATE;   -- one answer at a time per transfer
  IF NEW.kind = 'sent' AND EXISTS (SELECT 1 FROM vfd_transfer_events WHERE transfer_id = NEW.transfer_id AND kind = 'sent') THEN
    PERFORM auth_refuse('vfd_sent_once', 'a transfer is sent once: an automatic retry is how a payment goes out twice (§13)');
  END IF;
  IF NEW.kind = 'requeried' AND NOT EXISTS (SELECT 1 FROM vfd_transfer_events WHERE transfer_id = NEW.transfer_id AND kind = 'sent') THEN
    PERFORM auth_refuse('vfd_requery_after_send', 'VFD is asked about a transfer after it was sent');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_transfer_events_admissible BEFORE INSERT ON vfd_transfer_events FOR EACH ROW EXECUTE FUNCTION vfd_event_admissible();

-- ---- the rules: an outcome rests on what VFD said ----------------------------------------
CREATE FUNCTION vfd_outcome_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE TRIGGER vfd_transfer_outcomes_admissible BEFORE INSERT ON vfd_transfer_outcomes FOR EACH ROW EXECUTE FUNCTION vfd_outcome_admissible();

-- The system records, in the outcome's transaction. A failure releases the
-- hold, VFD's answer as the reason; a success posts the payout: what VFD paid
-- out of the bank, the member's savings down by that and the hold's charges,
-- dated the day the money left (the send), named by the outcome and by no
-- user. 0012's rules then judge it as any payout: a different amount, a
-- payout on a released hold, an overdrawn member are posted and flagged.
CREATE FUNCTION vfd_outcome_recorded() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE TRIGGER vfd_transfer_outcomes_recorded AFTER INSERT ON vfd_transfer_outcomes FOR EACH ROW EXECUTE FUNCTION vfd_outcome_recorded();

-- ---- the rules: the system's records are the outcome's; a person's are not the system's --
CREATE FUNCTION vfd_entry_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE TRIGGER ledger_entries_vfd BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION vfd_entry_admissible();

CREATE FUNCTION vfd_release_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.vfd_outcome_id IS NOT NULL THEN
    IF NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o JOIN vfd_transfers t ON t.id = o.transfer_id
                    WHERE o.id = NEW.vfd_outcome_id AND o.result = 'failed' AND t.hold_id = NEW.hold_id) THEN
      PERFORM auth_refuse('release_is_the_outcomes', 'a release naming VFD''s outcome is that outcome''s: failed, on its hold');
    END IF;
  ELSIF EXISTS (SELECT 1 FROM vfd_transfers WHERE hold_id = NEW.hold_id) THEN
    PERFORM auth_refuse('hold_release_while_at_vfd', 'this hold''s transfer is at VFD: it is released only on VFD''s definitive answer, never by a person on a guess (§13)');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER withdrawal_hold_releases_vfd BEFORE INSERT ON withdrawal_hold_releases FOR EACH ROW EXECUTE FUNCTION vfd_release_admissible();

-- ---- the hold: also on the last signature (0018's rule, one branch added) -------------------
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
    -- An authorisation (0012), or the signature that reached quorum on THIS
    -- request, in this transaction (§13: the last approval pays).
    IF a.kind = 'approval_signature' THEN
      IF NOT EXISTS (SELECT 1 FROM signatory_approvals sa WHERE sa.act_id = NEW.act_id AND sa.request_id = NEW.request_id) THEN
        PERFORM auth_refuse('hold_rests_on_this_requests_signature', 'a hold rests on a signature on its own request, not another''s');
      END IF;
    ELSIF a.kind IS DISTINCT FROM 'withdrawal_authorisation' THEN PERFORM auth_refuse('hold_act_kind', format('a hold rests on a withdrawal_authorisation act or the last signature, not %s', a.kind)); END IF;
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

-- ---- settling: the system's payout rests on VFD's outcome, not a step-up (0013's rule) -------
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

COMMIT;
