-- DELIBERATELY WRONG (over-strict). No charge line may be posted at all.
--
-- The claim: this breaks the NORMAL path. The §7a contribution has every
-- line on its right account (bank, the member's savings, partner charge on
-- the partner account, margin on the margin account) and must commit. If
-- the control passed here it would prove only that a plain transfer
-- commits.
--
-- Must turn red: TestRole_Control_ContributionWithEveryRoleRight.
CREATE FUNCTION m_role_charge() RETURNS trigger AS $$
BEGIN
  IF NEW.charge_role IN ('partner_charge', 'argon_margin') THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_charge BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION m_role_charge();
