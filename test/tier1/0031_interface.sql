-- 0031's INTERFACE: "M0" for suite 0031. The register's shape with NO RULES:
-- register_member writes the person and the member for anyone, on any act or
-- none, any phone, any branch, any date, pending; next of kin in any part;
-- nothing attributed. Applied on top of the real 0001–0030.
BEGIN;

ALTER TABLE members ADD COLUMN next_of_kin_name TEXT;
ALTER TABLE members ADD COLUMN next_of_kin_phone TEXT;
ALTER TABLE members ADD COLUMN next_of_kin_relationship TEXT;
ALTER TABLE members ADD COLUMN registered_by_act UUID REFERENCES credential_acts(id);
ALTER TABLE members ADD COLUMN register_batch_id UUID;

CREATE TABLE member_register_batches (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id    UUID NOT NULL REFERENCES societies(id),
  reference     TEXT NOT NULL,
  act_id        UUID REFERENCES credential_acts(id),
  row_count     INT NOT NULL,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register'));

CREATE FUNCTION register_member(p_society UUID, p_branch_code TEXT, p_number TEXT, p_name TEXT, p_phone TEXT, p_joined DATE,
    p_channel collection_channel, p_nok_name TEXT, p_nok_phone TEXT, p_nok_relationship TEXT, p_act UUID, p_batch UUID)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE v_user UUID; v_member UUID; v_branch UUID;
BEGIN
  SELECT id INTO v_branch FROM branches WHERE society_id = p_society ORDER BY code LIMIT 1;
  INSERT INTO users (full_name) VALUES (coalesce(p_name, '')) RETURNING id INTO v_user;
  INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id,
                       next_of_kin_name, next_of_kin_phone, next_of_kin_relationship)
    VALUES (p_society, v_branch, coalesce(p_number, ''), coalesce(p_name, ''), coalesce(p_phone, ''), p_channel, coalesce(p_joined, current_date), v_user,
            p_nok_name, p_nok_phone, p_nok_relationship)
    RETURNING id INTO v_member;
  RETURN v_member;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION register_member(UUID, TEXT, TEXT, TEXT, TEXT, DATE, collection_channel, TEXT, TEXT, TEXT, UUID, UUID) FROM PUBLIC;

COMMIT;
