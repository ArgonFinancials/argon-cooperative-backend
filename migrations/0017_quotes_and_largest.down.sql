-- Reverses 0017 only while nothing rests on it: once a request keeps the charge
-- it was quoted, dropping it would erase what a member was told when they asked
-- (finding 49), the figure a later charge is compared with. When it refuses,
-- nothing changes, but migrate has already recorded version 16, dirty. The
-- schema is still at 17: recover with `migrate force 17` (deploy/README.md,
-- "migrate says dirty").
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests WHERE quoted_on IS NOT NULL) THEN
    RAISE EXCEPTION '0017 down: refusing — withdrawal requests keep the charge they were quoted; dropping it would erase what members were told when they asked';
  END IF;
END $$;
DROP FUNCTION largest_withdrawal(UUID, movement_kind, BIGINT, DATE);
DROP FUNCTION fee_at(UUID, movement_kind, BIGINT, DATE);
DROP FUNCTION withdrawal_fits(UUID, movement_kind, BIGINT, BIGINT, DATE);
DROP TRIGGER approval_requests_withdrawal_r4_quote ON approval_requests;
DROP FUNCTION approval_request_quote();
ALTER TABLE approval_requests DROP COLUMN quoted_on;
ALTER TABLE approval_requests DROP COLUMN quoted_argon_margin_kobo;
ALTER TABLE approval_requests DROP COLUMN quoted_statutory_levy_kobo;
ALTER TABLE approval_requests DROP COLUMN quoted_vat_kobo;
ALTER TABLE approval_requests DROP COLUMN quoted_partner_fee_kobo;
COMMIT;
