-- DELIBERATELY WRONG. Any current appointment in the member's society counts
-- as "an officer", the internal auditor's included.
--
-- The claim: the auditor holds a post, so an "any post" check admits them.
-- A reset is a write, and the auditor is read-only by Directorate rule. The
-- REG lesson again: the label "officer" is not the reason.
--
-- Must turn red: TestAuthC3_ResetByAuditorRefused.
CREATE FUNCTION m_reset() RETURNS trigger AS $$
DECLARE
  target uuid := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));
BEGIN
  IF NEW.kind <> 'member_reset' THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS (
       SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
        WHERE am.user_id = NEW.actor_user_id
          AND a.society_id IN (SELECT society_id FROM members WHERE user_id = target)
          AND a.held_from <= auth_now()::date
          AND (a.held_until IS NULL OR a.held_until >= auth_now()::date)
          -- (missing: AND a.post <> 'internal_auditor')
     ) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_by_current_officer';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_reset BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_reset();
