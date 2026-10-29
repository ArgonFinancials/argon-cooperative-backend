-- DELIBERATELY WRONG. One Termii message id can be recorded for two sends.
--
-- Must turn red: TestSmsSend04_AMessageIdIsRecordedOnce.
BEGIN;
ALTER TABLE sms_sends ADD CONSTRAINT sms_provider_known CHECK (provider IN ('termii'));
ALTER TABLE sms_sends ADD CONSTRAINT sms_outcome_known CHECK (outcome IN ('accepted', 'refused', 'not_reached', 'unknown'));
ALTER TABLE sms_sends ADD CONSTRAINT sms_accepted_has_message_id CHECK ((outcome = 'accepted') = (coalesce(provider_message_id, '') <> ''));
ALTER TABLE sms_sends ADD CONSTRAINT sms_provider_code_on_refusal CHECK (provider_code IS NULL OR outcome = 'refused');
CREATE FUNCTION sms_sends_admissible() RETURNS trigger AS $$
BEGIN
  PERFORM auth_server_time(NEW.at, 'sms_sends_at_is_now');
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER sms_sends_admissible BEFORE INSERT ON sms_sends FOR EACH ROW EXECUTE FUNCTION sms_sends_admissible();
CREATE FUNCTION sms_sends_final() RETURNS trigger AS $$
BEGIN
  PERFORM auth_refuse('sms_send_is_final', 'a send is recorded once and never changed or removed');
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER sms_sends_final BEFORE UPDATE OR DELETE ON sms_sends FOR EACH ROW EXECUTE FUNCTION sms_sends_final();
COMMIT;
