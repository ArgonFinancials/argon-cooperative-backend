-- 0011's INTERFACE, with NO RULES: "M0" for suite 0011, single contributions
-- and withdrawals (decided 2026-10-07, 2026-10-09 and 2026-10-11). Applied on
-- top of the real 0001–0010.
--
--   charge_schedules / charge_bands  a society's charges per movement kind, in
--       FOUR COMPONENTS, each its own series from its own named source:
--         partner_fee      the bank's fee, banded on the amount (the CBN guide)
--         partner_fee_vat  VAT, a RATE ON THE PARTNER FEE, not on the amount
--         statutory_levy   the transfer levy, banded on the amount (its statute)
--         argon_margin     Argon's, banded on the amount (pending Legal)
--       A band: a flat fee and a rate with an optional cap on the rate. Nothing
--       is seeded; an unset component refuses. Written by an operator as
--       owner; the app only reads.
--   charge_quote(society, kind, amount, on)  the charge the SERVER computes,
--       per component. Here it answers zero for everything: maximally wrong.
--   ledger_lines.charge_role  + partner_vat, statutory_levy
--   accounts.role             + charges_vat, statutory_levy_payable
--       (remitted, not earned: kept apart from the fee and the margin)
--   societies.account_floor_kobo, .withdrawal_signatures_required  no default.
--   ledger_entries.act_id            the poster's own step-up, for a single
--                                    contribution or a withdrawal payout
--   ledger_entries.request_id        the approved withdrawal request it pays
--   ledger_entries.payout_reference  the transfer that paid it
--   credential_acts / auth_proofs    + contribution_record, withdrawal_payout
BEGIN;
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
CREATE FUNCTION charge_quote(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE)
RETURNS TABLE (partner_fee_kobo BIGINT, partner_vat_kobo BIGINT, statutory_levy_kobo BIGINT, argon_margin_kobo BIGINT) AS $$
  SELECT 0::bigint, 0::bigint, 0::bigint, 0::bigint;
$$ LANGUAGE sql STABLE;

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
COMMIT;
