-- DELIBERATELY WRONG. A write-off may not exceed what is owed, but may be less: a partial loss nobody decided.
--
-- Must turn red: TestWriteOff9_TheEntryIsExactlyWhatIsOwed.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE e ledger_entries%ROWTYPE; BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind::text = 'loan_write_off' AND (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN ledger_entries x ON x.id = l.entry_id JOIN accounts a ON a.id = l.account_id WHERE x.loan_id = e.loan_id AND a.role = 'member_loan') < 0 THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_is_outstanding'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON ledger_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
