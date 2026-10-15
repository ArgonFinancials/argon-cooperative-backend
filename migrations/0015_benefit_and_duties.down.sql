-- Reverses 0015 only while nothing rests on it: once a contribution carries
-- the client's reference, that reference is how a retry is told from a second
-- contribution, and it is never dropped. When it refuses, nothing changes, but
-- migrate has already recorded version 14, dirty. The schema is still at 15:
-- recover with `migrate force 15` (deploy/README.md, "migrate says dirty").
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE client_reference IS NOT NULL) THEN
    RAISE EXCEPTION '0015 down: refusing — contributions carry client references; dropping them would make a retry indistinguishable from a second contribution';
  END IF;
END $$;
DROP TRIGGER withdrawal_holds_not_by_beneficiary ON withdrawal_holds;
DROP FUNCTION hold_not_by_beneficiary();
DROP TRIGGER withdrawal_hold_releases_not_by_beneficiary ON withdrawal_hold_releases;
DROP FUNCTION release_not_by_beneficiary();
DROP TRIGGER ledger_lines_not_attested_by_beneficiary ON ledger_lines;
DROP FUNCTION entry_not_attested_by_beneficiary();
DROP TRIGGER ledger_entries_not_recorded_by_authoriser ON ledger_entries;
DROP FUNCTION payout_not_recorded_by_authoriser();
DROP TRIGGER ledger_entries_name_client_reference ON ledger_entries;
DROP FUNCTION contribution_names_client_reference();
DROP INDEX contribution_recorded_once;
ALTER TABLE ledger_entries DROP COLUMN client_reference;
COMMIT;
