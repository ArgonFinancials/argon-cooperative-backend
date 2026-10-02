-- DELIBERATELY WRONG. A PIN proof must name an act kind — any text will do.
--
-- The claim: a scope nobody can match is not a scope. A mistyped kind is a
-- proof that authorises nothing today and, if a kind of that name is ever
-- added, something tomorrow.
--
-- Must turn red: TestProof2_ActKindMustBeKnown.
CREATE FUNCTION m_proof() RETURNS trigger AS $$
BEGIN
  IF NEW.method = 'pin' AND NEW.act_kind IS NULL THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'pin_proof_names_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_proof BEFORE INSERT ON auth_proofs FOR EACH ROW EXECUTE FUNCTION m_proof();
