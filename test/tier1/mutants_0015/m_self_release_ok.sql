-- DELIBERATELY WRONG. A member releases the hold on their own payout.
--
-- Must turn red: TestBenefit08_NobodyReleasesTheirOwnHold.
BEGIN;
CREATE FUNCTION hold_not_by_beneficiary() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM members WHERE id = NEW.member_id AND user_id = NEW.actor_user_id) THEN
    PERFORM auth_refuse('authoriser_not_beneficiary', 'nobody authorises a movement they benefit from (LEDGER_CONTRACT §10): this payout comes out of the authoriser''s own savings. Another signatory authorises it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER withdrawal_holds_not_by_beneficiary BEFORE INSERT ON withdrawal_holds
  FOR EACH ROW EXECUTE FUNCTION hold_not_by_beneficiary();
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
