-- =============================================================================
-- ARGON — INITIAL SCHEMA
--
-- The constraints in this file are TIER 1. They are enforced by the DATABASE,
-- not by application code, because an application-level rule is a rule that a
-- migration script, a support tool or a future integration reaches around at
-- 2am. Each one carries the reason it exists.
--
-- Read /LEDGER_CONTRACT.md before changing anything here.
--
-- MONEY IS BIGINT KOBO. ₦1 = 100 kobo. Never NUMERIC, never a float, at any
-- layer. On the wire money is a STRING, because a JSON number is an IEEE-754
-- double and would silently violate this rule at the one layer nobody
-- inspects.
-- =============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- =============================================================================
-- TENANCY
-- =============================================================================

CREATE TYPE society_lifecycle AS ENUM (
  'prospect', 'verifying', 'provisioning', 'handover', 'active',
  'dormant', 'departed'
);

CREATE TABLE societies (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code                TEXT NOT NULL UNIQUE,
  registered_name     TEXT NOT NULL,
  -- Registration under the Nigerian Cooperative Societies Act. NOT a CAC
  -- business-name number; they are different registers.
  registration_number TEXT NOT NULL,
  state_directorate   TEXT NOT NULL,
  lifecycle           society_lifecycle NOT NULL DEFAULT 'prospect',
  -- The society's own policy, within platform bounds.
  guard_threshold_kobo BIGINT NOT NULL DEFAULT 10000000, -- ₦100,000
  signatures_required  SMALLINT NOT NULL DEFAULT 3,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT guard_threshold_positive CHECK (guard_threshold_kobo > 0),
  CONSTRAINT signatures_sane CHECK (signatures_required BETWEEN 1 AND 9)
);

CREATE TABLE branches (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id  UUID NOT NULL REFERENCES societies(id),
  code        TEXT NOT NULL,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (society_id, code)
);

-- =============================================================================
-- PEOPLE
-- =============================================================================

CREATE TYPE member_status AS ENUM ('pending', 'active', 'suspended', 'exited');

-- A member sits on exactly ONE collection channel per cycle. Two channels
-- means a member collected twice, which is a money defect of the same class as
-- an unbalanced entry. The type makes the alternative unrepresentable.
CREATE TYPE collection_channel AS ENUM ('payroll', 'subscription', 'cash');

CREATE TABLE members (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id         UUID NOT NULL REFERENCES societies(id),
  branch_id          UUID NOT NULL REFERENCES branches(id),
  member_number      TEXT NOT NULL,
  full_name          TEXT NOT NULL,
  phone              TEXT NOT NULL,
  status             member_status NOT NULL DEFAULT 'pending',
  collection_channel collection_channel NOT NULL,
  joined_on          DATE NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

  UNIQUE (society_id, member_number)
);

CREATE INDEX members_society_idx ON members (society_id, status);

-- -----------------------------------------------------------------------------
-- OFFICER POSTS — attribution is POINT IN TIME.
--
-- A post is held for a RANGE. A March signature must still read as signed by
-- March's treasurer after a handover in June. If attribution resolved through
-- "who is treasurer now", a handover would silently reassign every historical
-- signature at once, retrospectively, for every prior year — and a
-- cooperative's defence in a dispute is that the books say who did what and
-- when.
-- -----------------------------------------------------------------------------

CREATE TYPE officer_post AS ENUM (
  'chairman', 'treasurer', 'secretary', 'internal_auditor',
  'branch_officer', 'loan_officer'
);

CREATE TABLE appointments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id  UUID NOT NULL REFERENCES societies(id),
  member_id   UUID NOT NULL REFERENCES members(id),
  post        officer_post NOT NULL,
  -- The internal auditor is read-only by Directorate rule: signing a
  -- disbursement is a write. Enforced below, not left to the application.
  is_signatory BOOLEAN NOT NULL DEFAULT false,
  held_from   DATE NOT NULL,
  held_until  DATE,                       -- NULL means currently held
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT range_sane CHECK (held_until IS NULL OR held_until >= held_from),
  CONSTRAINT auditor_never_signs
    CHECK (post <> 'internal_auditor' OR is_signatory = false)
);

-- One holder per post per society at any moment.
CREATE UNIQUE INDEX appointments_one_current
  ON appointments (society_id, post)
  WHERE held_until IS NULL;

-- =============================================================================
-- THE LEDGER
--
-- A posted entry is IMMUTABLE. A correction is a reversal and a repost, or an
-- adjusting entry. There is no update path — see the trigger below.
-- =============================================================================

CREATE TYPE account_kind AS ENUM
  ('asset', 'liability', 'equity', 'income', 'expense');

