-- DELIBERATELY WRONG (m_counts). A full copy of 0030 with one rule broken,
-- applied over 0030_interface.sql, whose function is removed first.
BEGIN;
DROP FUNCTION enrolment_slip_used(UUID, TEXT);
COMMIT;
-- =============================================================================
-- ARGON — 0030: IS THIS THE PERSON'S OWN SLIP, ALREADY USED? A QUESTION THAT
-- SPENDS NOTHING.
--
-- Written against test/tier1/tier1_0030_test.go (2026-11-10), mutation-tested
-- with test/tier1/mutants_0030 before this file existed. Found by the
-- frontend on argon_dev: §12 rules that the holder of a used slip is told so
-- ("This code has already been used. Speak to your secretary."), and that
-- message reached only whoever knew the THIEF's PIN. The member whose slip was
-- taken has never had a PIN (the thief chose it), so they type the slip and a
-- new PIN, and were refused like a typo.
--
-- The auth service asks this when a request says "I have never had a PIN"
-- (newPin) for an account that now has one. It answers true only for THIS
-- person's own slip, already used. It redeems nothing, counts nothing and
-- touches no live code: a live code is never redeemed without the PIN (D-15).
-- Only argon_auth may ask it (grants.sql; probe P69).
-- =============================================================================
BEGIN;
CREATE FUNCTION enrolment_slip_used(p_user UUID, p_digest TEXT) RETURNS boolean
  LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM enrolment_codes WHERE user_id = p_user AND used_at IS NOT NULL AND code_digest = p_digest) THEN RETURN true; END IF;
  UPDATE enrolment_codes SET wrong_tries = wrong_tries + 1 WHERE user_id = p_user AND used_at IS NULL AND voided_at IS NULL;
  RETURN false;
END $$;
REVOKE EXECUTE ON FUNCTION enrolment_slip_used(UUID, TEXT) FROM PUBLIC;
COMMIT;
