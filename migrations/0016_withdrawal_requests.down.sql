-- Reverses 0016 only while nothing rests on it: once a request names who raised
-- it, dropping the column would erase the author of a request that moves money
-- (finding 41), and its raiser could then sign it. When it refuses, nothing
-- changes, but migrate has already recorded version 15, dirty. The schema is
-- still at 16: recover with `migrate force 16` (deploy/README.md, "migrate
-- says dirty").
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests WHERE raised_by_user_id IS NOT NULL OR act_id IS NOT NULL) THEN
    RAISE EXCEPTION '0016 down: refusing — withdrawal requests name their raisers; dropping them would erase who asked for money to move';
  END IF;
  IF EXISTS (SELECT 1 FROM credential_acts WHERE kind = 'withdrawal_request') THEN
    RAISE EXCEPTION '0016 down: refusing — withdrawal_request step-up acts exist; the act kind cannot be withdrawn under them';
  END IF;
END $$;
DROP FUNCTION operator_set_withdrawal_request_days(UUID, INT, TEXT, TEXT, TEXT, DATE, TEXT);
DROP TRIGGER signatory_approvals_raiser_never_signs ON signatory_approvals;
DROP FUNCTION signature_not_raiser();
DROP TRIGGER approval_requests_withdrawal_r6_reachable ON approval_requests;
DROP FUNCTION withdrawal_request_reachable();
DROP TRIGGER approval_requests_withdrawal_r5_withdrawable ON approval_requests;
DROP FUNCTION withdrawal_request_within_withdrawable();
DROP TRIGGER approval_requests_resubmission_same_member ON approval_requests;
DROP FUNCTION resubmission_same_member();
DROP TRIGGER approval_requests_withdrawal_r3_window ON approval_requests;
DROP FUNCTION withdrawal_request_window();
DROP TRIGGER approval_requests_withdrawal_r2_raiser ON approval_requests;
DROP FUNCTION withdrawal_request_raiser_admissible();
DROP INDEX approval_requests_act_once;
DROP TRIGGER approval_requests_withdrawal_r1_authored ON approval_requests;
DROP FUNCTION withdrawal_request_authored();
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation'));
ALTER TABLE societies DROP COLUMN withdrawal_request_days;
ALTER TABLE approval_requests DROP COLUMN act_id;
ALTER TABLE approval_requests DROP COLUMN raised_by_user_id;
COMMIT;
