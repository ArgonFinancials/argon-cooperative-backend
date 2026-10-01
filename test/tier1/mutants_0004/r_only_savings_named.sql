-- DELIBERATELY WRONG. A line on a member_savings account must name a member;
-- a line on a member_loan account need not.
--
-- The claim: the rule written for the case in front of the author (the
-- statement reads savings) and not for what "member" means. A loan
-- disbursed to no member is a receivable nobody owes.
--
-- Must turn red: TestRoleR2_LoanLineNamesAMember.
-- Expected to stay green: TestRoleR2_SavingsLineNamesAMember.
CREATE FUNCTION m_role_line() RETURNS trigger AS $$
BEGIN
  IF NEW.member_id IS NULL
     AND (SELECT role FROM accounts WHERE id = NEW.account_id) = 'member_savings' THEN
    -- (missing: member_wallet and member_loan too)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_line BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION m_role_line();
