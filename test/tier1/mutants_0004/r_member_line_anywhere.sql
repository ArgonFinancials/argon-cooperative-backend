-- DELIBERATELY WRONG. Lines on member accounts must name a member — but a
-- member may be named on any line, the bank's included.
--
-- The claim: only half of the pairing. A member's id on a bank line makes
-- that line part of their sub-ledger to any reader that sums lines by
-- member: their statement shows the bank's movement as theirs.
--
-- Must turn red: TestRoleR3_MemberLineOnlyOnMemberAccounts.
CREATE FUNCTION m_role_line() RETURNS trigger AS $$
BEGIN
  IF NEW.member_id IS NULL
     AND (SELECT role FROM accounts WHERE id = NEW.account_id) IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  -- (missing: a line that names a member must be on a member account)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_line BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION m_role_line();
