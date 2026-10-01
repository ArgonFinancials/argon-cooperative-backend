-- =============================================================================
-- 0004 INTERFACE — ACCOUNT ROLES. NAMES ONLY, NO ENFORCEMENT.
--
-- What an account IS, independent of its society-specific code: whose money
-- it holds and what a line on it means. The member statement reads
-- member_savings lines; reports and the trial balance will read the rest.
--
-- Roles: member_savings, member_wallet, member_loan, bank, charges_partner,
--        charges_margin, migration_control, general.
--
-- Applied on top of 0001–0003 it is M0 for the roles suite (TestRole*), and
-- the base the statement suite (TestApiStatement*) is written against.
-- =============================================================================
BEGIN;
ALTER TABLE accounts ADD COLUMN role TEXT;
COMMIT;
