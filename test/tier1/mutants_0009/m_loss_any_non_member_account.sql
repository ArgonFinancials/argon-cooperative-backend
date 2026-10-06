-- DELIBERATELY WRONG. A write-off's debit may go to any account that is not a member's: the bank included.
--
-- Must turn red: TestWriteOff12_TheLossGoesToALossAccount.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE e ledger_entries%ROWTYPE; BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind::text = 'loan_write_off' AND NEW.amount_kobo > 0 AND (SELECT role FROM accounts WHERE id = NEW.account_id) IN ('member_savings', 'member_wallet', 'member_loan') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'write_off_to_loss_account'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m_f();
