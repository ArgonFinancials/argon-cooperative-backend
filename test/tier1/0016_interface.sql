-- 0016's INTERFACE: "M0" for suite 0016, the shape with NO RULES: who raised
-- a request and on which act, the society's request window, the act kind,
-- and the operator's function for the window that changes it and nothing
-- else. Applied on top of the real 0001–0015.
BEGIN;
ALTER TABLE approval_requests ADD COLUMN raised_by_user_id UUID REFERENCES users(id);
ALTER TABLE approval_requests ADD COLUMN act_id UUID REFERENCES credential_acts(id);
ALTER TABLE societies ADD COLUMN withdrawal_request_days SMALLINT NOT NULL DEFAULT 7;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request'));

CREATE FUNCTION operator_set_withdrawal_request_days(p_society UUID, p_days INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) RETURNS void SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE societies SET withdrawal_request_days = p_days WHERE id = p_society;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_set_withdrawal_request_days(p_society UUID, p_days INT, p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT) FROM PUBLIC;
COMMIT;
