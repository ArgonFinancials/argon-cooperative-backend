-- 0011's INTERFACE, with NO RULES: "M0" for suite 0011, single contributions
-- and withdrawals (decided 2026-10-07 and 2026-10-09). Applied on top of the
-- real 0001–0010.
--
--   charge_schedules / charge_bands  a society's charges per movement kind, as
--       banded fees: each band a flat fee and an optional rate and cap, for the
--       partner and for Argon's margin SEPARATELY. A schedule names the edition
--       of the regulation its figures were taken from. No defaults: nothing is
--       seeded, and an unset figure refuses. Written by an operator as owner;
--       the app only reads.
--   charge_quote(society, kind, amount, on)  the charge the SERVER computes.
--       Here it answers zero for everything: the maximally wrong answer.
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
  effective_from DATE NOT NULL,
  edition        TEXT,
  recorded_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE charge_bands (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  schedule_id      UUID NOT NULL REFERENCES charge_schedules(id),
  from_kobo        BIGINT NOT NULL,
  to_kobo          BIGINT,            -- inclusive; NULL: no upper bound
  partner_flat_kobo BIGINT, partner_bp INT, partner_cap_kobo BIGINT,
  margin_flat_kobo  BIGINT, margin_bp  INT, margin_cap_kobo  BIGINT
);
CREATE FUNCTION charge_quote(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE)
RETURNS TABLE (partner_kobo BIGINT, margin_kobo BIGINT) AS $$
  SELECT 0::bigint, 0::bigint;
$$ LANGUAGE sql STABLE;

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
