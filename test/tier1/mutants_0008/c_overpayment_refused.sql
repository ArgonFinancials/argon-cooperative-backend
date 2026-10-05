-- DELIBERATELY WRONG. OVER-STRICT: a repayment beyond what is owed is refused, so money that arrived goes unrecorded.
--
-- Must turn red: TestLoan_Control_FullLifecycle.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE e ledger_entries%ROWTYPE; BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind = 'loan_repayment' AND (SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN ledger_entries x ON x.id = l.entry_id JOIN accounts a ON a.id = l.account_id
        WHERE x.loan_id = e.loan_id AND a.role = 'member_loan') < 0 THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_overpaid'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON ledger_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
