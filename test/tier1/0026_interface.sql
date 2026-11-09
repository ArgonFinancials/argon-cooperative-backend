-- 0026's INTERFACE: "M0" for suite 0026. The halves' columns and the server's
-- assembly of a completed code (so the controls can redeem one), with NO
-- RULES: anyone's act issues a whole code or a half, any shape is accepted, a
-- half can be set or changed at will, and the redemption (0025's) counts a
-- try on a code waiting for its second half. Applied on top of the real
-- 0001–0025.
BEGIN;
ALTER TABLE enrolment_codes ALTER COLUMN code_digest DROP NOT NULL;
ALTER TABLE enrolment_codes ADD COLUMN first_half_digest TEXT, ADD COLUMN second_half_digest TEXT,
  ADD COLUMN second_by_act UUID REFERENCES credential_acts(id);
CREATE FUNCTION enrolment_code_assemble() RETURNS trigger AS $$
BEGIN
  IF NEW.second_half_digest IS NOT NULL AND NEW.first_half_digest IS NOT NULL THEN
    NEW.code_digest := encode(sha256(convert_to(least(NEW.first_half_digest, NEW.second_half_digest)
                                             || greatest(NEW.first_half_digest, NEW.second_half_digest), 'UTF8')), 'hex');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER enrolment_code_zz_assemble BEFORE UPDATE ON enrolment_codes
  FOR EACH ROW EXECUTE FUNCTION enrolment_code_assemble();
COMMIT;
