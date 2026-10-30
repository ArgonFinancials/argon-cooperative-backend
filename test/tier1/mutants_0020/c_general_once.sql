-- DELIBERATELY WRONG (over-strict). 'general' held to one account too: a society could keep only one account of its own.
--
-- Must turn red: TestChartRole02_Control_GeneralAccountsAreMany.
BEGIN;
CREATE UNIQUE INDEX account_role_once ON accounts (society_id, role);
COMMIT;
