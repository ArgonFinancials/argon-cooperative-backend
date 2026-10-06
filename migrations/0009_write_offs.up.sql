-- =============================================================================
-- 0009 — WRITE-OFFS (LOAN_SHAPE.md §6a, approved 2026-10-06)
--
-- A write-off does not make a debt disappear: it records where the loss went,
-- who decided, and why. The loan stays exactly as agreed.
--
--   - The quorum is the SOCIETY'S (its bye-laws), with no default. Unset, a
--     write-off is refused in words that say so.
--   - The quorum is COPIED onto the request when it is raised, as
--     approval_requests.required, and never re-read: the point-in-time rule
--     of the officer's post at posting. Reading the society's current setting
--     at approval time looks simpler; it would let a bye-law change re-judge
--     a decision already taken.
--   - Any disbursed loan with money owed; a reason in words; neither the
--     borrower nor a guarantor signs. The borrower never signs ANY request
--     for their own loan, the disbursement included (finding 19).
--   - The entry is exactly what is owed AND what was approved, once, to a
--     loss account. Afterwards no repayment; money recovered is its own
--     movement and never credits the loan.
--
-- New enum values cannot be used by anything created in the transaction that
-- adds them, so the rules compare kind::text and "once" is a rule under the
-- loan row's lock rather than a partial unique index.
--
-- Tests: test/tier1/tier1_0009_test.go (suite 0009).
-- =============================================================================
BEGIN;

-- IF NOT EXISTS: a down cannot drop these values (finding 21), so a re-apply
-- after a rollback must find them already there and carry on.
ALTER TYPE movement_kind ADD VALUE IF NOT EXISTS 'loan_write_off';
ALTER TYPE movement_kind ADD VALUE IF NOT EXISTS 'loan_recovery';

ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general', 'loan_loss', 'loan_recovery_income'));

-- The bye-laws' number. NULL means the society has not decided; there is no default.
ALTER TABLE societies ADD COLUMN write_off_signatures_required SMALLINT
  CONSTRAINT write_off_quorum_sane CHECK (write_off_signatures_required BETWEEN 1 AND 9);
ALTER TABLE approval_requests ADD COLUMN reason TEXT;

-- What a loan's ledger says is owed, every entry counted.
CREATE FUNCTION loan_owed(p_loan UUID) RETURNS BIGINT
SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT coalesce(sum(l.amount_kobo), 0)::bigint
    FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
   WHERE e.loan_id = p_loan AND a.role = 'member_loan';
$$ LANGUAGE sql STABLE;

-- ---- the request ------------------------------------------------------------------
CREATE OR REPLACE FUNCTION loan_request_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l       loans%ROWTYPE;
  quorum  SMALLINT;
  owed    BIGINT;
BEGIN
  IF NEW.loan_id IS NULL THEN
    IF NEW.kind::text = 'loan_write_off' THEN
      RAISE EXCEPTION 'a write-off request names the loan it writes off'
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_needs_money_owed';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;

  IF NEW.kind::text = 'loan_write_off' THEN
    IF NEW.member_id IS DISTINCT FROM l.member_id OR NEW.society_id <> l.society_id THEN
      RAISE EXCEPTION 'a write-off request is for the borrower''s loan, in its society'
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_request_matches_loan';
    END IF;
    quorum := (SELECT write_off_signatures_required FROM societies WHERE id = NEW.society_id);
    IF quorum IS NULL THEN
      RAISE EXCEPTION 'this society has not decided how many signatures a write-off needs: set it from the bye-laws (societies.write_off_signatures_required) before raising one'
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_quorum_unset';
    END IF;
    -- COPIED here, at raise time, and never re-read: approval_status counts
    -- against NEW.required, so a later change to the setting cannot re-judge
    -- this request (the officer's-post-at-posting rule).
    IF NEW.required <> quorum THEN
      RAISE EXCEPTION 'this society''s bye-laws require % signatures for a write-off, not %', quorum, NEW.required
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_quorum_is_societys';
    END IF;
    owed := loan_owed(l.id);
    IF owed <= 0 OR NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind = 'loan_disbursement') THEN
      RAISE EXCEPTION 'loan % has nothing owed to write off', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_needs_money_owed';
    END IF;
    IF NEW.amount_kobo <> owed THEN
      RAISE EXCEPTION 'a write-off request is for what is owed (% kobo), not % kobo', owed, NEW.amount_kobo
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_request_is_outstanding';
    END IF;
    IF length(trim(coalesce(NEW.reason, ''))) = 0 THEN
      RAISE EXCEPTION 'a write-off is recorded with the reason the society gives for it'
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_states_reason';
    END IF;
    RETURN NEW;
  END IF;

  -- The disbursement request (0008, unchanged).
  IF NEW.kind <> 'loan_disbursement' OR NEW.amount_kobo <> l.principal_kobo
     OR NEW.member_id IS DISTINCT FROM l.member_id OR NEW.society_id <> l.society_id THEN
    RAISE EXCEPTION 'a loan''s request is its disbursement: the principal, to the borrower, in its society'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_request_matches_loan';
  END IF;
  IF loan_current_guarantees(l.id) < l.guarantors_required THEN
    RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- ---- who signs: never the borrower, on any request for their own loan (finding 19) ---
