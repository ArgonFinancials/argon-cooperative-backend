-- DELIBERATELY WRONG. A fresh, unused PIN proof authorises an act, whoever
-- the proof belongs to.
--
-- The claim: "a PIN was just verified" is not "the ACTOR's PIN was just
-- verified". An officer standing beside a colleague who has just entered
-- their PIN acts on it as their own.
--
-- Must turn red: TestAuthB2_ActorMustUseTheirOwnProof.
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
     OR pr.at < auth_now() - interval '5 minutes'          -- fresh
     OR pr.consumed_at IS NOT NULL THEN                    -- unused
    -- (missing: pr.user_id = NEW.actor_user_id)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_needs_pin_proof';
  END IF;
  UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = pr.id;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_proof BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_proof();
