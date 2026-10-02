-- DELIBERATELY WRONG (over-strict). The authorising act must come from a
-- DIFFERENT transaction than the batch: the proof rule, misapplied to the act.
--
-- The claim: this breaks the NORMAL path. The proof is minted elsewhere; the
-- act and the batch are one event and must commit together. If the control
-- passed here, it would prove only that a batch can be refused.
--
-- Must turn red: TestProof_Control_ScopedProofAuthorisesItsBatch.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
BEGIN
  IF NEW.authorised_by IS NULL OR (SELECT act_xid FROM credential_acts WHERE id = NEW.authorised_by) = pg_current_xact_id() THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_act_same_transaction';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
