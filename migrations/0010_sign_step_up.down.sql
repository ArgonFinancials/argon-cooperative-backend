-- Reverses 0010 only while nothing rests on it: once a signature or a
-- rejection names its act, or an act of the new kinds exists, the step-up is
-- part of how history is read. When it refuses, nothing changes, but migrate
-- has already recorded version 9, dirty. The schema is still at 10: recover
-- with `migrate force 10` (deploy/README.md, "migrate says dirty").
--
-- grants.sql names act_id: after going down, apply the grants.sql of the
-- commit before 0010, or 40-migrate.sh's grants step fails on the missing
-- column.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM signatory_approvals WHERE act_id IS NOT NULL)
     OR EXISTS (SELECT 1 FROM approval_rejections WHERE act_id IS NOT NULL)
     OR EXISTS (SELECT 1 FROM credential_acts WHERE kind IN ('approval_signature', 'approval_rejection'))
     OR EXISTS (SELECT 1 FROM auth_proofs WHERE act_kind IN ('approval_signature', 'approval_rejection')) THEN
    RAISE EXCEPTION '0010 down: refusing — signatures or rejections rest on step-up acts';
  END IF;
END $$;
DROP TRIGGER approval_rejections_step_up ON approval_rejections;
DROP FUNCTION rejection_on_step_up();
ALTER TABLE approval_rejections DROP COLUMN act_id;
DROP TRIGGER signatory_approvals_step_up ON signatory_approvals;
DROP FUNCTION signature_on_step_up();
ALTER TABLE signatory_approvals DROP COLUMN act_id;
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal'));
COMMIT;
