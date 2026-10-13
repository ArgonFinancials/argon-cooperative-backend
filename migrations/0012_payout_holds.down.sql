-- Reverses 0012 only while nothing rests on it: once a hold, a release, a
-- flag, or a payout naming a hold exists, 0012 is part of how history is read.
-- When it refuses, nothing changes, but migrate has already recorded version
-- 11, dirty. The schema is still at 12: recover with `migrate force 12`
-- (deploy/README.md, "migrate says dirty").
--
-- Going down restores 0011's recording-time judgements: the blindfold of
-- finding 33. grants.sql names 0012's tables and columns: after going down,
-- apply the grants.sql of the commit before 0012.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM withdrawal_holds) OR EXISTS (SELECT 1 FROM withdrawal_hold_releases)
     OR EXISTS (SELECT 1 FROM ledger_discrepancies) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id IS NOT NULL)
     OR EXISTS (SELECT 1 FROM credential_acts WHERE kind = 'withdrawal_authorisation')
     OR EXISTS (SELECT 1 FROM auth_proofs WHERE act_kind = 'withdrawal_authorisation') THEN
    RAISE EXCEPTION '0012 down: refusing — holds, releases, flags or held payouts rest on it';
  END IF;
END $$;

-- 0011's rules, restored verbatim.
CREATE OR REPLACE FUNCTION ledger_entry_settles() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e        ledger_entries%ROWTYPE;
  a        RECORD;
  q        RECORD;
  r        approval_requests%ROWTYPE;
  base     BIGINT;
  c_fee    BIGINT;
  c_vat    BIGINT;
  c_levy   BIGINT;
  c_margin BIGINT;
  v_floor  BIGINT;
  v_before BIGINT;
  v_held   BIGINT;
  v_person UUID;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.id;
  IF e.kind NOT IN ('contribution_in', 'withdrawal_out') OR e.reverses_id IS NOT NULL OR e.adjusts_id IS NOT NULL THEN
    RETURN NULL;
  END IF;
  -- The step-up: a single movement (a batch's entries rest on the batch's act).
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

  -- The charge: what the series say for the amount through the bank.
  SELECT coalesce(abs(sum(l.amount_kobo)), 0) INTO base
    FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id WHERE l.entry_id = e.id AND ac.role = 'bank';
  SELECT * INTO q FROM charge_quote(e.society_id, e.kind, base, e.value_date);
  SELECT coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'partner_charge'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'partner_vat'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'statutory_levy'), 0),
         coalesce(-sum(amount_kobo) FILTER (WHERE charge_role = 'argon_margin'), 0) INTO c_fee, c_vat, c_levy, c_margin
    FROM ledger_lines WHERE entry_id = e.id;
  IF c_fee <> q.partner_fee_kobo OR c_vat <> q.partner_vat_kobo OR c_levy <> q.statutory_levy_kobo OR c_margin <> q.argon_margin_kobo THEN
    PERFORM auth_refuse('entry_charge_is_the_schedules', format('by the series this %s of %s kobo is charged %s fee, %s VAT, %s levy and %s margin; the entry carries %s, %s, %s and %s', e.kind, base, q.partner_fee_kobo, q.partner_vat_kobo, q.statutory_levy_kobo, q.argon_margin_kobo, c_fee, c_vat, c_levy, c_margin));
  END IF;
  IF e.kind = 'contribution_in' THEN
    RETURN NULL;
  END IF;

  -- The withdrawal: the request it pays, for the amount approved (the bank
  -- pays exactly that; the charges are on top), out of the requester's savings.
  SELECT * INTO r FROM approval_requests WHERE id = e.request_id;
  IF r.kind IS DISTINCT FROM 'withdrawal_out' OR r.society_id IS DISTINCT FROM e.society_id
     OR base IS DISTINCT FROM r.amount_kobo
     OR EXISTS (SELECT 1 FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
                 WHERE l.entry_id = e.id AND ac.role = 'member_savings' AND l.member_id IS DISTINCT FROM r.member_id) THEN
    PERFORM auth_refuse('withdrawal_matches_request', format('this payout of %s kobo is not request %s (%s kobo, for member %s)', base, r.id, r.amount_kobo, r.member_id));
  END IF;

  -- What the member may withdraw (D-10): savings before this entry, less the
  -- full outstanding of every loan the PERSON guarantees, less the floor.
  SELECT account_floor_kobo INTO v_floor FROM societies WHERE id = e.society_id;
  IF v_floor IS NULL THEN
    PERFORM auth_refuse('account_floor_unset', 'this society has not set its account floor');
  END IF;
  PERFORM 1 FROM members WHERE id = r.member_id FOR UPDATE;   -- one withdrawal at a time per member
  SELECT coalesce(-sum(l.amount_kobo), 0) INTO v_before
    FROM ledger_lines l JOIN accounts ac ON ac.id = l.account_id
   WHERE ac.role = 'member_savings' AND l.member_id = r.member_id AND l.entry_id <> e.id;
  SELECT user_id INTO v_person FROM members WHERE id = r.member_id;
  SELECT coalesce(sum(greatest(loan_owed(g.loan_id), 0)), 0) INTO v_held
    FROM loan_guarantees g JOIN loans lo ON lo.id = g.loan_id
   WHERE (g.guarantor_member_id = r.member_id OR g.guarantor_user_id = v_person) AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id);
  IF r.amount_kobo + q.partner_fee_kobo + q.partner_vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo > v_before - v_held - v_floor THEN
    PERFORM auth_refuse('withdrawal_within_withdrawable', format('%s kobo out (the amount and its charge) against %s withdrawable: savings %s, held for guarantees %s, floor %s', r.amount_kobo + q.partner_fee_kobo + q.partner_vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo, v_before - v_held - v_floor, v_before, v_held, v_floor));
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION withdrawal_names_its_authority() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind <> 'withdrawal_out' OR NEW.reverses_id IS NOT NULL OR NEW.adjusts_id IS NOT NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.request_id IS NULL THEN
    PERFORM auth_refuse('withdrawal_names_request', 'money leaves a member''s savings only on an approved withdrawal request: this payout names none');
  END IF;
  IF approval_status(NEW.request_id) <> 'approved' THEN
    PERFORM auth_refuse('withdrawal_request_approved', format('request %s is %s, not approved', NEW.request_id, approval_status(NEW.request_id)));
  END IF;
  IF NEW.payout_reference IS NULL OR btrim(NEW.payout_reference) = '' THEN
    PERFORM auth_refuse('withdrawal_names_payout_reference', 'a payout names the transfer that paid it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP INDEX payout_recorded_once;
CREATE UNIQUE INDEX withdrawal_posted_once ON ledger_entries (request_id) WHERE kind = 'withdrawal_out';

DROP TRIGGER withdrawal_hold_releases_admissible ON withdrawal_hold_releases;
DROP FUNCTION withdrawal_hold_release_admissible();
DROP TRIGGER withdrawal_holds_admissible ON withdrawal_holds;
DROP FUNCTION withdrawal_hold_admissible();
DROP TABLE ledger_discrepancies;
ALTER TABLE ledger_entries DROP COLUMN hold_id;
DROP TABLE withdrawal_hold_releases;
DROP TABLE withdrawal_holds;

ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout'));
COMMIT;
