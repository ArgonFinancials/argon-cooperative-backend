-- DELIBERATELY WRONG. Money must be owed, but the request may ask for any amount.
--
-- Must turn red: TestWriteOff4_TheRequestIsForWhatIsOwed.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.kind::text = 'loan_write_off' AND (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id WHERE e.loan_id = NEW.loan_id AND a.role = 'member_loan') <= 0 THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_needs_money_owed'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
