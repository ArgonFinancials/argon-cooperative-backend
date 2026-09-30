-- =============================================================================
-- 0002 INTERFACE — NAMES ONLY, NO ENFORCEMENT.
--
-- The tables and columns the 0002 acceptance tests are written against, with
-- none of the rules the tests check. This is NOT a migration. It is part of
-- what is under review: if a name here is wrong, the tests are wrong.
--
-- Applied on top of 0001, it is "M0" in the mutation run — the maximally
-- broken schema — and every refusal test must go RED against it.
-- When 0002 exists it replaces this file and the tests run against
-- migrations/ alone.
-- =============================================================================
BEGIN;

CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  full_name  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A member record belongs to a person. One person can be two member records
-- (the import's duplicate case), and both point at the same user.
ALTER TABLE members ADD COLUMN user_id UUID REFERENCES users(id);

-- The signer, SNAPSHOTTED at signing (§4). The key PRD 8.4 needs.
ALTER TABLE signatory_approvals ADD COLUMN user_id UUID REFERENCES users(id);

-- Which member's sub-ledger a line posts to. Replaces ledger_entries.member_id,
-- which 0002 drops. It stays here, in 0001's shape, because dropping it is
-- one of the things under test.
ALTER TABLE ledger_lines ADD COLUMN member_id UUID;

-- An adjusting entry references the entry it corrects (§2, §5).
ALTER TABLE ledger_entries ADD COLUMN adjusts_id UUID;

CREATE TABLE accounting_periods (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id UUID NOT NULL REFERENCES societies(id),
  starts_on  DATE NOT NULL,
  ends_on    DATE NOT NULL,
  closed_at  TIMESTAMPTZ,            -- NULL = open
  closed_by  UUID REFERENCES users(id)
);

COMMIT;
