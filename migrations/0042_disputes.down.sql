BEGIN;
DROP FUNCTION IF EXISTS dispute_state(UUID);
DROP VIEW IF EXISTS dispute_states;
DROP TRIGGER IF EXISTS dispute_correction_decisions_admissible ON dispute_correction_decisions;
DROP TABLE IF EXISTS dispute_correction_decisions;
DROP FUNCTION IF EXISTS dispute_decision_admissible();
DROP FUNCTION IF EXISTS dispute_post_reversal(UUID, UUID, UUID);
DROP TABLE IF EXISTS dispute_corrections;
DROP FUNCTION IF EXISTS dispute_correction_admissible();
DROP FUNCTION IF EXISTS dispute_correctable(UUID);
DROP TABLE IF EXISTS dispute_replies;
DROP FUNCTION IF EXISTS dispute_reply_admissible();
DROP TABLE IF EXISTS dispute_answers;
DROP FUNCTION IF EXISTS dispute_answer_admissible();
DROP TABLE IF EXISTS entry_disputes;
DROP FUNCTION IF EXISTS entry_dispute_admissible();
DROP FUNCTION IF EXISTS dispute_officer(UUID, UUID);
DROP TRIGGER IF EXISTS ledger_entries_reversal_mirrors ON ledger_entries;
DROP FUNCTION IF EXISTS reversal_mirrors();
DROP INDEX IF EXISTS ledger_entry_reversed_once;
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation'));
COMMIT;
