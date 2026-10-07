-- 0010's INTERFACE, with NO RULES: "M0" for suite 0010, the step-up behind a
-- signature and a rejection (decided 2026-10-07: "a signature is the most
-- attributable act in the product"). Applied on top of the real 0001–0009.
--
--   credential_acts.kind (and auth_proofs.act_kind) + approval_signature, approval_rejection
--   signatory_approvals.act_id   the signer's own step-up act, in this transaction
--   approval_rejections.act_id   the rejecter's own step-up act, in this transaction
BEGIN;
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));
ALTER TABLE signatory_approvals ADD COLUMN act_id UUID REFERENCES credential_acts(id);
ALTER TABLE approval_rejections ADD COLUMN act_id UUID REFERENCES credential_acts(id);
COMMIT;
