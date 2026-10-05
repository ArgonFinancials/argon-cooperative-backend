-- DELIBERATELY WRONG. The member record's signing post is checked; the person recorded with it is not.
--
-- Must turn red: TestApproval11_RejecterIsThePersonOfTheMemberRecord.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE r approval_requests%ROWTYPE; BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id;
  IF NOT EXISTS (SELECT 1 FROM appointments a WHERE a.member_id = NEW.member_id AND a.society_id = r.society_id AND a.is_signatory AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejecter_holds_signing_post'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
