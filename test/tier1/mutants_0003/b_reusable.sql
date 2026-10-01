-- DELIBERATELY WRONG. A fresh PIN proof authorises any number of acts within
-- its five minutes.
--
-- The claim: one PIN entry, then a run of resets. Each attributable act
-- should cost its own entry of the actor's credential, so that each one is a
-- decision rather than the tail of an earlier one.
--
-- Must turn red: TestAuthB4_ProofIsSingleUse.
CREATE FUNCTION m_proof() RETURNS trigger AS $$
DECLARE
  pr auth_proofs%ROWTYPE;
BEGIN
  IF NEW.kind NOT IN ('member_reset', 'phone_change') THEN
    RETURN NEW;
  END IF;
  SELECT * INTO pr FROM auth_proofs WHERE id = NEW.proof_id;
  IF NOT FOUND
     OR pr.method <> 'pin'                                 -- a PIN, not a code
     OR pr.user_id <> NEW.actor_user_id                    -- the actor's own
     OR pr.at < auth_now() - interval '5 minutes' THEN     -- fresh
    -- (missing: pr.consumed_at IS NULL, and marking it consumed)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_needs_pin_proof';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_proof BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_proof();
