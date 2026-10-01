-- DELIBERATELY WRONG. Any fresh, unused proof BY THE ACTOR authorises an
-- attributable act, including a code sent to their phone.
--
-- The claim: this checks everything except the one thing addition 1 is
-- about. A code proves someone holds the phone, not who they are. Accept one
-- here, and whoever holds an officer's phone can reset members and redirect
-- their codes.
--
-- Must turn red: TestAuthB1_CodeProofCannotAuthoriseAReset,
--                TestAuthB1_CodeProofCannotAuthoriseAPhoneChange.
CREATE FUNCTION m_proof() RETURNS trigger AS $$
DECLARE
  pr auth_proofs%ROWTYPE;
BEGIN
  IF NEW.kind NOT IN ('member_reset', 'phone_change') THEN
    RETURN NEW;
  END IF;
  SELECT * INTO pr FROM auth_proofs WHERE id = NEW.proof_id;
  IF NOT FOUND
     OR pr.user_id <> NEW.actor_user_id                    -- the actor's own
     OR pr.at < auth_now() - interval '5 minutes'          -- fresh
     OR pr.consumed_at IS NOT NULL THEN                    -- unused
    -- (missing: pr.method = 'pin')
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'act_needs_pin_proof';
  END IF;
  UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = pr.id;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_proof BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_proof();
