-- DELIBERATELY WRONG. Every account must have a role — any text will do.
--
-- The claim: NOT NULL without the list. "savingz" is a role nothing reads, so
-- a mistyped role is an account that every statement and report quietly
-- ignores, with nothing erroring.
--
-- Must turn red: TestRoleR1_RoleMustBeKnown.
-- Expected to stay green: TestRoleR1_AccountNeedsARole.
ALTER TABLE accounts ALTER COLUMN role SET NOT NULL;
-- (missing: role IN (the known roles))
