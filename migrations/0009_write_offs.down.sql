-- Removes write-offs. Every write-off request's reason, every write-off and
-- recovery link is LOST; take a backup first (40-migrate.sh does). It refuses
-- while write-off or recovery entries, or loan-loss/recovery accounts, exist.
--
-- NOT UNDONE: the movement_kind values 'loan_write_off' and 'loan_recovery'.
-- PostgreSQL cannot drop an enum value; they remain, unused, after going down.
BEGIN;
DROP TRIGGER ledger_lines_write_off_outstanding ON ledger_lines;
DROP FUNCTION write_off_is_outstanding();
DROP TRIGGER signatory_approvals_not_borrower ON signatory_approvals;
DROP FUNCTION signature_not_borrower();

-- 0008's rules, restored verbatim.
CREATE OR REPLACE FUNCTION loan_request_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l loans%ROWTYPE;
BEGIN
  IF NEW.loan_id IS NULL THEN
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
  IF NEW.kind NOT IN ('loan_disbursement', 'loan_repayment') THEN
    IF NEW.loan_id IS NOT NULL THEN
      RAISE EXCEPTION 'only a loan disbursement or repayment names a loan, not %', NEW.kind
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_id_only_on_loan_movements';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.loan_id IS NULL THEN
    RAISE EXCEPTION 'a % names the loan it moves money for', NEW.kind
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan';
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;   -- serialise with a withdrawal
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
    -- Re-checked here, not only at the request (decided 2026-10-05).
    IF loan_current_guarantees(l.id) < l.guarantors_required THEN
      RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
    END IF;
  ELSE
    IF NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind = 'loan_disbursement') THEN
      RAISE EXCEPTION 'loan % was never disbursed; there is nothing to repay', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_repaid_after_disbursement';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION ledger_line_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e ledger_entries%ROWTYPE;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.loan_id IS NOT NULL AND (SELECT role FROM accounts WHERE id = NEW.account_id) = 'member_loan'
     AND NEW.member_id IS DISTINCT FROM (SELECT member_id FROM loans WHERE id = e.loan_id) THEN
    RAISE EXCEPTION 'loan % is the borrower''s: its loan lines name the borrower', e.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_line_is_borrowers';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP FUNCTION loan_owed(UUID);
ALTER TABLE approval_requests DROP COLUMN reason;
ALTER TABLE societies DROP COLUMN write_off_signatures_required;
ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general'));
COMMIT;
