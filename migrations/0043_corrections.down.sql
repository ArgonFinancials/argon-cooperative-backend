BEGIN;
DROP TABLE IF EXISTS entry_correction_decisions;
DROP FUNCTION IF EXISTS entry_correction_decision_admissible();
DROP FUNCTION IF EXISTS entry_correction_post(UUID, UUID, UUID);
DROP TABLE IF EXISTS entry_corrections;
DROP FUNCTION IF EXISTS entry_correction_admissible();
DROP FUNCTION IF EXISTS entry_names_user(UUID, UUID);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision'));
COMMIT;
