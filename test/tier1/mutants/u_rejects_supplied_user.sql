-- DELIBERATELY WRONG (over-strict). Refuses any caller-supplied user_id, even a correct one.
CREATE FUNCTION mu() RETURNS trigger AS $$ BEGIN
  IF NEW.user_id IS NOT NULL THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_user_is_member_user'; END IF;
  NEW.user_id := (SELECT user_id FROM members WHERE id = NEW.member_id);
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mu BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION mu();
ALTER TABLE signatory_approvals ALTER user_id SET NOT NULL;
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_approvals_one_per_user UNIQUE (request_id, user_id);
