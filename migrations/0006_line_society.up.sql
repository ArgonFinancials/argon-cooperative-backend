-- =============================================================================
-- 0006 — A LINE POSTS TO AN ACCOUNT OF ITS ENTRY'S SOCIETY (LEDGER_CONTRACT §3)
--
-- Found 2026-10-05 while specifying the trial balance, NOT by testing the
-- write path: 0002's ledger_line_admissible checks a line's MEMBER against the
-- entry's society and never its ACCOUNT. An entry of society S could post to
-- society T's account: the entry still sums to zero, but S's books lose a leg
-- and T's gain one, so NEITHER society's trial balance sums to zero. "The
-- trial balance sums to zero by construction" was not true until this.
--
-- Per account, whatever it is: bank, charges, a member's savings.
-- Tests: test/tier1/tier1_0006_test.go (suite 0006).
-- =============================================================================
BEGIN;

-- Existing rows are not re-checked by a trigger. Refuse to apply over books
-- that already carry a cross-society line: they must be found and corrected
-- deliberately first, not grandfathered silently.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_lines l
               JOIN ledger_entries e ON e.id = l.entry_id
               JOIN accounts a ON a.id = l.account_id
              WHERE a.society_id <> e.society_id) THEN
    RAISE EXCEPTION '0006: ledger lines already post to another society''s account. Find them '
                    '(ledger_lines JOIN ledger_entries JOIN accounts WHERE accounts.society_id <> ledger_entries.society_id) '
                    'and correct them before applying 0006.';
  END IF;
END $$;

CREATE FUNCTION ledger_line_account_in_society() RETURNS TRIGGER AS $$
DECLARE
  entry_society   UUID := (SELECT society_id FROM ledger_entries WHERE id = NEW.entry_id);
  account_society UUID := (SELECT society_id FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF entry_society IS NULL OR account_society IS NULL THEN
    RETURN NEW;   -- no such entry or account: the foreign keys refuse it, by name
  END IF;
  IF account_society <> entry_society THEN
    RAISE EXCEPTION 'line posts to an account of another society: entry % is society %, account % is society %',
                    NEW.entry_id, entry_society, NEW.account_id, account_society
      USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_line_account_in_society';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_lines_account_in_society
  BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION ledger_line_account_in_society();

COMMIT;
