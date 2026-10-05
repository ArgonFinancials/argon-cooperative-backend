-- DELIBERATELY WRONG. OVER-STRICT: compares the account with the line's MEMBER, so a line with no member (bank, charges) is refused even on its own society's account.
--
-- Must turn red: TestLineSociety_Control_OwnAccountsAccepted.
CREATE FUNCTION m_line_society() RETURNS trigger AS $$
DECLARE es UUID; acct_soc UUID; acct_role TEXT;
BEGIN
  SELECT society_id INTO es FROM ledger_entries WHERE id = NEW.entry_id;
  SELECT society_id, role INTO acct_soc, acct_role FROM accounts WHERE id = NEW.account_id;
  IF es IS NULL OR acct_soc IS NULL THEN
    RETURN NEW;   -- the foreign keys refuse these, by name
  END IF;
  IF (NEW.member_id IS NULL OR (SELECT society_id FROM members WHERE id = NEW.member_id) <> acct_soc) THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_line_account_in_society';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_line_society BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m_line_society();
