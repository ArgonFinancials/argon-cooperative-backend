-- 0024 down: no enrolment codes, and no 'enrolment_issue' act kind (refused
-- while any such act exists: the kind lists below would reject it).
BEGIN;
DROP FUNCTION enrolment_redeem(UUID, TEXT);
DROP FUNCTION operator_issue_enrolment(UUID, TEXT, TEXT, TEXT, TEXT);
DROP TABLE enrolment_codes;
DROP FUNCTION enrolment_code_append_only();
DROP FUNCTION enrolment_code_admissible();
DROP TRIGGER credential_acts_enrolment_issue ON credential_acts;
DROP FUNCTION enrolment_issue_admissible();
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request'));
COMMIT;
