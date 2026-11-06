-- 0025's INTERFACE: "M0" for suite 0025. The code's device column and the
-- three-argument redemption with NO RULES: it returns any code of the
-- person's whose digest matches, trusts and binds no device, never says a
-- slip was used. The two-argument redemption is left in place. Applied on
-- top of the real 0001–0024.
BEGIN;
ALTER TABLE enrolment_codes ADD COLUMN used_device_id UUID REFERENCES auth_devices(id);
CREATE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT, p_device_digest TEXT,
                                 OUT code_id UUID, OUT device_id UUID, OUT already_used BOOLEAN)
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT id, NULL::uuid, false FROM enrolment_codes WHERE user_id = p_user AND code_digest = p_digest
   ORDER BY issued_at DESC LIMIT 1;
$$ LANGUAGE sql VOLATILE;
REVOKE EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT, TEXT) FROM PUBLIC;
COMMIT;
