-- 0017's INTERFACE: "M0" for suite 0017, the shape with NO RULES: where a
-- withdrawal request keeps the charge it was quoted when raised, and the
-- function that answers the largest amount a member may request (here it
-- answers nothing). Applied on top of the real 0001–0016.
BEGIN;
ALTER TABLE approval_requests ADD COLUMN quoted_partner_fee_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_vat_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_statutory_levy_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_argon_margin_kobo BIGINT;
ALTER TABLE approval_requests ADD COLUMN quoted_on DATE;

CREATE FUNCTION largest_withdrawal(p_society UUID, p_kind movement_kind, p_pool BIGINT, p_on DATE) RETURNS BIGINT AS $$
  SELECT NULL::bigint
$$ LANGUAGE sql STABLE;
COMMIT;
