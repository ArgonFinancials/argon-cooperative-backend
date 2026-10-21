-- OVER-REACHING (a control must go red). OVER-REACHING: every kind of request is quoted as a withdrawal.
--
-- Must turn red: TestQuote_Control_ALoanRequestCarriesNoQuote.
BEGIN;
-- The charge the member was quoted, kept as raised: the series in force at
-- the raise, server-set (as the expiry), whatever the client sends. A request
-- of any other kind carries none. Named to fire after r3 and before r5, whose
-- charge_quote is the same call: where the series cannot say, both refuse alike.
CREATE FUNCTION approval_request_quote() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  q RECORD;
BEGIN
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
-- THE LARGEST AMOUNT A MEMBER MAY REQUEST from p_pool (what may leave their
-- savings), its charge on top, EXACTLY. The amount plus its charge only rises
-- within a stretch where no band changes; a band can make it FALL at its start
-- (a cheaper band above a threshold), so one bisection over the whole range is
-- not enough. The range is cut at every band's start and the amount after its
-- end, for every component banded on the amount, and wherever Argon's fee
-- crosses a start of VAT's bands (VAT is banded on the fee); each stretch is
-- then bisected, and the largest answer wins. Bands of every series of the
-- society are used: more cuts than needed never change the answer.
CREATE OR REPLACE FUNCTION largest_withdrawal(p_society UUID, p_kind movement_kind, p_pool BIGINT, p_on DATE) RETURNS BIGINT SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  cuts  BIGINT[];
  vats  BIGINT[];
  extra BIGINT[] := '{}';
  n     INT;
  i     INT;
  a     BIGINT;
  b     BIGINT;
  v     BIGINT;
  lo    BIGINT;
  hi    BIGINT;
  mid   BIGINT;
  best  BIGINT := 0;
BEGIN
  IF p_pool IS NULL OR p_pool <= 0 THEN
    RETURN 0;
  END IF;
  SELECT array_agg(DISTINCT x ORDER BY x) INTO cuts FROM (
    SELECT 0::bigint AS x
    UNION SELECT cb.from_kobo FROM charge_bands cb JOIN charge_schedules cs ON cs.id = cb.schedule_id
     WHERE cs.society_id = p_society AND cs.kind = p_kind AND cs.component <> 'vat_on_argon_fee'
    UNION SELECT cb.to_kobo + 1 FROM charge_bands cb JOIN charge_schedules cs ON cs.id = cb.schedule_id
     WHERE cs.society_id = p_society AND cs.kind = p_kind AND cs.component <> 'vat_on_argon_fee' AND cb.to_kobo IS NOT NULL) c
   WHERE x BETWEEN 0 AND p_pool;
  SELECT array_agg(DISTINCT x) INTO vats FROM (
    SELECT cb.from_kobo AS x FROM charge_bands cb JOIN charge_schedules cs ON cs.id = cb.schedule_id
     WHERE cs.society_id = p_society AND cs.kind = p_kind AND cs.component = 'vat_on_argon_fee'
    UNION SELECT cb.to_kobo + 1 FROM charge_bands cb JOIN charge_schedules cs ON cs.id = cb.schedule_id
     WHERE cs.society_id = p_society AND cs.kind = p_kind AND cs.component = 'vat_on_argon_fee' AND cb.to_kobo IS NOT NULL) c;
  n := array_length(cuts, 1);
  IF vats IS NOT NULL THEN
    FOR i IN 1 .. n LOOP
      a := cuts[i];
      b := CASE WHEN i < n THEN cuts[i + 1] - 1 ELSE p_pool END;
      FOREACH v IN ARRAY vats LOOP
        -- the smallest amount in [a, b] whose fee reaches v: the fee only rises here
        IF coalesce(fee_at(p_society, p_kind, a, p_on), -1) < v AND coalesce(fee_at(p_society, p_kind, b, p_on), -1) >= v THEN
          lo := a; hi := b;
          WHILE lo < hi LOOP
            mid := lo + (hi - lo) / 2;
            IF coalesce(fee_at(p_society, p_kind, mid, p_on), -1) >= v THEN hi := mid; ELSE lo := mid + 1; END IF;
          END LOOP;
          extra := extra || lo;
        END IF;
      END LOOP;
    END LOOP;
    SELECT array_agg(DISTINCT x ORDER BY x) INTO cuts FROM unnest(cuts || extra) x;
    n := array_length(cuts, 1);
  END IF;
  FOR i IN 1 .. n LOOP
    a := cuts[i];
    b := CASE WHEN i < n THEN cuts[i + 1] - 1 ELSE p_pool END;
    CONTINUE WHEN a > b OR NOT withdrawal_fits(p_society, p_kind, a, p_pool, p_on);
    lo := a; hi := b;
    WHILE lo < hi LOOP
      mid := lo + (hi - lo + 1) / 2;
      IF withdrawal_fits(p_society, p_kind, mid, p_pool, p_on) THEN lo := mid; ELSE hi := mid - 1; END IF;
    END LOOP;
    best := greatest(best, lo);
  END LOOP;
  RETURN best;
END $$ LANGUAGE plpgsql STABLE;
COMMIT;
