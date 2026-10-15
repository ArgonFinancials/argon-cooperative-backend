-- =============================================================================
-- ARGON — 0015: NOBODY AUTHORISES, SIGNS OR ATTESTS TO A MOVEMENT THEY BENEFIT
-- FROM; THE AUTHORISER NEVER RECORDS; A CONTRIBUTION IS RECORDED ONCE
--
-- Written against test/tier1/tier1_0015_test.go (approved 2026-10-14, the
-- release added at review; mutation-tested with test/tier1/mutants_0015
-- before this file existed). LEDGER_CONTRACT.md §10, widened 2026-10-14.
--
-- "Nobody signs a request they benefit from" was written for signatures, and
-- the payout hold is a different object, so it did not reach: a treasurer who
-- was the member being paid could authorise their own payout and record it.
-- The rule now names the acts, checked per PERSON (the member record's user):
--
--   AUTHORISES  the hold (authoriser_not_beneficiary), and releasing it
--               (releaser_not_beneficiary): a release frees the member's
--               withdrawable, the precondition for their next payout.
--   ATTESTS     recording a contribution or a payout into or out of one's own
--               savings (contribution_/payout_not_recorded_by_beneficiary).
--               Refusing a record is right here under finding 33's own test:
--               the attestation is malformed (the beneficiary's own), and a
--               correct form exists (another officer records the same event).
--
-- AUTHORISING AND ATTESTING ARE DIFFERENT DUTIES (authoriser_never_records):
-- the officer who authorised a hold never records its payout.
--
-- A SINGLE CONTRIBUTION NAMES THE CLIENT'S REFERENCE (client_reference;
-- contribution_names_client_reference), unique within the society
-- (contribution_recorded_once): a retry on a dropped connection is recorded
-- once. Imports (batches) and corrections are exempt.
-- =============================================================================

BEGIN;
ALTER TABLE ledger_entries ADD COLUMN client_reference TEXT;

CREATE FUNCTION hold_not_by_beneficiary() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND user_id = NEW.actor_user_id) THEN
    PERFORM auth_refuse('authoriser_not_beneficiary', 'nobody authorises a movement they benefit from (LEDGER_CONTRACT §10): this payout comes out of the authoriser''s own savings. Another signatory authorises it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER withdrawal_holds_not_by_beneficiary BEFORE INSERT ON withdrawal_holds
  FOR EACH ROW EXECUTE FUNCTION hold_not_by_beneficiary();
CREATE FUNCTION release_not_by_beneficiary() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM withdrawal_holds h JOIN members m ON m.id = h.member_id WHERE h.id = NEW.hold_id AND m.user_id = NEW.actor_user_id) THEN
    PERFORM auth_refuse('releaser_not_beneficiary', 'nobody authorises a movement they benefit from (LEDGER_CONTRACT §10): releasing this hold frees the releaser''s own withdrawable, the precondition for their next payout. Another signatory releases it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER withdrawal_hold_releases_not_by_beneficiary BEFORE INSERT ON withdrawal_hold_releases
  FOR EACH ROW EXECUTE FUNCTION release_not_by_beneficiary();
CREATE FUNCTION entry_not_attested_by_beneficiary() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e ledger_entries%ROWTYPE;
BEGIN
  IF NEW.member_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.batch_id IS NOT NULL OR e.reverses_id IS NOT NULL OR e.adjusts_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND user_id = e.actor_user_id) THEN
    RETURN NEW;
  END IF;
  IF e.kind = 'contribution_in' THEN
    PERFORM auth_refuse('contribution_not_recorded_by_beneficiary', 'nobody attests to a movement they benefit from (LEDGER_CONTRACT §10): this contribution is credited to the recorder''s own savings. Another officer records it');
  END IF;
  IF e.kind = 'withdrawal_out' THEN
    PERFORM auth_refuse('payout_not_recorded_by_beneficiary', 'nobody attests to a movement they benefit from (LEDGER_CONTRACT §10): this payout comes out of the recorder''s own savings. Another officer records it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_lines_not_attested_by_beneficiary BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION entry_not_attested_by_beneficiary();
CREATE FUNCTION payout_not_recorded_by_authoriser() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.hold_id IS NOT NULL AND EXISTS (SELECT 1 FROM withdrawal_holds WHERE id = NEW.hold_id AND actor_user_id = NEW.actor_user_id) THEN
    PERFORM auth_refuse('authoriser_never_records', 'authorising a payout and attesting that it happened are different duties (LEDGER_CONTRACT §10): the officer who authorised this hold does not record its payout. Another officer records it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_entries_not_recorded_by_authoriser BEFORE INSERT ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION payout_not_recorded_by_authoriser();
CREATE FUNCTION contribution_names_client_reference() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind = 'contribution_in' AND NEW.batch_id IS NULL AND NEW.reverses_id IS NULL AND NEW.adjusts_id IS NULL
     AND (NEW.client_reference IS NULL OR btrim(NEW.client_reference) = '') THEN
    PERFORM auth_refuse('contribution_names_client_reference', 'a contribution recorded on its own names the client''s reference for it, so a retried request is recorded once');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_entries_name_client_reference BEFORE INSERT ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION contribution_names_client_reference();
CREATE UNIQUE INDEX contribution_recorded_once ON ledger_entries (society_id, client_reference) WHERE client_reference IS NOT NULL;

COMMIT;
