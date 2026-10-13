-- DELIBERATELY WRONG. VAT is computed on the partner's fee, as before 0013.
--
-- Must turn red: TestVAT01_VATIsChargedOnArgonsFeeNotThePartners, TestVAT02_VATFollowsArgonsFee, TestVAT06_AHoldsVATIsOnArgonsFee.
BEGIN;
CREATE OR REPLACE FUNCTION charge_quote(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE)
RETURNS TABLE (partner_fee_kobo BIGINT, vat_kobo BIGINT, statutory_levy_kobo BIGINT, argon_margin_kobo BIGINT) SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  partner_fee_kobo := 0; vat_kobo := 0; statutory_levy_kobo := 0; argon_margin_kobo := 0;
  IF NOT EXISTS (SELECT 1 FROM charge_schedules WHERE society_id = p_society AND kind = p_kind AND effective_from <= p_on) THEN
    IF p_kind = 'contribution_in' THEN
      RETURN NEXT;   -- a contribution in a society that charges none is free
      RETURN;
    END IF;
  END IF;
  partner_fee_kobo := charge_component(p_society, p_kind, 'partner_fee', p_amount, p_on);
  argon_margin_kobo := charge_component(p_society, p_kind, 'argon_margin', p_amount, p_on);
  -- VAT on the base below: the bank's VAT is inside what the bank charges Argon.
  vat_kobo := charge_component(p_society, p_kind, 'vat_on_argon_fee', partner_fee_kobo, p_on);
  statutory_levy_kobo := charge_component(p_society, p_kind, 'statutory_levy', p_amount, p_on);
  RETURN NEXT;
END $$ LANGUAGE plpgsql STABLE;

CREATE OR REPLACE FUNCTION ledger_line_role_admissible() RETURNS trigger AS $$
DECLARE
  r TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF r IS NULL THEN
    RETURN NEW;
  END IF;
  IF r IN ('member_savings', 'member_wallet', 'member_loan') AND NEW.member_id IS NULL THEN
    RAISE EXCEPTION 'a line on a % account names the member it belongs to', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  IF NEW.member_id IS NOT NULL AND r NOT IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'a line naming a member belongs on a member account, not a % account', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_on_member_account';
  END IF;
  IF (coalesce(NEW.charge_role, '') = 'partner_charge') <> (r = 'charges_partner')
     OR (coalesce(NEW.charge_role, '') = 'argon_margin') <> (r = 'charges_margin')
     OR (coalesce(NEW.charge_role, '') = 'statutory_levy') <> (r = 'statutory_levy_payable')
     OR (coalesce(NEW.charge_role, '') = 'vat') <> (r = 'charges_vat') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
COMMIT;