CREATE TABLE accounts (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id  UUID NOT NULL REFERENCES societies(id),
  code        TEXT NOT NULL,
  name        TEXT NOT NULL,
  kind        account_kind NOT NULL,
  postable    BOOLEAN NOT NULL DEFAULT true,
  UNIQUE (society_id, code)
);

-- Every movement of money in the product. The frontend's MovementKind must
-- match this exactly — the OpenAPI enum is generated from it.
CREATE TYPE movement_kind AS ENUM (
  'contribution_in',
  'withdrawal_out',
  'loan_disbursement',
  'loan_repayment',
  'transfer_out',
  'payroll_remittance',   -- free: the employer's remittance carries no charge
  'project_funding',
  'opening_balance'       -- free, DELIBERATELY. See below.
);

-- opening_balance carries no charge on purpose, not by oversight. Charging on
-- a migration takes a cut of money that was in the society's books before
-- Argon existed, at the moment a cooperative is deciding whether to adopt. It
-- is also not a contribution: a member's statement must not say they
-- contributed ₦480,000 on the day the software changed.

CREATE TABLE ledger_batches (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id  UUID NOT NULL REFERENCES societies(id),
  -- The idempotency key. Minted CLIENT-SIDE from a SHA-256 of the uploaded
  -- file's content, so a retry of one upload carries the same key and a
  -- corrected file carries its own. The server never generates one.
  reference   TEXT NOT NULL,
  as_of       DATE,          -- the society's own as-at date, NEVER the import date
  posted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_user_id UUID NOT NULL,
  row_count   INTEGER NOT NULL,
  total_kobo  BIGINT NOT NULL,

  UNIQUE (society_id, reference)
);

CREATE TABLE ledger_entries (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id    UUID NOT NULL REFERENCES societies(id),
  batch_id      UUID REFERENCES ledger_batches(id),
  member_id     UUID REFERENCES members(id),
  kind          movement_kind NOT NULL,

  -- The society's own as-at date where one applies, else the posting date.
  value_date    DATE NOT NULL,
  posted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- ATTRIBUTION, SNAPSHOTTED AT WRITE TIME. Never resolved by looking up who
  -- currently holds the post.
  -- KNOWN GAP: actor_user_id references nothing — there is no users table
  -- until 0002 (with auth). Until then any UUID is accepted here.
  actor_user_id     UUID NOT NULL,
  actor_post        officer_post,
  actor_post_label  TEXT NOT NULL,

  -- A reversal is a NEW entry pointing at the original. The original is never
  -- altered.
  reverses_id   UUID REFERENCES ledger_entries(id),
  reason        TEXT,

  -- Source traceability: a posted line can be traced to the file row it came
  -- from, which is how an import is audited after the fact.
  source_line   INTEGER,

  CONSTRAINT reversal_states_reason
    CHECK (reverses_id IS NULL OR (reason IS NOT NULL AND length(trim(reason)) > 10))
);

CREATE INDEX ledger_entries_society_idx ON ledger_entries (society_id, posted_at DESC);
CREATE INDEX ledger_entries_member_idx  ON ledger_entries (member_id, posted_at DESC);
CREATE INDEX ledger_entries_batch_idx   ON ledger_entries (batch_id);

-- -----------------------------------------------------------------------------
-- LINES — the double entry itself.
--
-- The trial balance sums to zero BY CONSTRUCTION, not by reconciliation. A
-- transaction whose debits do not equal its credits is REJECTED at write time,
-- not flagged and not queued for review.
-- -----------------------------------------------------------------------------

CREATE TABLE ledger_lines (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  entry_id    UUID NOT NULL REFERENCES ledger_entries(id),
  account_id  UUID NOT NULL REFERENCES accounts(id),
  -- Signed kobo: positive debit, negative credit. One column rather than two
  -- means "balanced" is a sum to zero rather than a comparison, and a sum
  -- cannot be got wrong by filling in the wrong column.
  amount_kobo BIGINT NOT NULL,

  -- The partner charge and the Argon margin are recorded SEPARATELY. Summed
  -- into one total the distinction is unrecoverable from history, and
  -- transaction charges are the only revenue line in the product.
  charge_role TEXT CHECK (charge_role IN ('principal', 'partner_charge', 'argon_margin')),

  CONSTRAINT amount_not_zero CHECK (amount_kobo <> 0)
);

CREATE INDEX ledger_lines_entry_idx   ON ledger_lines (entry_id);
CREATE INDEX ledger_lines_account_idx ON ledger_lines (account_id);

