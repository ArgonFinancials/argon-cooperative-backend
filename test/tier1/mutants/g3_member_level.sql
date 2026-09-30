-- DELIBERATELY WRONG. Checks the auditor's MEMBER RECORD, not the person.
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id, members sm
             WHERE sm.id = NEW.member_id AND a.society_id = sm.society_id AND a.post = 'internal_auditor'
               AND (a.member_id = sm.id)
               AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_auditor'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m();
