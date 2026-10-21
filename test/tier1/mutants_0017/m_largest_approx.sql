-- DELIBERATELY WRONG. The pool less the charge on the whole pool.
--
-- Must turn red: TestLargest01_ExactOneKoboBelowTheLevy, TestLargest02_ExactOneKoboBelowTheFee.
BEGIN;
-- The charge the member was quoted, kept as raised: the series in force at
-- the raise, server-set (as the expiry), whatever the client sends. A request
-- of any other kind carries none. Named to fire after r3 and before r5, whose
-- charge_quote is the same call: where the series cannot say, both refuse alike.
CREATE FUNCTION approval_request_quote() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q RECORD;
BEGIN
  IF NEW.kind <> 'withdrawal_out' THEN
    NEW.quoted_partner_fee_kobo := NULL; NEW.quoted_vat_kobo := NULL;
    NEW.quoted_statutory_levy_kobo := NULL; NEW.quoted_argon_margin_kobo := NULL; NEW.quoted_on := NULL;
    RETURN NEW;
  END IF;
  SELECT * INTO q FROM charge_quote(NEW.society_id, 'withdrawal_out', NEW.amount_kobo, current_date);
  NEW.quoted_partner_fee_kobo := q.partner_fee_kobo;
  NEW.quoted_vat_kobo := q.vat_kobo;
  NEW.quoted_statutory_levy_kobo := q.statutory_levy_kobo;
  NEW.quoted_argon_margin_kobo := q.argon_margin_kobo;
  NEW.quoted_on := current_date;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_withdrawal_r4_quote BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION approval_request_quote();
-- Does a withdrawal of p_amount, its charge on top, fit p_pool? An amount
-- whose charge the series cannot determine cannot be requested: it does not fit.
CREATE FUNCTION withdrawal_fits(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_pool BIGINT, p_on DATE) RETURNS BOOLEAN SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q RECORD;
BEGIN
  BEGIN
    SELECT * INTO q FROM charge_quote(p_society, p_kind, p_amount, p_on);
  EXCEPTION WHEN check_violation THEN
    RETURN false;
  END;
  RETURN p_amount + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo <= p_pool;
END $$ LANGUAGE plpgsql STABLE;

-- Argon's fee on an amount, NULL where the series cannot say (VAT is banded on it).
CREATE FUNCTION fee_at(p_society UUID, p_kind movement_kind, p_amount BIGINT, p_on DATE) RETURNS BIGINT SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  RETURN charge_component(p_society, p_kind, 'argon_margin', p_amount, p_on);
EXCEPTION WHEN check_violation THEN
  RETURN NULL;
END $$ LANGUAGE plpgsql STABLE;
-- DELIBERATELY WRONG: the pool less the charge on the whole pool.
CREATE OR REPLACE FUNCTION largest_withdrawal(p_society UUID, p_kind movement_kind, p_pool BIGINT, p_on DATE) RETURNS BIGINT SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_pool IS NULL OR p_pool <= 0 THEN RETURN 0; END IF;
  DECLARE q RECORD;
  BEGIN
    SELECT * INTO q FROM charge_quote(p_society, p_kind, p_pool, p_on);
    RETURN greatest(0, p_pool - (q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo));
  EXCEPTION WHEN check_violation THEN
    RETURN 0;
  END;
END $$ LANGUAGE plpgsql STABLE;
COMMIT;
