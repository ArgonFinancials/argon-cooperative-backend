-- DELIBERATELY WRONG. Any current officer of the society may approve a
-- signatory reset, signing post or not.
--
-- The claim: this is D-15 walked around. An administrator (here, a loan
-- officer holding no signing post) cannot reset a signatory directly, but
-- could approve one. "Two other SIGNATORIES" means holders of a signing post,
-- the same people whose agreement releases money.
--
-- Must turn red: TestAuthD4_ApproverMustBeASignatory.
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

  IF NEW.actor_user_id = rq.target_user_id THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_approver_not_target';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
                  WHERE am.user_id = NEW.actor_user_id AND a.society_id = soc
                    -- (missing: AND a.is_signatory)
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
