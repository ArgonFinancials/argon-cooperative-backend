-- DELIBERATELY WRONG. A charge line must be on its own charges account — but
-- a line on a charges account may call itself 'principal'.
--
-- The claim: the pairing enforced from the line's side only. A 'principal'
-- line on the partner-charges account is money in a charges account that
-- no charge figure counts, so the partner's share is understated with
-- nothing erroring.
--
-- Must turn red: TestRoleR4_ChargeAccountLineIsACharge.
CREATE FUNCTION m_role_charge() RETURNS trigger AS $$
DECLARE
  r text := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF (NEW.charge_role = 'partner_charge' AND r <> 'charges_partner')
     OR (NEW.charge_role = 'argon_margin' AND r <> 'charges_margin') THEN
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  -- (missing: a line on a charges account must carry that account's charge role)
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_charge BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION m_role_charge();
