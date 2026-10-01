-- DELIBERATELY WRONG. "The PIN is hashed" checked as "it looks like a hash".
--
-- The claim: a check for a leading '$' refuses the plaintext case everyone
-- thinks of, and accepts bcrypt, md5-crypt, or anything else in PHC-ish form.
-- The rule is argon2id specifically: the parameters are part of the
-- security, and a weaker scheme slipping in stays invisible.
--
-- Must turn red: TestAuthA1_PinHashMustBeArgon2id.
-- Expected to stay green: TestAuthA1_PinNeverStoredInPlaintext.
ALTER TABLE auth_credentials
  ADD CONSTRAINT pin_is_argon2id CHECK (pin_hash LIKE '$%');
