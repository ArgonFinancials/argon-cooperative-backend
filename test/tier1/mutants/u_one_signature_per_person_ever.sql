-- DELIBERATELY WRONG (over-strict). One signature per person, ever.
CREATE FUNCTION mu() RETURNS trigger AS $$ DECLARE mu uuid; BEGIN
  SELECT user_id INTO mu FROM members WHERE id = NEW.member_id;
  IF NEW.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM mu THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_user_is_member_user'; END IF;
  NEW.user_id := mu;
  RETURN NEW; END $$ LANGUAGE plpgsql;
CREATE TRIGGER mu BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION mu();
ALTER TABLE signatory_approvals ALTER user_id SET NOT NULL;
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_approvals_one_per_user UNIQUE (user_id);
