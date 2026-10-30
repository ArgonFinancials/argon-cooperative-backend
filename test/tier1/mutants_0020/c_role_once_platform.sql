-- DELIBERATELY WRONG (over-strict). One account per role across the PLATFORM: the second society could have no savings account.
--
-- Must turn red: TestChartRole03_Control_EachSocietyHasItsOwn.
BEGIN;
CREATE UNIQUE INDEX account_role_once ON accounts (role) WHERE role <> 'general';
COMMIT;
