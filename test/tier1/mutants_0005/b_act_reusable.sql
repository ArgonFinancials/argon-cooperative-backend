-- DELIBERATELY WRONG. Everything about the authorising act (its kind, its
-- actor, its transaction) except that it authorises ONE batch.
--
-- The claim: "one act, one proof" covers the batch side too. Without it, a
-- single PIN entry posts as many batches as the transaction holds.
--
-- Must turn red: TestProof9_OneActAuthorisesOneBatch.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
DECLARE
  k TEXT; who UUID; x xid8;
BEGIN
  SELECT kind, actor_user_id, act_xid INTO k, who, x FROM credential_acts WHERE id = NEW.authorised_by;
  IF NEW.authorised_by IS NULL OR k <> 'ledger_batch' OR who <> NEW.actor_user_id OR x <> pg_current_xact_id() THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_needs_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
