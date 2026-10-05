BEGIN;
DROP TRIGGER ledger_lines_account_in_society ON ledger_lines;
DROP FUNCTION ledger_line_account_in_society();
COMMIT;
