-- DELIBERATELY WRONG. An officer may release a lock — their own included.
--
-- The claim: officers are members too, and get locked out like anyone. If an
-- officer can release their own lock, the officer tier means nothing for
-- the accounts that matter most: whoever is guessing an officer's PIN (or
-- the officer who forgot it) just releases and carries on. Same shape as
-- addition 5: the officer path must never act on the officer.
--
-- Must turn red: TestAuthG7_OfficerCannotReleaseOwnLock.
CREATE FUNCTION m_release() RETURNS trigger AS $$
DECLARE
  target uuid := coalesce(NEW.target_user_id, (SELECT user_id FROM members WHERE id = NEW.target_member_id));
BEGIN
  IF NEW.kind <> 'lock_release' THEN
    RETURN NEW;
  END IF;
  IF NEW.reason IS NULL OR length(trim(NEW.reason)) < 20 THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_states_reason';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
                  WHERE am.user_id = NEW.actor_user_id AND a.post <> 'internal_auditor'
                    AND a.society_id IN (SELECT society_id FROM members WHERE user_id = target)
                    AND a.held_from <= auth_now()::date AND (a.held_until IS NULL OR a.held_until >= auth_now()::date)) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'reset_by_current_officer';
  END IF;
  -- (missing: target <> NEW.actor_user_id)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_release BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_release();
