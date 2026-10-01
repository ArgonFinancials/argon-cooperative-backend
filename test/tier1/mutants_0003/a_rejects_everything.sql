-- DELIBERATELY WRONG (over-strict). No PIN hash is ever accepted.
--
-- The claim: the hash-format control must be able to FAIL. If it passes
-- here, it is not checking that a valid argon2id hash is accepted, only that
-- the INSERT statement parses.
--
-- Must turn red: TestAuthA_Control_HashesAccepted.
ALTER TABLE auth_credentials
  ADD CONSTRAINT pin_is_argon2id CHECK (false);
