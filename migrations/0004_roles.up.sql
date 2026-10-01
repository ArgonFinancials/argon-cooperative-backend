-- =============================================================================
-- ARGON — 0004: ACCOUNT ROLES
--
-- Written against test/tier1/tier1_0004_test.go (reviewed; mutation-tested
-- with test/tier1/mutants_0004 before this file existed).
--
-- A role says what an account IS, independent of its society-specific code:
-- whose money it holds and what a line on it means. Roles are where accounts
-- meet lines, so that is where the rules are:
--   - a line on a member account names the member;
--   - a line that names a member is on a member account;
--   - a partner charge is on the partner-charges account, a margin on the
--     margin account, and a line on a charges account says which charge.
--     Without this, D-03's separation existed in the schema and not in the
--     books: a partner charge was postable to the margin account.
--   - a role is final once any line uses the account. An unused account is a
--     configuration mistake; a used one is history.
-- =============================================================================

BEGIN;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM accounts) THEN
    RAISE EXCEPTION '0004: accounts already exist. Their roles cannot be inferred from a society-specific '
                    'code; assign each one deliberately before applying 0004.';
  END IF;
END $$;

ALTER TABLE accounts ADD COLUMN role TEXT NOT NULL;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general'));

CREATE FUNCTION ledger_line_role_admissible() RETURNS trigger AS $$
DECLARE
  r TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF r IS NULL THEN
    RETURN NEW;   -- no such account: the foreign key refuses it, by name
  END IF;
  IF r IN ('member_savings', 'member_wallet', 'member_loan') AND NEW.member_id IS NULL THEN
    RAISE EXCEPTION 'a line on a % account names the member it belongs to', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  IF NEW.member_id IS NOT NULL AND r NOT IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'a line naming a member belongs on a member account, not a % account', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_on_member_account';
  END IF;
  -- Both directions: a charge line on its own charges account, and a line on
  -- a charges account carrying that account's charge. (A line with no charge
  -- role is neither, so coalesce it rather than let NULL compare as unequal.)
  IF (coalesce(NEW.charge_role, '') = 'partner_charge') <> (r = 'charges_partner')
     OR (coalesce(NEW.charge_role, '') = 'argon_margin') <> (r = 'charges_margin') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_lines_role_admissible BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION ledger_line_role_admissible();

CREATE FUNCTION account_role_is_final() RETURNS trigger AS $$
BEGIN
  IF NEW.role IS DISTINCT FROM OLD.role AND EXISTS (SELECT 1 FROM ledger_lines WHERE account_id = OLD.id) THEN
    RAISE EXCEPTION 'account % carries lines; relabelling it would reinterpret history', OLD.code
      USING ERRCODE = 'check_violation', CONSTRAINT = 'account_role_is_final';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER accounts_role_is_final BEFORE UPDATE OF role ON accounts
  FOR EACH ROW EXECUTE FUNCTION account_role_is_final();

COMMIT;
