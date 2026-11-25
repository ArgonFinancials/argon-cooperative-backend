BEGIN;
CREATE OR REPLACE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h withdrawal_holds%ROWTYPE;
BEGIN
  SELECT * INTO h FROM withdrawal_holds WHERE id = NEW.hold_id FOR UPDATE;   -- one transfer decision at a time per hold
  IF NEW.reference IS DISTINCT FROM vfd_transfer_reference(h.id) THEN
    PERFORM auth_refuse('vfd_reference_is_the_holds', format('a transfer''s reference is derived from its hold: %s, not %s', vfd_transfer_reference(h.id), NEW.reference));
  END IF;
  IF NEW.amount_kobo IS DISTINCT FROM h.amount_kobo THEN
    PERFORM auth_refuse('vfd_transfer_is_the_holds', format('a transfer moves what was held: %s kobo, not %s', h.amount_kobo, NEW.amount_kobo));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE member_id = h.member_id AND account_number = NEW.to_account) THEN
    PERFORM auth_refuse('vfd_transfer_to_members_account', 'a payout goes into the member''s own VFD account and no other (§12, 2026-11-10); this member has none, or this is not it');
  END IF;
  -- From the pool (0039, §1.1): every transfer leaves Argon's VFD pool, the
  -- account VFD's enquiry names; a society's account holds nothing (VFD sweeps
  -- its deposits into the pool).
  IF NOT EXISTS (SELECT 1 FROM vfd_pool WHERE account_number = NEW.from_account) THEN
    PERFORM auth_refuse('vfd_transfer_from_pool', 'a payout leaves from Argon''s VFD pool, the account VFD''s enquiry names (§1.1); none is recorded, or this is not it');
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id) THEN
    PERFORM auth_refuse('vfd_transfer_on_open_hold', 'a transfer is sent on an open hold: this one was released or paid');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS vfd_transfers_to_bank ON vfd_transfers;
DROP FUNCTION IF EXISTS vfd_transfer_to_bank();
ALTER TABLE vfd_transfers DROP COLUMN IF EXISTS to_bank;
DROP TRIGGER IF EXISTS signatory_approvals_bank_setter ON signatory_approvals;
DROP FUNCTION IF EXISTS signature_not_bank_setter();
DROP TRIGGER IF EXISTS withdrawal_holds_bank ON withdrawal_holds;
DROP FUNCTION IF EXISTS withdrawal_hold_bank();
DROP TRIGGER IF EXISTS approval_requests_bank ON approval_requests;
DROP FUNCTION IF EXISTS withdrawal_request_bank();
DROP FUNCTION IF EXISTS society_on_vfd(UUID);
DROP VIEW IF EXISTS member_bank_current;
DROP TABLE IF EXISTS bank_name_confirmations;
DROP FUNCTION IF EXISTS bank_name_confirmation_admissible();
DROP TABLE IF EXISTS bank_name_checks;
DROP FUNCTION IF EXISTS bank_name_check_admissible();
DROP TABLE IF EXISTS member_bank_accounts;
DROP FUNCTION IF EXISTS member_bank_account_admissible();
DROP FUNCTION IF EXISTS names_resemble(TEXT, TEXT);
DROP FUNCTION IF EXISTS name_words(TEXT);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status'));
COMMIT;
