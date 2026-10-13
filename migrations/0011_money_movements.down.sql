-- Reverses 0011 only while nothing rests on it: once a charge series, a
-- charge line or account of the new kinds, a single movement's act, a payout
-- or a withdrawal request exists, 0011 is part of how history is read. When it
-- refuses, nothing changes, but migrate has already recorded version 10,
-- dirty. The schema is still at 11: recover with `migrate force 11`
-- (deploy/README.md, "migrate says dirty").
--
-- grants.sql names 0011's columns and tables: after going down, apply the
-- grants.sql of the commit before 0011, or 40-migrate.sh's grants step fails.
BEGIN;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM charge_schedules)
     OR EXISTS (SELECT 1 FROM ledger_entries WHERE act_id IS NOT NULL OR request_id IS NOT NULL OR payout_reference IS NOT NULL)
     OR EXISTS (SELECT 1 FROM ledger_lines WHERE charge_role IN ('partner_vat', 'statutory_levy'))
     OR EXISTS (SELECT 1 FROM accounts WHERE role IN ('charges_vat', 'statutory_levy_payable'))
     OR EXISTS (SELECT 1 FROM credential_acts WHERE kind IN ('contribution_record', 'withdrawal_payout'))
     OR EXISTS (SELECT 1 FROM auth_proofs WHERE act_kind IN ('contribution_record', 'withdrawal_payout'))
     OR EXISTS (SELECT 1 FROM societies WHERE account_floor_kobo IS NOT NULL OR withdrawal_signatures_required IS NOT NULL) THEN
    RAISE EXCEPTION '0011 down: refusing — charges, single movements, payouts or withdrawal settings rest on it';
  END IF;
END $$;

DROP TRIGGER signatory_approvals_not_requester ON signatory_approvals;
DROP FUNCTION signature_not_requester();
DROP TRIGGER approval_requests_withdrawal ON approval_requests;
DROP FUNCTION withdrawal_request_admissible();
DROP TRIGGER ledger_entries_settle ON ledger_entries;
DROP FUNCTION ledger_entry_settles();
DROP INDEX entry_act_once;
DROP INDEX withdrawal_posted_once;
DROP TRIGGER ledger_entries_w_withdrawal ON ledger_entries;
DROP FUNCTION withdrawal_names_its_authority();
DROP FUNCTION charge_quote(UUID, movement_kind, BIGINT, DATE);
DROP FUNCTION charge_component(UUID, movement_kind, TEXT, BIGINT, DATE);
DROP TABLE charge_bands;
DROP TABLE charge_schedules;
DROP FUNCTION charge_bands_disjoint();
DROP FUNCTION charge_configuration_immutable();

-- 0004's rule, restored verbatim.
CREATE OR REPLACE FUNCTION ledger_line_role_admissible() RETURNS trigger AS $$
DECLARE
  r TEXT := (SELECT role FROM accounts WHERE id = NEW.account_id);
BEGIN
  IF r IS NULL THEN
    RETURN NEW;   -- no such account: the foreign key refuses it, by name
  END IF;
  IF r IN ('member_savings', 'member_wallet', 'member_loan') AND NEW.member_id IS NULL THEN
    RAISE EXCEPTION 'a line on a % account names the member it belongs to', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_names_member';
  END IF;
  IF NEW.member_id IS NOT NULL AND r NOT IN ('member_savings', 'member_wallet', 'member_loan') THEN
    RAISE EXCEPTION 'a line naming a member belongs on a member account, not a % account', r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'member_line_on_member_account';
  END IF;
  -- Both directions: a charge line on its own charges account, and a line on
  -- a charges account carrying that account's charge. (A line with no charge
  -- role is neither, so coalesce it rather than let NULL compare as unequal.)
  IF (coalesce(NEW.charge_role, '') = 'partner_charge') <> (r = 'charges_partner')
     OR (coalesce(NEW.charge_role, '') = 'argon_margin') <> (r = 'charges_margin') THEN
    RAISE EXCEPTION 'charge role % does not belong on a % account (D-03)', coalesce(NEW.charge_role, 'none'), r
      USING ERRCODE = 'check_violation', CONSTRAINT = 'charge_role_matches_account';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));
ALTER TABLE ledger_entries DROP COLUMN payout_reference;
ALTER TABLE ledger_entries DROP COLUMN request_id;
ALTER TABLE ledger_entries DROP COLUMN act_id;
ALTER TABLE societies DROP COLUMN withdrawal_signatures_required;
ALTER TABLE societies DROP COLUMN account_floor_kobo;
ALTER TABLE accounts DROP CONSTRAINT account_role_known;
ALTER TABLE accounts ADD CONSTRAINT account_role_known CHECK (role IN (
  'member_savings', 'member_wallet', 'member_loan', 'bank',
  'charges_partner', 'charges_margin', 'migration_control', 'general', 'loan_loss', 'loan_recovery_income'));
ALTER TABLE ledger_lines DROP CONSTRAINT ledger_lines_charge_role_check;
ALTER TABLE ledger_lines ADD CONSTRAINT ledger_lines_charge_role_check CHECK (charge_role IN ('principal', 'partner_charge', 'argon_margin'));
COMMIT;
