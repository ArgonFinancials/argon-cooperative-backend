-- DELIBERATELY WRONG. A role must be one of the known roles — if there is
-- one. The column stays nullable.
--
-- The claim: a CHECK on a nullable column passes NULL. This is the most
-- natural way to write "the role must be valid", and it lets an account
-- exist with no role at all. Every reader that asks "which lines are
-- savings?" then silently skips it.
--
-- Must turn red: TestRoleR1_AccountNeedsARole.
-- Expected to stay green: TestRoleR1_RoleMustBeKnown.
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general'));
-- (missing: role NOT NULL)
