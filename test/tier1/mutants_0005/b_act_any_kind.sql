-- DELIBERATELY WRONG. A batch needs an authorising act — of any kind.
--
-- The claim: without the kind, any attributable act the actor made in the
-- transaction (a PIN change, say) authorises a posting it was never entered for.
--
-- Must turn red: TestProof6_BatchActIsALedgerBatchAct.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
BEGIN
  IF NEW.authorised_by IS NULL THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_needs_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
