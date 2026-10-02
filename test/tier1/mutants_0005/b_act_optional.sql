-- DELIBERATELY WRONG. A batch's authorising act must be a ledger_batch act —
-- IF the batch names one.
--
-- The claim: the step-up is only as strong as the requirement to take it. A
-- batch that names no act at all is the bypass.
--
-- Must turn red: TestProof5_BatchNeedsAnAuthorisingAct.
CREATE FUNCTION m_batch() RETURNS trigger AS $$
BEGIN
  IF NEW.authorised_by IS NOT NULL AND (SELECT kind FROM credential_acts WHERE id = NEW.authorised_by) <> 'ledger_batch' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'batch_act_is_ledger_batch';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_batch BEFORE INSERT ON ledger_batches FOR EACH ROW EXECUTE FUNCTION m_batch();
