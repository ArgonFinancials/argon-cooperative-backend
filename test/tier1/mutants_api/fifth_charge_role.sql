-- A FIFTH CHARGE ROLE ARRIVES, the statement handler unchanged: a stamp duty, with its own account role and its pairing, as a real migration would add one.
--
-- Not a broken rule: a schema the handler has not caught up with. Run against
-- the REAL handler (SQL mutants never set the fake's variable), it must turn
-- TestApiStatement20 red ON THE GROSS: the new role's line is posted, and a
-- handler that reads four named roles leaves it out (finding 55, one role on).
--
-- Must turn red: TestApiStatement20_EveryChargeRoleIsShownAndCountedInTheGross.
BEGIN;
ALTER TABLE ledger_lines DROP CONSTRAINT ledger_lines_charge_role_check;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_charge_role_check CHECK (charge_role IN (
  'principal', 'partner_charge', 'vat', 'statutory_levy', 'argon_margin', 'stamp_duty'));
ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general', 'loan_loss', 'loan_recovery_income',
  'charges_vat', 'statutory_levy_payable', 'stamp_duty_payable'));

CREATE OR REPLACE FUNCTION ledger_line_role_admissible() RETURNS trigger AS $$
DECLARE
  r TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF r IS NULL THEN
    RETURN NEW;
  END IF;
  IF r IN ('member_savings', 'member_wallet', 'member_loan') AND NEW.member_id IS NULL THEN
    RAISE EXCEPTION 'a line on a % account names the member it belongs to', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  IF NEW.member_id IS NOT NULL AND r NOT IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'a line naming a member belongs on a member account, not a % account', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_on_member_account';
  END IF;
  IF (coalesce(NEW.charge_role, '') = 'partner_charge') <> (r = 'charges_partner')
     OR (coalesce(NEW.charge_role, '') = 'argon_margin') <> (r = 'charges_margin')
     OR (coalesce(NEW.charge_role, '') = 'statutory_levy') <> (r = 'statutory_levy_payable')
     OR (coalesce(NEW.charge_role, '') = 'vat') <> (r = 'charges_vat')
     OR (coalesce(NEW.charge_role, '') = 'stamp_duty') <> (r = 'stamp_duty_payable') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
COMMIT;
