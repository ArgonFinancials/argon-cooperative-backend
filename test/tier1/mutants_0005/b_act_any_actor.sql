-- DELIBERATELY WRONG. A batch needs a ledger_batch act — anybody's.
--
-- The claim: attribution is the point of the step-up. One secretary's PIN must
-- not stand behind another person's posting.
--
-- Must turn red: TestProof7_BatchActIsTheActorsOwn.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
BEGIN
  IF NEW.authorised_by IS NULL OR (SELECT kind FROM credential_acts WHERE id = NEW.authorised_by) <> 'ledger_batch' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_needs_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
