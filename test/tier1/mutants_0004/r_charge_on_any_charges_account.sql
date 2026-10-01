-- DELIBERATELY WRONG. A charge line must be on A charges account — either one.
--
-- The claim: "charges go to a charges account" passes a partner charge
-- posted to the margin account. That is D-03 undone without a single
-- figure changing: the total is right, and what Argon earned versus what it
-- passed through is wrong for ever.
--
-- Must turn red: TestRoleR4_PartnerChargeOnlyOnPartnerAccount.
-- Expected to stay green: TestRoleR4_MarginOnlyOnMarginAccount (margin on the bank).
CREATE FUNCTION m_role_charge() RETURNS trigger AS $$
BEGIN
  IF NEW.charge_role IN ('partner_charge', 'argon_margin')
     AND (SELECT role FROM accounts WHERE id = NEW.account_id) NOT IN ('charges_partner', 'charges_margin') THEN
    -- (missing: partner_charge only on charges_partner, argon_margin only on charges_margin)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_charge BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION m_role_charge();
