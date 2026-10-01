-- Reverses 0004 only while no ledger line exists: once lines exist, the
-- roles are part of how history is read.
-- When it refuses, nothing changes (the transaction rolls back), but migrate
-- has already recorded version 3, dirty. The schema is still at 4: recover
-- with `migrate force 4`, not by re-running anything.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_lines) THEN
    RAISE EXCEPTION '0004 down: refusing — ledger lines exist and are read through their accounts'' roles';
  END IF;
END $$;
DROP TRIGGER accounts_role_is_final ON accounts;
DROP FUNCTION account_role_is_final();
DROP TRIGGER ledger_lines_role_admissible ON ledger_lines;
DROP FUNCTION ledger_line_role_admissible();
ALTER TABLE accounts DROP COLUMN role;
COMMIT;
