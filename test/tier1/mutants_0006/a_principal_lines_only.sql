-- DELIBERATELY WRONG. Only principal lines are checked: a charge line can still post to another society's charges account.
--
-- Must turn red: TestLineSociety2_ChargeLineCannotPostToAnotherSocietysChargesAccount.
CREATE FUNCTION m_line_society() RETURNS trigger AS $$
DECLARE es UUID; acct_soc UUID; acct_role TEXT;
BEGIN
  SELECT society_id INTO es FROM ledger_entries WHERE id = NEW.entry_id;
  SELECT society_id, role INTO acct_soc, acct_role FROM accounts WHERE id = NEW.account_id;
  IF es IS NULL OR acct_soc IS NULL THEN
    RETURN NEW;   -- the foreign keys refuse these, by name
  END IF;
  IF acct_soc <> es AND coalesce(NEW.charge_role, 'principal') = 'principal' THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_line_account_in_society';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_line_society BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION m_line_society();
