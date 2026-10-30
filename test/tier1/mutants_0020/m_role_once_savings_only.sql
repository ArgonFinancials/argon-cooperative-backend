-- DELIBERATELY WRONG. Only member_savings is held to one account: the role the batch already checked, and none of the others the money paths post to.
--
-- Must turn red: TestChartRole01_ASecondAccountOfARoleIsRefused.
BEGIN;
CREATE UNIQUE INDEX account_role_once ON accounts (society_id, role) WHERE role = 'member_savings';
COMMIT;
