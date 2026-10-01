-- Reverses 0003 only when no credential, session or attributable act exists.
-- Dropping them would erase who could sign in and every record of who reset or
-- changed what. Refuse instead.
BEGIN;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM auth_credentials) OR EXISTS (SELECT 1 FROM auth_sessions)
     OR EXISTS (SELECT 1 FROM credential_acts) THEN
    RAISE EXCEPTION '0003 down: refusing — credentials, sessions or attributable acts exist';
  END IF;
END $$;

DROP TRIGGER members_phone_attributable ON members;
DROP FUNCTION members_phone_attributable();
DROP TRIGGER appointments_mark_sessions ON appointments;
DROP FUNCTION appointments_mark_sessions();

DROP TABLE credential_acts;
DROP FUNCTION credential_act_effect();
DROP FUNCTION credential_act_admissible();
DROP FUNCTION credential_acts_immutable();
DROP FUNCTION auth_reset_person(UUID);

DROP FUNCTION lock_state(UUID);
DROP FUNCTION lock_state_for_credential(TEXT);
DROP FUNCTION lock_state_of(UUID, TEXT);
DROP TABLE sign_in_attempts;
DROP FUNCTION sign_in_attempts_admissible();

DROP FUNCTION session_valid(TEXT);
DROP TABLE auth_sessions;
DROP FUNCTION auth_sessions_admissible();
DROP FUNCTION auth_sessions_end();
DROP FUNCTION auth_sessions_revoke_device();

DROP FUNCTION device_trusted(TEXT);
DROP TABLE auth_devices;
DROP FUNCTION auth_devices_revocation_final();

DROP FUNCTION code_verify(UUID, TEXT);
DROP TABLE auth_codes;
DROP FUNCTION auth_codes_admissible();
DROP FUNCTION otp_destination(UUID);

DROP TABLE auth_proofs;
DROP FUNCTION auth_proofs_admissible();
DROP TABLE auth_credentials;

DROP FUNCTION auth_server_time(timestamptz, TEXT);
DROP FUNCTION auth_refuse(TEXT, TEXT);
DROP FUNCTION officer_over(UUID, UUID);
DROP FUNCTION person_holds_post(UUID, UUID, BOOLEAN, BOOLEAN);
DROP FUNCTION auth_today();
DROP FUNCTION auth_now();

COMMIT;
