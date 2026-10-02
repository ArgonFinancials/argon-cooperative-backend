-- Reverses 0005 only while nothing rests on it: once a batch names its act,
-- or an act of the new kind exists, the step-up is part of how history is read.
-- When it refuses, nothing changes, but migrate has already recorded version 4,
-- dirty. The schema is still at 5: recover with `migrate force 5`
-- (deploy/README.md, "migrate says dirty").
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_batches WHERE authorised_by IS NOT NULL)
     OR EXISTS (SELECT 1 FROM credential_acts WHERE kind = 'ledger_batch') THEN
    RAISE EXCEPTION '0005 down: refusing — batches rest on step-up acts';
  END IF;
END $$;
DROP TRIGGER ledger_batches_authorised ON ledger_batches;
DROP FUNCTION ledger_batch_authorised();
ALTER TABLE ledger_batches DROP COLUMN authorised_by;
DROP TRIGGER credential_acts_proof_scoped ON credential_acts;
DROP FUNCTION credential_act_proof_scoped();
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval'));
DROP TRIGGER auth_proofs_scoped ON auth_proofs;
DROP FUNCTION auth_proofs_scoped();
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs DROP COLUMN mint_xid;
ALTER TABLE auth_proofs DROP COLUMN act_kind;
COMMIT;
