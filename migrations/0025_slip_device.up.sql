-- =============================================================================
-- ARGON — 0025: A REDEEMED CODE NAMES THE DEVICE IT TRUSTED; A USED SLIP SAYS
-- SO TO ITS HOLDER
--
-- Written against test/tier1/tier1_0025_test.go (2026-11-06; red against the
-- interface first). LEDGER_CONTRACT.md §12: a slip is a bearer credential,
-- and whoever holds another's slip can enrol as them (accepted residual). The
-- controls are attribution and handing it over in person. This makes the
-- attribution complete:
--
--   THE REDEMPTION TRUSTS THE DEVICE AND THE CODE NAMES IT, in one act: the
--   device is created only by a right, live, unexpired code, and the code
--   records it with the time. A bound device never changes.
--   A USED SLIP SAYS SO, TO ITS HOLDER ONLY: when the code typed is a code of
--   the person's that was already used. A wrong code never says it, so a
--   stranger without the slip learns nothing; telling costs no try.
--   THE TWO-ARGUMENT REDEMPTION (0024), WHICH TRUSTED NO DEVICE, IS DROPPED:
--   a path that trusts a device without recording it is the gap this closes.
-- =============================================================================

BEGIN;
ALTER TABLE enrolment_codes ADD COLUMN used_device_id UUID REFERENCES auth_devices(id);
DROP FUNCTION enrolment_redeem(UUID, TEXT);

-- 0024's rule, with the device: set once, with used_at, and never again.
CREATE OR REPLACE FUNCTION enrolment_code_append_only() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code''s record is never removed');
  END IF;
  IF (NEW.id, NEW.user_id, NEW.member_id, NEW.code_digest, NEW.issued_at, NEW.expires_at, NEW.issued_by_act,
      NEW.issued_by_operator, NEW.operator_login, NEW.operator_reason, NEW.replaces_id)
     IS DISTINCT FROM
     (OLD.id, OLD.user_id, OLD.member_id, OLD.code_digest, OLD.issued_at, OLD.expires_at, OLD.issued_by_act,
      OLD.issued_by_operator, OLD.operator_login, OLD.operator_reason, OLD.replaces_id)
     OR OLD.used_at IS NOT NULL OR OLD.voided_at IS NOT NULL
     OR NEW.wrong_tries NOT IN (OLD.wrong_tries, OLD.wrong_tries + 1)
     OR (NEW.used_at IS NOT NULL AND NEW.voided_at IS NOT NULL)
     OR (NEW.used_device_id IS NOT NULL) <> (NEW.used_at IS NOT NULL) THEN
    PERFORM auth_refuse('enrolment_code_append_only', 'an enrolment code only moves forward: tried, used (with the device it trusted) or voided, once, while live');
  END IF;
  IF NEW.wrong_tries >= 5 AND NEW.voided_at IS NULL THEN
    NEW.voided_at := auth_now(); NEW.void_reason := 'wrong_tries';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

-- The person's live code. Right, inside 30 days: the device is trusted and the
-- code names it. The slip already used, typed again: already_used, nothing
-- counted, nothing trusted. Otherwise a wrong try on the live code (the fifth
-- voids it). Never a sign-in attempt: the tries are the code's.
CREATE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT, p_device_digest TEXT,
                                 OUT code_id UUID, OUT device_id UUID, OUT already_used BOOLEAN)
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c    enrolment_codes%ROWTYPE;
  live BOOLEAN;
BEGIN
  already_used := false;
  SELECT * INTO c FROM enrolment_codes WHERE user_id = p_user AND used_at IS NULL AND voided_at IS NULL FOR UPDATE;
  live := FOUND AND auth_now() < c.expires_at;
  IF live AND c.code_digest = p_digest THEN
    INSERT INTO auth_devices (user_id, token_digest) VALUES (p_user, p_device_digest) RETURNING id INTO device_id;
    UPDATE enrolment_codes SET used_at = auth_now(), used_device_id = device_id WHERE id = c.id;
    code_id := c.id;
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM enrolment_codes WHERE user_id = p_user AND used_at IS NOT NULL AND code_digest = p_digest) THEN
    already_used := true;
    RETURN;
  END IF;
  IF live THEN
    UPDATE enrolment_codes SET wrong_tries = wrong_tries + 1 WHERE id = c.id;
  END IF;
END $$ LANGUAGE plpgsql VOLATILE;
REVOKE EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT, TEXT) FROM PUBLIC;
COMMIT;
