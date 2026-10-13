-- =============================================================================
-- ARGON — 0011: SINGLE CONTRIBUTIONS AND WITHDRAWALS
--
-- Written against test/tier1/tier1_0011_test.go (approved 2026-10-11, revised
-- to four charge components and re-approved 2026-10-12; mutation-tested with
-- test/tier1/mutants_0011 before this file existed). LEDGER_CONTRACT.md §8–§10.
--
--   CHARGES are configuration with no default, refused until set, computed by
--   the database (charge_quote), never accepted from a client. Four components,
--   each its own series from its own NAMED SOURCE, each on its own line and
--   account:
--     partner_fee      bands on the amount
--     partner_fee_vat  a RATE ON THE FEE (never a per-band figure)
--     statutory_levy   its own bands; REFUSED until Compliance answers its
--                      incidence (never assumed to apply, nor zero)
--     argon_margin     REFUSED until Legal answers (never assumed zero)
--   A band: a flat fee plus a rate (half up to the kobo) with an optional cap
--   on the rate part. Configuration is never edited: a correction is a new
--   series, effective from a date. Nothing is seeded. The operator writes it as
--   the owner; the app only reads it. A contribution in a society that charges
--   none is free.
--
--   AN ENTRY carries exactly the series' charge (at commit). A single
--   contribution or payout rests on its poster's own PIN step-up: their own
--   act, of its kind, from this transaction, used once (at commit, so the
--   foreign keys refuse first).
--
--   A WITHDRAWAL is posted when an officer records its payout: on its approved
--   request (once), naming the transfer, for the amount approved, out of the
--   requester's savings, the charges ON TOP, within what the member may
--   withdraw (D-10): savings − guarantees held (the full outstanding of every
--   loan the PERSON guarantees, the conservative default) − the account floor
--   (no default: refused until set).
--
--   A WITHDRAWAL REQUEST names its member and needs the society's quorum (no
--   default). Nobody signs a request they benefit from (§10): the member a
--   withdrawal pays never signs it.
--
--   GOVERNANCE (§10): the quorums and the floor are bye-law decisions, set by
--   the operator script until a request kind exists to change them. grants.sql
--   gives the app no INSERT or UPDATE on them: a society created by the app
--   cannot carry a quorum of 1.
--
-- AS THE APP (findings 16, 25): every rule function that reads beyond the row
-- it judges is SECURITY DEFINER with a pinned search_path (the role rule reads
-- only accounts.role, which the app may read, as 0004's did); grants.sql adds act_id,
-- request_id and payout_reference to argon_app's INSERT list; the probe posts,
-- signs and is refused as argon_app (P25–P29, K25).
--
-- Rows written before 0011 are not re-judged.
-- =============================================================================

BEGIN;

-- ---- the shape ----------------------------------------------------------------
CREATE TABLE charge_schedules (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id     UUID NOT NULL REFERENCES societies(id),
  kind           movement_kind NOT NULL,
  component      TEXT NOT NULL CHECK (component IN ('partner_fee', 'partner_fee_vat', 'statutory_levy', 'argon_margin')),
  effective_from DATE NOT NULL,
  source         TEXT,              -- the named source its figures were taken from
  recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A band's base is the transfer amount, except for partner_fee_vat, whose base
-- is the partner fee.
CREATE TABLE charge_bands (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  schedule_id UUID NOT NULL REFERENCES charge_schedules(id),
  from_kobo   BIGINT NOT NULL,
  to_kobo     BIGINT,               -- inclusive; NULL: no upper bound
  flat_kobo   BIGINT,
  bp          INT,
  cap_kobo    BIGINT                -- caps the rate part; NULL: no cap
);

ALTER TABLE ledger_lines DROP CONSTRAINT ledger_lines_charge_role_check;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_charge_role_check CHECK (charge_role IN (
  'principal', 'partner_charge', 'partner_vat', 'statutory_levy', 'argon_margin'));
ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general', 'loan_loss', 'loan_recovery_income',
  'charges_vat', 'statutory_levy_payable'));

ALTER TABLE societies ADD COLUMN account_floor_kobo BIGINT;
ALTER TABLE societies ADD COLUMN withdrawal_signatures_required SMALLINT;

ALTER TABLE ledger_entries ADD COLUMN act_id UUID REFERENCES credential_acts(id);
ALTER TABLE ledger_entries ADD COLUMN request_id UUID REFERENCES approval_requests(id);
ALTER TABLE ledger_entries ADD COLUMN payout_reference TEXT;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout'));

-- ---- the rules: configuration ----------------------------------------------------
ALTER TABLE charge_schedules ADD CONSTRAINT charge_schedule_names_source CHECK (source IS NOT NULL AND btrim(source) <> '');

ALTER TABLE charge_bands ADD CONSTRAINT charge_band_whole CHECK (flat_kobo IS NOT NULL AND bp IS NOT NULL);

CREATE FUNCTION charge_configuration_immutable() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('charge_configuration_immutable', 'charge configuration is never edited: a correction is a new series, effective from a date');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER charge_schedules_immutable BEFORE UPDATE OR DELETE ON charge_schedules
  FOR EACH ROW EXECUTE FUNCTION charge_configuration_immutable();
CREATE TRIGGER charge_bands_immutable BEFORE UPDATE OR DELETE ON charge_bands
  FOR EACH ROW EXECUTE FUNCTION charge_configuration_immutable();

