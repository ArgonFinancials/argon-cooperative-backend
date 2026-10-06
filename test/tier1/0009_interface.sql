-- 0009's INTERFACE, with NO RULES: "M0" for suite 0009, write-offs (LOAN_SHAPE.md
-- §6a, approved 2026-10-06). Applied on top of the real 0001–0008.
--
--   societies.write_off_signatures_required   the bye-laws' quorum; NULL = undecided
--   approval_requests.reason                  why a write-off is asked for
--   movement_kind  + loan_write_off, loan_recovery
--   account roles  + loan_loss (expense), loan_recovery_income (income)
--
-- 0008's rules are widened only as far as letting the new kinds exist:
--   - a loan_write_off request may name a loan (0008 allowed only the
--     disbursement), with no checks at all;
--   - a write-off or recovery entry may name a loan, with no checks at all;
--   - 0008's "a guarantor never signs" is NARROWED to disbursement requests.
--     The real 0008 already covers every request naming the loan; narrowing
--     it is the regression test 7 guards against.
BEGIN;
ALTER TYPE movement_kind ADD VALUE 'loan_write_off';
ALTER TYPE movement_kind ADD VALUE 'loan_recovery';
ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general', 'loan_loss', 'loan_recovery_income'));
ALTER TABLE societies ADD COLUMN write_off_signatures_required SMALLINT;
ALTER TABLE approval_requests ADD COLUMN reason TEXT;

CREATE OR REPLACE FUNCTION loan_request_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l loans%ROWTYPE;
BEGIN
  IF NEW.loan_id IS NULL OR NEW.kind::text = 'loan_write_off' THEN
    RETURN NEW;
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;
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

CREATE OR REPLACE FUNCTION ledger_entry_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l loans%ROWTYPE;
BEGIN
  IF NEW.kind::text IN ('loan_write_off', 'loan_recovery') THEN
    RETURN NEW;   -- M0: write-offs and recoveries go unchecked
  END IF;
  IF NEW.kind NOT IN ('loan_disbursement', 'loan_repayment') THEN
    IF NEW.loan_id IS NOT NULL THEN
      RAISE EXCEPTION 'only a loan movement names a loan, not %', NEW.kind
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_id_only_on_loan_movements';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.loan_id IS NULL THEN
    RAISE EXCEPTION 'a % names the loan it moves money for', NEW.kind
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan';
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;
  IF l.society_id IS DISTINCT FROM NEW.society_id THEN
    RAISE EXCEPTION 'loan % is not this society''s', NEW.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_same_society';
  END IF;
  IF NEW.kind = 'loan_disbursement' THEN
    IF NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = l.id AND r.kind = 'loan_disbursement'
                     AND approval_status(r.id) = 'approved') THEN
      RAISE EXCEPTION 'loan % has no approved disbursement request', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved';
    END IF;
    IF loan_current_guarantees(l.id) < l.guarantors_required THEN
      RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
    END IF;
  ELSIF NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind = 'loan_disbursement') THEN
    RAISE EXCEPTION 'loan % was never disbursed; there is nothing to repay', l.id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_repaid_after_disbursement';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION signature_not_guarantor() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loan_guarantees g ON g.loan_id = r.loan_id
              WHERE r.id = NEW.request_id AND r.kind = 'loan_disbursement'   -- M0: narrowed
                AND g.guarantor_user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN
    RAISE EXCEPTION 'a guarantor of a loan never signs it'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
COMMIT;
