-- 0024's INTERFACE: "M0" for suite 0024. The enrolment code's table, its act
-- kind, the operator's issuing function and the redemption, with NO RULES:
-- any row is written as given; the operator's function inserts what it is
-- handed; a redemption returns any code of the person's whose digest matches
-- (used, voided or expired alike) and counts nothing. Applied on top of the
-- real 0001–0023.
BEGIN;
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue'));

CREATE TABLE enrolment_codes (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id            UUID NOT NULL REFERENCES users(id),
  member_id          UUID NOT NULL REFERENCES members(id),
  code_digest        TEXT NOT NULL,
  issued_at          TIMESTAMPTZ NOT NULL DEFAULT auth_now(),
  expires_at         TIMESTAMPTZ NOT NULL DEFAULT auth_now() + interval '30 days',
  issued_by_act      UUID REFERENCES credential_acts(id),
  issued_by_operator TEXT,
  operator_login     TEXT,
  operator_reason    TEXT,
  replaces_id        UUID REFERENCES enrolment_codes(id),
  wrong_tries        SMALLINT NOT NULL DEFAULT 0,
  used_at            TIMESTAMPTZ,
  voided_at          TIMESTAMPTZ,
  void_reason        TEXT
);

CREATE FUNCTION operator_issue_enrolment(p_member UUID, p_digest TEXT, p_operator TEXT, p_login TEXT, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_operator, operator_login, operator_reason)
    SELECT user_id, id, p_digest, p_operator, p_login, p_reason FROM members WHERE id = p_member
    RETURNING id INTO v_id;
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_issue_enrolment(UUID, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;

CREATE FUNCTION enrolment_redeem(p_user UUID, p_digest TEXT) RETURNS uuid
  SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT id FROM enrolment_codes WHERE user_id = p_user AND code_digest = p_digest ORDER BY issued_at DESC LIMIT 1;
$$ LANGUAGE sql VOLATILE;
REVOKE EXECUTE ON FUNCTION enrolment_redeem(UUID, TEXT) FROM PUBLIC;
COMMIT;