CREATE FUNCTION charge_bands_disjoint() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM 1 FROM charge_schedules WHERE id = NEW.schedule_id FOR UPDATE;
  IF EXISTS (SELECT 1 FROM charge_bands b WHERE b.schedule_id = NEW.schedule_id
              AND b.from_kobo <= coalesce(NEW.to_kobo, 9223372036854775807)
              AND NEW.from_kobo <= coalesce(b.to_kobo, 9223372036854775807)) THEN
    PERFORM auth_refuse('charge_bands_do_not_overlap', format('a band from %s kobo overlaps another band of this series: an amount would have two charges', NEW.from_kobo));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER charge_bands_disjoint BEFORE INSERT ON charge_bands FOR EACH ROW EXECUTE FUNCTION charge_bands_disjoint();

-- ---- the rules: the charge the server computes --------------------------------------
-- One component's charge on `p_base` (the amount; for VAT, the partner fee),
-- from the series in force on `p_on`. Undecided refuses, naming who decides.
CREATE FUNCTION charge_component(p_society UUID, p_kind movement_kind, p_component TEXT, p_base BIGINT, p_on DATE)
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
      WHEN 'partner_fee_vat' THEN PERFORM auth_refuse('charge_vat_unset', format('this society has no VAT on the %s fee in force on %s', p_kind, p_on));
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

CREATE FUNCTION charge_quote(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE)
RETURNS TABLE (partner_fee_kobo BIGINT, partner_vat_kobo BIGINT, statutory_levy_kobo BIGINT, argon_margin_kobo BIGINT) SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  partner_fee_kobo := 0; partner_vat_kobo := 0; statutory_levy_kobo := 0; argon_margin_kobo := 0;
  IF NOT EXISTS (SELECT 1 FROM charge_schedules WHERE society_id = p_society AND kind = p_kind AND effective_from <= p_on) THEN
    IF p_kind = 'contribution_in' THEN
      RETURN NEXT;   -- a contribution in a society that charges none is free
      RETURN;
    END IF;
  END IF;
  partner_fee_kobo := charge_component(p_society, p_kind, 'partner_fee', p_amount, p_on);
  partner_vat_kobo := charge_component(p_society, p_kind, 'partner_fee_vat', partner_fee_kobo, p_on);
  statutory_levy_kobo := charge_component(p_society, p_kind, 'statutory_levy', p_amount, p_on);
  argon_margin_kobo := charge_component(p_society, p_kind, 'argon_margin', p_amount, p_on);
  RETURN NEXT;
END $$ LANGUAGE plpgsql STABLE;

-- ---- the rules: each charge on its own account, both directions (0004's rule,
-- with VAT and the levy, remitted not earned, beside the fee and the margin) ----
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
     OR (coalesce(NEW.charge_role, '') = 'partner_vat') <> (r = 'charges_vat')
     OR (coalesce(NEW.charge_role, '') = 'statutory_levy') <> (r = 'statutory_levy_payable') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- ---- the rules: a withdrawal names what authorises it (at insert) ---------------
CREATE FUNCTION withdrawal_names_its_authority() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE TRIGGER ledger_entries_w_withdrawal BEFORE INSERT ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION withdrawal_names_its_authority();

CREATE UNIQUE INDEX withdrawal_posted_once ON ledger_entries (request_id) WHERE kind = 'withdrawal_out';

CREATE UNIQUE INDEX entry_act_once ON ledger_entries (act_id);

-- ---- the rules: at commit, the step-up, the charge, the withdrawal ---------------
CREATE FUNCTION ledger_entry_settles() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
CREATE CONSTRAINT TRIGGER ledger_entries_settle AFTER INSERT ON ledger_entries
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_entry_settles();

-- ---- the rules: who authorises a withdrawal --------------------------------------
CREATE FUNCTION withdrawal_request_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q SMALLINT;
BEGIN
  IF NEW.kind <> 'withdrawal_out' THEN
    RETURN NEW;
  END IF;
  IF NEW.member_id IS NULL THEN
    PERFORM auth_refuse('withdrawal_request_names_member', 'a withdrawal request names the member whose savings it moves');
  END IF;
  SELECT withdrawal_signatures_required INTO q FROM societies WHERE id = NEW.society_id;
  IF q IS NULL THEN
    PERFORM auth_refuse('withdrawal_quorum_unset', 'this society has not decided how many signatures a withdrawal needs');
  END IF;
  IF NEW.required <> q THEN
    PERFORM auth_refuse('withdrawal_quorum_is_societys', format('this society''s withdrawals need %s signatures, not %s', q, NEW.required));
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION withdrawal_request_admissible();

-- Nobody signs a request they benefit from (§10), per PERSON.
CREATE FUNCTION signature_not_requester() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN members rm ON rm.id = r.member_id
              WHERE r.id = NEW.request_id AND r.kind = 'withdrawal_out'
                AND (r.member_id = NEW.member_id OR rm.user_id = (SELECT user_id FROM members WHERE id = NEW.member_id))) THEN
    PERFORM auth_refuse('requester_never_signs_own_withdrawal', 'the member a withdrawal is for never signs it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_not_requester BEFORE INSERT ON signatory_approvals
  FOR EACH ROW EXECUTE FUNCTION signature_not_requester();

COMMIT;
