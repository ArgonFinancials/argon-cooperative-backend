-- DELIBERATELY WRONG. An approval may rest on a PIN proof taken before the
-- request it approves existed.
--
-- The claim: this is the back-dating walk-around. The approver's proof is
-- fresh (within five minutes) and theirs, but it was taken before the
-- request was raised. So the approval records agreement to something that
-- did not yet exist when the approver entered their PIN: a proof collected
-- for one purpose, spent on another. Same shape as a supplied signed_at.
--
-- Must turn red: TestAuthD6_ApprovalCannotPredateTheRequest.
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

  -- (missing: the approver's proof was taken at or after rq.at)

  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_approve BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_approve();
