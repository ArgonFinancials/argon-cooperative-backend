-- DELIBERATELY WRONG. An unset quorum is refused, but a set one is never compared with the request: any number of signatures will do.
--
-- Must turn red: TestWriteOff2_TheRequestCarriesTheSocietysQuorum.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND (SELECT write_off_signatures_required FROM societies WHERE id = NEW.society_id) IS NULL THEN RAISE EXCEPTION 'this society has not decided how many signatures a write-off needs' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_quorum_unset'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
