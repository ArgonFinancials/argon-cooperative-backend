-- DELIBERATELY WRONG. The reason is checked; the time the author supplies is believed.
--
-- Must turn red: TestApproval9_RejectionTimeIsTheServers.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.reason NOT IN ('documents_missing', 'amount_wrong', 'not_eligible', 'looks_wrong') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_reason_known'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
