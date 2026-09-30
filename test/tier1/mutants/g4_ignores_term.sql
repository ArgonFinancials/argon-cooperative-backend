-- DELIBERATELY WRONG (over-strict). Ignores terms: a past auditor can never hold a signing post.
CREATE FUNCTION m() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM appointments o JOIN members om ON om.id = o.member_id, members nm
             WHERE nm.id = NEW.member_id AND o.society_id = NEW.society_id AND o.id <> NEW.id
               AND (o.member_id = NEW.member_id OR om.user_id = nm.user_id)
               AND ((NEW.post = 'internal_auditor' AND o.is_signatory) OR (NEW.is_signatory AND o.post = 'internal_auditor')))
  THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'auditor_holds_no_signing_post'; END IF;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER m BEFORE INSERT OR UPDATE ON appointments FOR EACH ROW EXECUTE FUNCTION m();
