-- DELIBERATELY WRONG. A batch needs the actor's own ledger_batch act — from any
-- transaction.
--
-- The claim: an act committed on its own is a stored authorisation, waiting to
-- be spent later by whatever posts next. The act and the batch are one event.
--
-- Must turn red: TestProof8_BatchActIsFromThisTransaction.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
DECLARE
  k TEXT; who UUID;
BEGIN
  SELECT kind, actor_user_id INTO k, who FROM credential_acts WHERE id = NEW.authorised_by;
  IF NEW.authorised_by IS NULL OR k <> 'ledger_batch' OR who <> NEW.actor_user_id THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_needs_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
