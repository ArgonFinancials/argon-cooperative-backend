-- DELIBERATELY WRONG. '3x savings' read as savings PLUS three times savings.
--
-- Must turn red: TestLoan4_PrincipalWithinTheSavingsMultiple.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.principal_kobo > (SELECT -coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE a.role = 'member_savings' AND l.member_id = NEW.member_id) * NEW.savings_multiple_pct / 100 + (SELECT -coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE a.role = 'member_savings' AND l.member_id = NEW.member_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_within_savings_multiple'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
