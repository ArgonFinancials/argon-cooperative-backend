-- DELIBERATELY WRONG. The locked-out signatory may approve their own reset.
--
-- The claim: every approver check holds (a current signatory of the
-- society, one approval per person, inside the window, after the request),
-- except that the approver must be someone OTHER than the target. With it
-- missing, "two other signatories" becomes "the target plus one", and the
-- person whose seat is at stake is half the quorum that unlocks it.
--
-- Must turn red: TestAuthD2_TargetCannotApproveOwnReset.
CREATE FUNCTION m_approve() RETURNS trigger AS $$
DECLARE
  rq  credential_acts%ROWTYPE;
  soc uuid;
BEGIN
  IF NEW.kind <> 'signatory_approval' THEN
    RETURN NEW;
  END IF;
  SELECT * INTO rq FROM credential_acts WHERE id = NEW.request_id;
  soc := (SELECT society_id FROM members WHERE id = rq.target_member_id);

  -- (missing: NEW.actor_user_id <> rq.target_user_id)

  IF NOT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
                  WHERE am.user_id = NEW.actor_user_id AND a.society_id = soc AND a.is_signatory
                    AND a.held_from <= auth_now()::date AND (a.held_until IS NULL OR a.held_until >= auth_now()::date)) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_approver_is_signatory';
  END IF;
  IF EXISTS (SELECT 1 FROM credential_acts WHERE request_id = NEW.request_id AND kind = 'signatory_approval'
                AND actor_user_id = NEW.actor_user_id) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_one_approval_per_person';
  END IF;
  IF auth_now() > rq.at + interval '48 hours' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_request_expired';
  END IF;
  IF (SELECT at FROM auth_proofs WHERE id = NEW.proof_id) < rq.at THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'approval_after_request';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_approve BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_approve();
