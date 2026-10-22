-- =============================================================================
-- ARGON — 0017: A REQUEST KEEPS THE CHARGE IT WAS QUOTED; THE LARGEST AMOUNT A
-- MEMBER MAY REQUEST, EXACTLY
--
-- Written against test/tier1/tier1_0017_test.go (approved 2026-10-22, in five
-- pieces sent as plain text; mutation-tested with test/tier1/mutants_0017
-- before this file existed). LEDGER_CONTRACT.md §8, §9, §12; finding 49.
--
-- THE QUOTE, KEPT (finding 49). The quote is indicative and the hold's charge
-- applies; a member quoted ₦10.75 can have ₦26.88 taken if the society's series
-- changes in between, and must be told. Recomputing "what was quoted" from the
-- series in force on the raise date fails exactly when a series takes effect
-- the same day, after the request. So a withdrawal request KEEPS its quote:
-- the charge from the same charge_quote call 0016's withdrawable check makes,
-- so the figure kept is the figure the request was JUDGED by. Server-set, as
-- the expiry: a client never chooses the figure it will be held to. A request
-- of any other kind carries none.
--
-- THE LARGEST AMOUNT A MEMBER MAY REQUEST (decided 2026-10-21: the figure a
-- member is shown is what they can actually ask for, not the pool the charge
-- comes out of). largest_withdrawal(society, kind, pool, date) answers EXACTLY,
-- one kobo below a threshold where the charge jumps, and where a band makes the
-- charge FALL above a threshold. It prices every amount with the same
-- charge_quote the raise rule uses, and compares as that rule does (the amount
-- and its charge at most the pool), so what it answers is accepted when raised
-- and one kobo more is refused.
-- =============================================================================

BEGIN;
ALTER TABLE approval_requests ADD COLUMN quoted_partner_fee_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_vat_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_statutory_levy_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_argon_margin_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_on DATE;

-- ---- the quote, kept on the request -----------------------------------------------
-- Named to fire after r3 and before r5, whose charge_quote is the same call:
-- where the series cannot say, both refuse alike.
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

-- ---- the largest amount a member may request --------------------------------------
-- Does a withdrawal of p_amount, its charge on top, fit p_pool? An amount whose
-- charge the series cannot determine cannot be requested: it does not fit.
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

-- The amount plus its charge only rises within a stretch where no band changes;
-- a band can make it FALL at its start (a cheaper band above a threshold), so
-- one bisection over the whole range is not enough. The range is cut at every
-- band's start and the amount after its end, for every component banded on the
-- amount, and wherever Argon's fee crosses a start of VAT's bands (VAT is banded
-- on the fee); each stretch is then bisected, and the largest answer wins.
-- Bands of every series of the society are used: more cuts than needed never
-- change the answer.
CREATE FUNCTION largest_withdrawal(p_society UUID, p_kind movement_kind, p_pool BIGINT, p_on DATE) RETURNS BIGINT SECURITY DEFINER SET search_path = public, pg_temp AS $$
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
