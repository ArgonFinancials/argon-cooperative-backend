-- =============================================================================
-- ARGON — 0019: EVERY SIGN-IN CODE HANDED TO THE SMS PROVIDER IS RECORDED
--
-- Written against test/tier1/tier1_0019_test.go (the Termii sender, piece 3,
-- approved 2026-10-28; mutation-tested with test/tier1/mutants_0019 before this
-- file existed). deploy/AUTH_SMS.md.
--
-- One row per send, written once by the sign-in service (argon_auth) and never
-- changed: which code (the destination is on the code's own row, not copied),
-- which provider, what came of it, and Termii's message id when it accepted.
-- "Was this member's code sent?" becomes a lookup, with an id to quote to
-- Termii's support.
--
--   accepted     Termii said yes and gave a message id. NOT "delivered":
--                delivery reports are a later stage, named in AUTH_SMS.md.
--   refused      Termii said no; its refusal CODE is kept, never its wording.
--   not_reached  the connection was never made.
--   unknown      the request may have reached Termii; no readable answer came
--                back. The member was told the code may be late.
--
-- THERE IS NO COLUMN FOR THE MESSAGE TEXT OR THE CODE: a column nobody fills is
-- a column someone fills later. Only argon_auth writes or reads this table
-- (deploy/sql/grants.sql; probed as argon_app, P49 and P50).
-- =============================================================================
BEGIN;
CREATE TABLE sms_sends (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code_id             UUID NOT NULL REFERENCES auth_codes(id),
  provider            TEXT NOT NULL,
  outcome             TEXT NOT NULL,
  provider_message_id TEXT,
  provider_code       TEXT,
  at                  TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);
ALTER TABLE sms_sends ADD CONSTRAINT sms_provider_known CHECK (provider IN ('termii'));
ALTER TABLE sms_sends ADD CONSTRAINT sms_outcome_known CHECK (outcome IN ('accepted', 'refused', 'not_reached', 'unknown'));
ALTER TABLE sms_sends ADD CONSTRAINT sms_accepted_has_message_id CHECK ((outcome = 'accepted') = (coalesce(provider_message_id, '') <> ''));
ALTER TABLE sms_sends ADD CONSTRAINT sms_provider_code_on_refusal CHECK (provider_code IS NULL OR outcome = 'refused');
CREATE UNIQUE INDEX sms_message_id_once ON sms_sends (provider, provider_message_id) WHERE provider_message_id IS NOT NULL;
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
