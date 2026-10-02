-- DELIBERATELY WRONG. A proof's act kind must be a known one — IF it names one.
--
-- The claim: "bound at mint" needs the binding to be REQUIRED. A proof minted
-- for nothing in particular slips past a check that only validates what is
-- there, and an unscoped proof is exactly the one that satisfies any act.
--
-- Must turn red: TestProof1_PinProofNamesItsActAtMint.
CREATE FUNCTION m_proof() RETURNS trigger AS $$
BEGIN
  IF NEW.act_kind IS NOT NULL AND NEW.act_kind NOT IN ('pin_change', 'member_reset', 'phone_change', 'lock_release',
     'signatory_reset_request', 'signatory_phone_request', 'signatory_approval', 'ledger_batch') THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'proof_act_kind_known';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_proof BEFORE INSERT ON auth_proofs FOR EACH ROW EXECUTE FUNCTION m_proof();
