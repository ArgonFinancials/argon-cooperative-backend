-- DELIBERATELY WRONG. "Minted before it is used", checked by TIME instead of by
-- transaction: the proof's time must not be after the act's.
--
-- The claim: inside one transaction the proof and the act have the SAME time
-- (auth_now() is the transaction's start), so "not after" always holds. It is
-- a check that reads like the rule and can never fire.
--
-- Must turn red: TestProof4_ProofCannotBeMintedAndUsedInOneTransaction.
CREATE FUNCTION m_act() RETURNS trigger AS $$
BEGIN
  IF (SELECT at FROM auth_proofs WHERE id = NEW.proof_id) > NEW.at THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'proof_minted_elsewhere';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_act BEFORE INSERT ON credential_acts FOR EACH ROW EXECUTE FUNCTION m_act();
