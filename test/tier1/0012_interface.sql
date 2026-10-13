-- 0012's INTERFACE, with NO NEW RULES: "M0" for suite 0012, the payout hold
-- (decided 2026-10-13, LEDGER_CONTRACT §11, finding 33). Applied on top of the
-- real 0001–0011, so M0 still behaves as 0011 does: it checks a withdrawal when
-- it is RECORDED, and refuses to record one it judges wrong. That is the
-- blindfold 0012 removes.
--
--   withdrawal_holds           a payout authorised to execute: checked, and its
--                              amount and charges held, before money moves
--   withdrawal_hold_releases   a hold freed (the transfer definitively failed),
--                              with its reason
--   ledger_entries.hold_id     the hold a recorded payout settles
--   ledger_discrepancies       what was recorded but differs from what was
--                              authorised: for a person, never a refusal
--   credential_acts / auth_proofs  + withdrawal_authorisation
BEGIN;
CREATE TABLE withdrawal_holds (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id          UUID NOT NULL REFERENCES approval_requests(id),
  society_id          UUID NOT NULL REFERENCES societies(id),
  member_id           UUID NOT NULL REFERENCES members(id),
  amount_kobo         BIGINT NOT NULL,
  partner_fee_kobo    BIGINT,
  partner_vat_kobo    BIGINT,
  statutory_levy_kobo BIGINT,
  argon_margin_kobo   BIGINT,
  actor_user_id       UUID NOT NULL REFERENCES users(id),
  act_id              UUID REFERENCES credential_acts(id),
  held_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE withdrawal_hold_releases (
  hold_id       UUID PRIMARY KEY REFERENCES withdrawal_holds(id),
  actor_user_id UUID NOT NULL REFERENCES users(id),
  reason        TEXT,
  at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ledger_entries ADD COLUMN hold_id UUID REFERENCES withdrawal_holds(id);
CREATE TABLE ledger_discrepancies (
  id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entry_id  UUID NOT NULL REFERENCES ledger_entries(id),
  kind      TEXT NOT NULL,
  detail    TEXT NOT NULL,
  raised_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation'));
COMMIT;
