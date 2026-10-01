-- DELIBERATELY WRONG. "The actor is a current officer" — of ANY society.
--
-- The claim: holding a post is not holding a post over THIS member. A
-- secretary of one society could reset members of every other society on
-- the platform. The check has to ask where the post is held.
--
-- Must turn red: TestAuthC5_OfficerOfAnotherSocietyRefused.
CREATE FUNCTION m_reset() RETURNS trigger AS $$
BEGIN
  IF NEW.kind <> 'member_reset' THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS (
       SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
        WHERE am.user_id = NEW.actor_user_id
          AND a.post <> 'internal_auditor'
          AND a.held_from <= auth_now()::date
          AND (a.held_until IS NULL OR a.held_until >= auth_now()::date)
          -- (missing: AND a.society_id is a society the target belongs to)
     ) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_by_current_officer';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_reset BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_reset();
