-- DELIBERATELY WRONG. A PIN proof never goes stale.
--
-- The claim: without a freshness limit, "the actor's own credential" means
-- "the actor entered their PIN at some point". An officer who signed in at
-- 08:00 and left the terminal unlocked has authorised whatever is done
-- there at 16:00.
--
-- Must turn red: TestAuthB3_StaleProofRefused.
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
     OR pr.consumed_at IS NOT NULL THEN                    -- unused
    -- (missing: pr.at >= auth_now() - interval '5 minutes')
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_needs_pin_proof';
  END IF;
  UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = pr.id;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_proof BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_proof();
