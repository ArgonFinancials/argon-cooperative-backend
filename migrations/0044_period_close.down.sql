BEGIN;
ALTER FUNCTION ledger_entry_not_in_closed_period() SECURITY INVOKER RESET search_path;
DROP TRIGGER IF EXISTS ledger_entries_outside_closed_period ON ledger_entries;
DROP FUNCTION IF EXISTS entry_outside_closed_period();
DROP TABLE IF EXISTS period_close_decisions;
DROP FUNCTION IF EXISTS period_close_decision_admissible();
DROP TABLE IF EXISTS period_closes;
DROP FUNCTION IF EXISTS period_close_admissible();
DROP FUNCTION IF EXISTS society_closed_through(UUID);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision', 'entry_correction', 'entry_correction_decision'));
COMMIT;
