-- Removes the loan book. Every loan, schedule, guarantee and the ledger's
-- links to them are LOST, and it refuses while guarantee acts exist (the act
-- kinds cannot be narrowed under them): take a backup first (40-migrate.sh
-- does).
BEGIN;
DROP TRIGGER ledger_lines_loan_principal ON ledger_lines;
DROP TRIGGER ledger_lines_loan ON ledger_lines;
DROP TRIGGER ledger_entries_d_loan ON ledger_entries;
DROP INDEX loan_disbursed_once;
ALTER TABLE ledger_entries DROP COLUMN loan_id;
DROP TRIGGER signatory_approvals_not_guarantor ON signatory_approvals;
DROP TRIGGER approval_requests_loan ON approval_requests;
ALTER TABLE approval_requests DROP COLUMN loan_id;
DROP TABLE loan_guarantee_withdrawals;
DROP TABLE loan_guarantees;
DROP TABLE loan_instalments;
DROP TABLE loans;
DROP TABLE loan_products;
DROP FUNCTION loan_disbursement_is_principal();
DROP FUNCTION ledger_line_loan_admissible();
DROP FUNCTION ledger_entry_loan_admissible();
DROP FUNCTION signature_not_guarantor();
DROP FUNCTION loan_request_admissible();
DROP FUNCTION loan_guarantees_immutable();
DROP FUNCTION loan_guarantee_withdrawal_admissible();
DROP FUNCTION loan_guarantee_admissible();
DROP FUNCTION loan_current_guarantees(UUID);
DROP FUNCTION loan_signed_by(UUID, UUID);
DROP FUNCTION loan_schedule_whole();
DROP FUNCTION loan_schedule_immutable();
DROP FUNCTION loan_schedule_closed();
DROP FUNCTION loan_agreement_immutable();
DROP FUNCTION loan_admissible();
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release',
  'signatory_reset_request', 'signatory_phone_request', 'signatory_approval'));
COMMIT;
