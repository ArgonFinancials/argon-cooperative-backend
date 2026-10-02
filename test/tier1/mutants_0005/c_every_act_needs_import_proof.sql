-- DELIBERATELY WRONG (over-strict). Every act's proof must be an import proof.
--
-- The claim: this breaks the EXISTING acts. Scoping is per act kind. A check
-- written with the new act in mind and applied to all of them stops a member
-- from changing their own PIN with a proof minted for exactly that.
--
-- Must turn red: TestProof_Control_ExistingActWithItsScopedProof.
CREATE FUNCTION m_act() RETURNS trigger AS $$
BEGIN
  IF (SELECT act_kind FROM auth_proofs WHERE id = NEW.proof_id) IS DISTINCT FROM 'ledger_batch' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_proof_for_this_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_act BEFORE INSERT ON credential_acts FOR EACH ROW EXECUTE FUNCTION m_act();
