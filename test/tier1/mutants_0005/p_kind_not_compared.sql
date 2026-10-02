-- DELIBERATELY WRONG. An act needs a proof that is SCOPED — to anything.
--
-- The claim: the scope is decorative unless it is compared with the act. This
-- checks that the proof carries a kind, and never which one.
--
-- Must turn red: both TestProof3 tests.
CREATE FUNCTION m_act() RETURNS trigger AS $$
BEGIN
  IF (SELECT act_kind FROM auth_proofs WHERE id = NEW.proof_id) IS NULL THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_proof_for_this_act';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_act BEFORE INSERT ON credential_acts FOR EACH ROW EXECUTE FUNCTION m_act();
