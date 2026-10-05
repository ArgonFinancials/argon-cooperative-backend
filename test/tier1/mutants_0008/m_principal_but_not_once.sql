-- DELIBERATELY WRONG. Each disbursement must be the principal; nothing stops a second one.
--
-- Must turn red: TestLoan27_DisbursedOnce.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE e ledger_entries%ROWTYPE; BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind = 'loan_disbursement' AND (SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'member_loan') <> (SELECT principal_kobo FROM loans WHERE id = e.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_is_principal'; END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER m_t AFTER INSERT ON ledger_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION m_f();
