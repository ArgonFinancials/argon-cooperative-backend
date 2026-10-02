-- 0005's INTERFACE, with NO RULES: "M0" for suite 0005, the maximally broken
-- step-up. The columns and the new act kind exist so the tests can be written
-- and run; nothing refuses anything. Applied on top of the real 0001–0004.
--
--   auth_proofs.act_kind    the act a proof is minted FOR (bound at mint)
--   auth_proofs.mint_xid    the transaction that minted it
--   credential_acts.kind    gains 'ledger_batch'
--   ledger_batches.authorised_by  the act that authorised the batch
BEGIN;
ALTER TABLE auth_proofs ADD COLUMN act_kind TEXT;
ALTER TABLE auth_proofs ADD COLUMN mint_xid xid8 NOT NULL DEFAULT pg_current_xact_id();
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset', 'phone_change',
  'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval', 'ledger_batch'));
ALTER TABLE ledger_batches ADD COLUMN authorised_by UUID REFERENCES credential_acts(id);
COMMIT;
