-- 0008's INTERFACE, with NO RULES: "M0" for suite 0008, the maximally broken
-- loan book (LOAN_SHAPE.md). The tables and columns exist so the tests can be
-- written and run; nothing refuses anything. Applied on top of the real
-- 0001–0006 and the 0007 (approvals) interface.
--
--   loan_products      a society's products; terms are CONFIGURATION, the rate included
--   loans              the AGREEMENT: borrower, product, principal, term, terms snapshotted
--   loan_instalments   the schedule, fixed at agreement
--   loan_guarantees    a guarantor's consent, resting on their own step-up act
--   loan_guarantee_withdrawals   a consent withdrawn, by the same person's act
--   ledger_entries.loan_id       the ledger's link to the loan it moves money for
--   approval_requests.loan_id    the disbursement request's loan
--   credential_acts.kind (and auth_proofs.act_kind) gain 'guarantee', 'guarantee_withdrawal'
--
-- There is NO outstanding-balance column anywhere: what is owed is derived
-- from the ledger, always (LOAN_SHAPE.md §1).
BEGIN;
CREATE TABLE loan_products (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id           UUID NOT NULL REFERENCES societies(id),
  code                 TEXT NOT NULL,
  name                 TEXT NOT NULL,
  max_term_months      SMALLINT NOT NULL,
  guarantors_required  SMALLINT NOT NULL,
  savings_multiple_pct INTEGER NOT NULL,          -- 300 = three times savings
  interest_rate_bp     INTEGER NOT NULL DEFAULT 0, -- basis points; configuration, not an assumption
  allows_prepayment    BOOLEAN NOT NULL,
  UNIQUE (society_id, code)
);
CREATE TABLE loans (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id           UUID NOT NULL REFERENCES societies(id),
  member_id            UUID NOT NULL REFERENCES members(id),
  product_id           UUID NOT NULL REFERENCES loan_products(id),
  principal_kobo       BIGINT NOT NULL,
  term_months          SMALLINT NOT NULL,
  purpose              TEXT NOT NULL,
  -- The product's terms, snapshotted at agreement (§4's rule, for terms).
  guarantors_required  SMALLINT NOT NULL,
  savings_multiple_pct INTEGER NOT NULL,
  interest_rate_bp     INTEGER NOT NULL,
  allows_prepayment    BOOLEAN NOT NULL,
  supersedes_id        UUID REFERENCES loans(id),
  agreed_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  agreed_xid           xid8 NOT NULL DEFAULT pg_current_xact_id()
);
CREATE TABLE loan_instalments (
  loan_id     UUID NOT NULL REFERENCES loans(id),
  n           SMALLINT NOT NULL,
  due_on      DATE NOT NULL,
  amount_kobo BIGINT NOT NULL,
  PRIMARY KEY (loan_id, n)
);
CREATE TABLE loan_guarantees (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  loan_id             UUID NOT NULL REFERENCES loans(id),
  guarantor_member_id UUID NOT NULL REFERENCES members(id),
  guarantor_user_id   UUID NOT NULL REFERENCES users(id),
  act_id              UUID NOT NULL REFERENCES credential_acts(id),
  at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE loan_guarantee_withdrawals (
  guarantee_id UUID PRIMARY KEY REFERENCES loan_guarantees(id),
  act_id       UUID NOT NULL REFERENCES credential_acts(id),
  at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ledger_entries ADD COLUMN loan_id UUID REFERENCES loans(id);
ALTER TABLE approval_requests ADD COLUMN loan_id UUID REFERENCES loans(id);

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release',
  'signatory_reset_request', 'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal'));
COMMIT;
