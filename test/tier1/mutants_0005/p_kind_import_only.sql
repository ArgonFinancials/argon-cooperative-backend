-- DELIBERATELY WRONG. An IMPORT needs an import proof; other acts take any proof.
--
-- The claim: this is the plausible partial fix, scoping added for the new act
-- and never retrofitted to the old ones. A proof minted for an import then
-- still changes a PIN. In the user's words, "a proof minted for an import must
-- not satisfy a reversal": the binding runs both ways.
--
-- Must turn red: TestProof3_ProofForAnImportDoesNotAuthoriseAPinChange.
CREATE FUNCTION m_act() RETURNS trigger AS $$
BEGIN
  IF NEW.kind = 'ledger_batch' AND (SELECT act_kind FROM auth_proofs WHERE id = NEW.proof_id) IS DISTINCT FROM 'ledger_batch' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_proof_for_this_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_act BEFORE INSERT ON credential_acts FOR EACH ROW EXECUTE FUNCTION m_act();