-- -----------------------------------------------------------------------------
-- IMMUTABILITY, ENFORCED.
--
-- An operation that exists is an operation someone eventually calls. There is
-- no application path that updates a posted entry — and there is no database
-- path either.
-- -----------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION refuse_mutation() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION
    'ledger rows are immutable: % on % is refused. A correction is a reversal '
    'and a repost, or an adjusting entry. See /LEDGER_CONTRACT.md §2.',
    TG_OP, TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_immutable
  BEFORE UPDATE OR DELETE ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION refuse_mutation();

CREATE TRIGGER ledger_lines_immutable
  BEFORE UPDATE OR DELETE ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION refuse_mutation();

CREATE TRIGGER ledger_batches_immutable
  BEFORE UPDATE OR DELETE ON ledger_batches
  FOR EACH ROW EXECUTE FUNCTION refuse_mutation();

-- -----------------------------------------------------------------------------
-- BALANCE, ENFORCED AT COMMIT.
--
-- Deferred to the end of the transaction, because the lines of one entry are
-- inserted across several statements. An entry that does not balance takes the
-- whole transaction down — which is the point.
-- -----------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION assert_entry_balanced() RETURNS TRIGGER AS $$
DECLARE
  imbalance BIGINT;
BEGIN
  SELECT COALESCE(SUM(amount_kobo), 0) INTO imbalance
  FROM ledger_lines WHERE entry_id = NEW.entry_id;

  IF imbalance <> 0 THEN
    RAISE EXCEPTION
      'entry % does not balance: debits minus credits = % kobo. The trial '
      'balance sums to zero by construction, never by reconciliation.',
      NEW.entry_id, imbalance;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER ledger_entry_balances
  AFTER INSERT ON ledger_lines
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION assert_entry_balanced();

-- =============================================================================
-- WALLET AND RECONCILIATION
--
-- Argon holds funds in a sub-wallet under VFD's licence. VFD's balance is the
-- RECORD OF ACCOUNT; this ledger is the BOOK OF ACCOUNT. They will periodically
-- disagree — in-flight transfers, provider fees, settlement timing — and
-- adjusting the books to match VFD would destroy the thing the ledger exists
-- for. A difference is a reconciliation item to be EXPLAINED, never a
-- correction to be applied.
-- =============================================================================

CREATE TABLE wallets (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id   UUID NOT NULL REFERENCES societies(id) UNIQUE,
  provider     TEXT NOT NULL DEFAULT 'VFD',
  provider_ref TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wallet_reconciliations (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id          UUID NOT NULL REFERENCES wallets(id),
  observed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  provider_balance_kobo BIGINT NOT NULL,
  ledger_balance_kobo   BIGINT NOT NULL,
  -- Stored, not derived, so a historical difference stays what it was at the
  -- moment it was observed.
  difference_kobo    BIGINT NOT NULL,
  explanation        TEXT,
  resolved_at        TIMESTAMPTZ
);

-- =============================================================================
-- APPROVALS
-- =============================================================================

CREATE TYPE approval_state AS ENUM
  ('awaiting', 'approved', 'rejected', 'expired');

CREATE TABLE approval_requests (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id    UUID NOT NULL REFERENCES societies(id),
  reference     TEXT NOT NULL,
  member_id     UUID REFERENCES members(id),
  kind          movement_kind NOT NULL,
  amount_kobo   BIGINT NOT NULL CHECK (amount_kobo > 0),
  required      SMALLINT NOT NULL CHECK (required BETWEEN 1 AND 9),
  state         approval_state NOT NULL DEFAULT 'awaiting',
  raised_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at    TIMESTAMPTZ NOT NULL,
  UNIQUE (society_id, reference)
);

CREATE TABLE signatory_approvals (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id    UUID NOT NULL REFERENCES approval_requests(id),
  member_id     UUID NOT NULL REFERENCES members(id),
  -- Snapshotted, like every other attribution in this schema.
  post_at_signing officer_post NOT NULL,
  signed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- No user counts as more than one signatory on the same request. PRD 8.4
  -- requires this explicitly and it is the control the whole feature exists
  -- for, so it is a unique index rather than a check in a handler.
  --
  -- KNOWN GAP — THIS DOES NOT YET ENFORCE THE RULE AS STATED. The rule is per
  -- USER; this index is per MEMBER. One person who is two member records in a
  -- society (a duplicate created during migration — exactly what the import's
  -- duplicate detection exists to catch) can sign twice and satisfy a 2-of-3
  -- on their own. There is no users table yet, so there is nothing to key on.
  -- 0002 adds users with auth and moves this key to (request_id, user_id).
  -- Until then this index is a partial control, not the PRD 8.4 control.
  UNIQUE (request_id, member_id)
);

COMMIT;
