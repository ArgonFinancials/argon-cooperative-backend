-- =============================================================================
-- ARGON — 0013: VAT IS CHARGED ON ARGON'S OWN FEE, AND THE NAMES SAY SO
--
-- Written against test/tier1/tier1_0013_test.go (approved 2026-10-13;
-- mutation-tested with test/tier1/mutants_0013 before this file existed).
-- LEDGER_CONTRACT.md §9 (corrected 2026-10-13), §6.
--
-- The illustrative ₦10 / ₦50 transfer fee is ARGON'S. The bank's VAT is inside
-- whatever the bank charges Argon (the partner component); we charge our own
-- fee, so we account for VAT on our own fee. (Which may make Argon a supplier
-- of a VATable service in its own right: with Legal, alongside the fee.)
--
--   charge_quote computes VAT on argon_margin, not partner_fee.
--   Renamed, because a name that says VAT on the bank's fee when it is VAT on
--   Argon's fee is how the next person gets it wrong:
--     charge_schedules.component  partner_fee_vat  → vat_on_argon_fee
--     ledger_lines.charge_role    partner_vat      → vat
--     charge_quote, withdrawal_holds  partner_vat_kobo → vat_kobo
--   The old names are refused, not kept beside the new. A vat line belongs on
--   the VAT account, and the VAT account carries only vat lines.
--
-- REFUSES TO RUN if anything already uses the old names: a VAT series
-- configured "on the partner's fee" must not silently become VAT on Argon's
-- fee, and a posted line is never rewritten. None existed when this was
-- written; if one does, it is a decision for a person.
-- When it refuses, nothing changes, but migrate has already recorded version
-- 13, dirty: the schema is still at 12. Recover with `migrate force 12`.
-- =============================================================================

BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM charge_schedules WHERE component = 'partner_fee_vat')
     OR EXISTS (SELECT 1 FROM ledger_lines WHERE charge_role = 'partner_vat') THEN
    RAISE EXCEPTION '0013: refusing — VAT series or lines exist under the old names (VAT on the partner''s fee). Converting them changes what was charged; a person decides how.';
  END IF;
END $$;

-- ---- the shape: renamed ------------------------------------------------------------
ALTER TABLE withdrawal_holds RENAME COLUMN partner_vat_kobo TO vat_kobo;
ALTER TABLE charge_schedules DROP CONSTRAINT charge_schedules_component_check;
ALTER TABLE charge_schedules ADD CONSTRAINT charge_schedules_component_check CHECK (component IN ('partner_fee', 'vat_on_argon_fee', 'statutory_levy', 'argon_margin'));
ALTER TABLE ledger_lines DROP CONSTRAINT ledger_lines_charge_role_check;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_charge_role_check CHECK (charge_role IN ('principal', 'partner_charge', 'vat', 'statutory_levy', 'argon_margin'));

CREATE OR REPLACE FUNCTION charge_component(p_society UUID, p_kind movement_kind, p_component TEXT, p_base BIGINT, p_on DATE)
RETURNS BIGINT SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  s_id UUID;
  b    charge_bands%ROWTYPE;
BEGIN
  SELECT id INTO s_id FROM charge_schedules WHERE society_id = p_society AND kind = p_kind AND component = p_component AND effective_from <= p_on
   ORDER BY effective_from DESC, recorded_at DESC, id DESC LIMIT 1;
  IF s_id IS NULL THEN
    CASE p_component
      WHEN 'partner_fee' THEN PERFORM auth_refuse('charge_partner_fee_unset', format('this society has no partner fee for %s in force on %s', p_kind, p_on));
      WHEN 'vat_on_argon_fee' THEN PERFORM auth_refuse('charge_vat_unset', format('this society has no VAT on the %s fee in force on %s', p_kind, p_on));
      WHEN 'statutory_levy' THEN PERFORM auth_refuse('charge_levy_unset', 'whether the transfer levy falls on the sender is with Compliance: it is never assumed to apply, nor to be zero');
      ELSE PERFORM auth_refuse('charge_margin_unset', 'Argon''s margin has not been set: it waits on Legal, and is never assumed to be zero');
    END CASE;
  END IF;
  SELECT * INTO b FROM charge_bands WHERE schedule_id = s_id AND from_kobo <= p_base AND (to_kobo IS NULL OR to_kobo >= p_base);
  IF NOT FOUND THEN
    PERFORM auth_refuse('charge_band_missing', format('no band of this society''s %s %s series covers %s kobo', p_kind, p_component, p_base));
  END IF;
  RETURN b.flat_kobo + CASE WHEN b.cap_kobo IS NULL THEN ((p_base * b.bp + 5000) / 10000) ELSE least(((p_base * b.bp + 5000) / 10000), b.cap_kobo) END;
END $$ LANGUAGE plpgsql STABLE;

DROP FUNCTION charge_quote(UUID, movement_kind, BIGINT, DATE);

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

-- ---- the rules: VAT on Argon's fee; a vat line on the VAT account ------------------
CREATE FUNCTION charge_quote(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE)
RETURNS TABLE (partner_fee_kobo BIGINT, vat_kobo BIGINT, statutory_levy_kobo BIGINT, argon_margin_kobo BIGINT) SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  partner_fee_kobo := 0; vat_kobo := 0; statutory_levy_kobo := 0; argon_margin_kobo := 0;
  IF NOT EXISTS (SELECT 1 FROM charge_schedules WHERE society_id = p_society AND kind = p_kind AND effective_from <= p_on) THEN
    IF p_kind = 'contribution_in' THEN
      RETURN NEXT;   -- a contribution in a society that charges none is free
      RETURN;
    END IF;
  END IF;
  partner_fee_kobo := charge_component(p_society, p_kind, 'partner_fee', p_amount, p_on);
  argon_margin_kobo := charge_component(p_society, p_kind, 'argon_margin', p_amount, p_on);
  -- VAT on Argons own fee: the bank's VAT is inside what the bank charges Argon.
  vat_kobo := charge_component(p_society, p_kind, 'vat_on_argon_fee', argon_margin_kobo, p_on);
  statutory_levy_kobo := charge_component(p_society, p_kind, 'statutory_levy', p_amount, p_on);
  RETURN NEXT;
END $$ LANGUAGE plpgsql STABLE;

CREATE OR REPLACE FUNCTION ledger_line_role_admissible() RETURNS trigger AS $$
DECLARE
  r TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF r IS NULL THEN
    RETURN NEW;
  END IF;
  IF r IN ('member_savings', 'member_wallet', 'member_loan') AND NEW.member_id IS NULL THEN
    RAISE EXCEPTION 'a line on a % account names the member it belongs to', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  IF NEW.member_id IS NOT NULL AND r NOT IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'a line naming a member belongs on a member account, not a % account', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_on_member_account';
  END IF;
  IF (coalesce(NEW.charge_role, '') = 'partner_charge') <> (r = 'charges_partner')
     OR (coalesce(NEW.charge_role, '') = 'argon_margin') <> (r = 'charges_margin')
     OR (coalesce(NEW.charge_role, '') = 'statutory_levy') <> (r = 'statutory_levy_payable')
     OR (coalesce(NEW.charge_role, '') = 'vat') <> (r = 'charges_vat') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

COMMIT;
