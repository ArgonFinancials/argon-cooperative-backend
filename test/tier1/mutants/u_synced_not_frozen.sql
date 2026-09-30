-- DELIBERATELY WRONG. All correct except the signature's user is KEPT IN STEP with the
-- member record - resolved through the current link, not frozen at signing.
CREATE FUNCTION mu() RETURNS trigger AS $$ DECLARE mu uuid; BEGIN
  SELECT user_id INTO mu FROM members WHERE id = NEW.member_id;
  IF NEW.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM mu THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_user_is_member_user'; END IF;
  NEW.user_id := mu;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mu BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION mu();
ALTER TABLE signatory_approvals ALTER user_id SET NOT NULL;
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_approvals_one_per_user UNIQUE (request_id, user_id);
CREATE FUNCTION ms() RETURNS trigger AS $$ BEGIN
  UPDATE signatory_approvals SET user_id = NEW.user_id WHERE member_id = NEW.id;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER ms AFTER UPDATE OF user_id ON members FOR EACH ROW EXECUTE FUNCTION ms();
