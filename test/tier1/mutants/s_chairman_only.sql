-- DELIBERATELY WRONG (over-strict). Only the chairman may sign.
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF NEW.signed_at IS DISTINCT FROM now() THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_approvals_signed_at_is_now'; END IF;
  IF NOT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id, members sm, approval_requests r
     WHERE sm.id = NEW.member_id AND r.id = NEW.request_id AND a.society_id = r.society_id
       AND (a.member_id = sm.id OR am.user_id = sm.user_id)
       AND a.is_signatory AND a.post = 'chairman'
       AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_holds_signing_post'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m();