CREATE FUNCTION signature_not_borrower() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loans l ON l.id = r.loan_id JOIN members m ON m.id = l.member_id
              WHERE r.id = NEW.request_id AND m.user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN
    RAISE EXCEPTION 'a borrower never signs a request for their own loan'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'borrower_never_signs_own_loan';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_not_borrower BEFORE INSERT ON signatory_approvals
  FOR EACH ROW EXECUTE FUNCTION signature_not_borrower();
-- (A guarantor never signs: 0008's signatory_not_guarantor already covers every
-- request naming the loan, write-offs included. One rule, not two copies.)

-- ---- the entries -------------------------------------------------------------------
CREATE OR REPLACE FUNCTION ledger_entry_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l          loans%ROWTYPE;
  k          TEXT := NEW.kind::text;
  written    BOOLEAN;
BEGIN
  IF k NOT IN ('loan_disbursement', 'loan_repayment', 'loan_write_off', 'loan_recovery') THEN
    IF NEW.loan_id IS NOT NULL THEN
      RAISE EXCEPTION 'only a loan movement names a loan, not %', k
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_id_only_on_loan_movements';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.loan_id IS NULL THEN
    RAISE EXCEPTION 'a % names the loan it moves money for', k
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan';
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;   -- serialise every movement of one loan
  IF l.society_id IS DISTINCT FROM NEW.society_id THEN
    RAISE EXCEPTION 'loan % is not this society''s', NEW.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_same_society';
  END IF;
  written := EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind::text = 'loan_write_off');

  IF k = 'loan_disbursement' THEN
    IF NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = l.id AND r.kind = 'loan_disbursement'
                     AND approval_status(r.id) = 'approved') THEN
      RAISE EXCEPTION 'loan % has no approved disbursement request', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved';
    END IF;
    IF loan_current_guarantees(l.id) < l.guarantors_required THEN
      RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
    END IF;
  ELSIF k = 'loan_repayment' THEN
    IF NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind = 'loan_disbursement') THEN
      RAISE EXCEPTION 'loan % was never disbursed; there is nothing to repay', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_repaid_after_disbursement';
    END IF;
    IF written THEN
      RAISE EXCEPTION 'loan % was written off; money received now is a recovery, its own movement', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_written_off';
    END IF;
  ELSIF k = 'loan_write_off' THEN
    IF written THEN
      RAISE EXCEPTION 'loan % is already written off', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_once';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = l.id AND r.kind::text = 'loan_write_off'
                     AND approval_status(r.id) = 'approved') THEN
      RAISE EXCEPTION 'loan % has no approved write-off', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_approved';
    END IF;
  ELSE   -- loan_recovery
    IF NOT written THEN
      RAISE EXCEPTION 'loan % was never written off; money received is a repayment', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'recovery_needs_write_off';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- ---- the lines ---------------------------------------------------------------------
CREATE OR REPLACE FUNCTION ledger_line_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e    ledger_entries%ROWTYPE;
  role TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.loan_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF role = 'member_loan' AND NEW.member_id IS DISTINCT FROM (SELECT member_id FROM loans WHERE id = e.loan_id) THEN
    RAISE EXCEPTION 'loan % is the borrower''s: its loan lines name the borrower', e.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_line_is_borrowers';
  END IF;
  IF e.kind::text = 'loan_write_off' AND NEW.amount_kobo > 0 AND role <> 'loan_loss' THEN
    RAISE EXCEPTION 'a write-off records its loss on a loan-loss account, not %', role
      USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_to_loss_account';
  END IF;
  IF e.kind::text = 'loan_recovery' AND role = 'member_loan' THEN
    RAISE EXCEPTION 'a recovery never credits the written-off loan: it would read as an overpayment'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'recovery_never_reopens_loan';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- At commit: a write-off clears exactly what is owed, and is exactly what was
-- approved. A repayment after the approval makes the approval describe
-- something else; the write-off is then refused, not posted at the old figure.
CREATE FUNCTION write_off_is_outstanding() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e        ledger_entries%ROWTYPE;
  credit   BIGINT;
  approved BIGINT;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind::text <> 'loan_write_off' THEN
    RETURN NULL;
  END IF;
  SELECT -coalesce(sum(l.amount_kobo), 0) INTO credit
    FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'member_loan';
  SELECT r.amount_kobo INTO approved FROM approval_requests r
   WHERE r.loan_id = e.loan_id AND r.kind::text = 'loan_write_off' AND approval_status(r.id) = 'approved'
   ORDER BY r.raised_at DESC LIMIT 1;
  IF loan_owed(e.loan_id) <> 0 OR credit IS DISTINCT FROM approved THEN
    RAISE EXCEPTION 'a write-off of loan % is exactly what is owed and what was approved (% kobo approved, % written off, % left owing)',
                    e.loan_id, approved, credit, loan_owed(e.loan_id)
      USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_is_outstanding';
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER ledger_lines_write_off_outstanding AFTER INSERT ON ledger_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION write_off_is_outstanding();

COMMIT;
