-- =============================================================================
-- ARGON — 0041: THE COOPERATIVE'S OWN TRANSFER REFERENCE, AND NO PAYOUT INTO
-- AN ACCOUNT AT VFD (2026-11-25, the reviewer, on the wallet platform's review).
--
-- The cooperative sends from the wallet platform's VFD pool, on the same
-- credentials. The wallet's references are "Argon-" and 24 hex characters;
-- ours were "Argon-" and 32. The prefix is the only thing that says whose a
-- transfer is, and a lookup VFD matches by prefix would hand one platform the
-- other's transfer. Ours are now "COOP-" and the hold's 32 hex characters
-- (references already recorded are kept as they are: none was ever sent).
--
-- A member's bank details are never at VFD's own bank code (090110): an
-- account there may sit on the same credentials, and a payout into it never
-- leaves the pool (0040's reason for paying into a member's own bank). Which
-- VFD accounts share the credentials cannot be told from outside, so none is
-- taken.
--
-- Written against test/tier1/tier1_0041_test.go (gen0041.py).
-- =============================================================================
BEGIN;

CREATE OR REPLACE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'COOP-' || replace(p_hold::text, '-', '')
$$;

ALTER TABLE member_bank_accounts ADD CONSTRAINT bank_not_at_vfd CHECK (bank_code <> '090110');

COMMIT;
