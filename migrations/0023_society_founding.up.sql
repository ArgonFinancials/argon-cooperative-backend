-- =============================================================================
-- ARGON — 0023: A SOCIETY IS FOUNDED BY THE OPERATOR, ON ITS FOUNDING
-- RESOLUTION, ON THE STANDARD CHART
--
-- Written against test/tier1/tier1_0023_test.go (2026-11-06; red against the
-- interface first, every rule a mutant of its own). LEDGER_CONTRACT.md §12.
--
-- operator_create_society, callable only by the owner's role (the operator's):
--
--   WHO AND ON WHOSE DECISION, as every operator act (0014): a named operator,
--   the server login, a reason, and the society's FOUNDING RESOLUTION (minutes
--   reference and date, never in the future). The same refusals, by the same
--   names.
--   A REGISTERED SOCIETY: name, registration number, state directorate, none
--   blank (NOT NULL alone accepts an empty string).
--   THE FOUNDING RESOLUTION SETS ALL THREE QUORUMS (withdrawals, write-offs,
--   other governed acts), each at least 2, never a column default: stated,
--   attributable, never inherited. Each is logged with the resolution; so is
--   the founding itself.
--   THE STANDARD CHART: one account per posting role, the same for every
--   society, copied from standard_chart. A society adds its own 'general'
--   accounts after. Changing the standard is a migration, reviewed.
--   A HEAD OFFICE: a member cannot exist without a branch.
--   ALL OR NOTHING: one function, one transaction; a refusal leaves nothing.
--
-- NOT HERE, ON PURPOSE: the backup gate (BACKUP_STORE s3:// and a passing S3
-- drill within 8 days). It is deploy/46-create-society.sh's. The database
-- could only know through a row someone writes, and the owner can write that
-- row by hand: a check satisfied by asserting the thing it checks is not a
-- check. Do not add a database half thinking it strengthens anything.
-- =============================================================================

BEGIN;
CREATE TABLE standard_chart (
  code TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind account_kind NOT NULL,
  role TEXT NOT NULL UNIQUE
);
INSERT INTO standard_chart (code, name, kind, role) VALUES
  ('1000', 'Bank',                    'asset',     'bank'),
  ('1201', 'Member loans',            'asset',     'member_loan'),
  ('2000', 'Member savings',          'liability', 'member_savings'),
  ('2101', 'Partner charges payable', 'liability', 'charges_partner'),
  ('2102', 'VAT payable',             'liability', 'charges_vat'),
  ('2103', 'Statutory levy payable',  'liability', 'statutory_levy_payable'),
  ('2111', 'Argon charges payable',   'liability', 'charges_margin'),
  ('2201', 'Member wallets',          'liability', 'member_wallet'),
  ('3901', 'Migration control',       'equity',    'migration_control'),
  ('4901', 'Loan recoveries',         'income',    'loan_recovery_income'),
  ('5901', 'Loan losses',             'expense',   'loan_loss');

CREATE FUNCTION operator_create_society(p_code TEXT, p_name TEXT, p_reg TEXT, p_directorate TEXT,
    p_withdrawal INT, p_write_off INT, p_signatures INT,
    p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  IF p_operator IS NULL OR btrim(p_operator) = '' THEN PERFORM auth_refuse('operator_names_themselves', 'an operator names themselves: someone with shell access is not attribution'); END IF;
  IF p_login IS NULL OR btrim(p_login) = '' THEN PERFORM auth_refuse('operator_login_recorded', 'the server login that ran this is recorded beside the operator'); END IF;
  IF p_reason IS NULL OR btrim(p_reason) = '' THEN PERFORM auth_refuse('operator_gives_reason', 'founding a society says why'); END IF;
  IF p_ref IS NULL OR btrim(p_ref) = '' OR p_date IS NULL THEN PERFORM auth_refuse('society_resolution_required', 'the operator carries out the society''s founding decision: name its resolution (minutes reference and date). Without it, an Argon staff member would be the author of a society''s governance'); END IF;
  IF p_date > current_date THEN PERFORM auth_refuse('society_resolution_not_future', 'a resolution is passed before it is carried out; this one is dated in the future'); END IF;
  IF btrim(coalesce(p_name, '')) = '' OR btrim(coalesce(p_reg, '')) = '' OR btrim(coalesce(p_directorate, '')) = '' THEN
    PERFORM auth_refuse('society_is_registered', 'a society is registered: its registered name, its registration number under the Cooperative Societies Act and its state directorate, none of them blank');
  END IF;
  IF p_withdrawal IS NULL OR p_withdrawal < 2 OR p_withdrawal > 9 THEN
    PERFORM auth_refuse('withdrawal_quorum_at_least_two', 'a quorum of one is no quorum: it makes withdrawals unilateral, the single risk PRD 8.4 exists to prevent. A society that wants single-signature withdrawals is a conversation with Argon about whether this platform suits it, not a setting');
  END IF;
  IF p_write_off IS NULL OR p_write_off < 2 OR p_write_off > 9 THEN
    PERFORM auth_refuse('write_off_quorum_at_least_two', 'a write-off quorum of one would let one person decide a debt will not be recovered: the easiest way to make money disappear from a society''s books while leaving a legitimate-looking record. A write-off needs two people');
  END IF;
  IF p_signatures IS NULL OR p_signatures < 2 OR p_signatures > 9 THEN
    PERFORM auth_refuse('signatures_required_at_least_two', 'a signature count of one is no quorum: every act it governs would be unilateral');
  END IF;

  INSERT INTO societies (code, registered_name, registration_number, state_directorate,
      withdrawal_signatures_required, write_off_signatures_required, signatures_required)
    VALUES (p_code, btrim(p_name), btrim(p_reg), btrim(p_directorate), p_withdrawal, p_write_off, p_signatures)
    RETURNING id INTO v_id;
  INSERT INTO branches (society_id, code, name) VALUES (v_id, 'HQ', 'Head office');
  INSERT INTO accounts (society_id, code, name, kind, role)
    SELECT v_id, code, name, kind, role FROM standard_chart;

  INSERT INTO society_settings_log (society_id, setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date, reason)
  VALUES (v_id, 'founded',                        NULL, p_code,              p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'withdrawal_signatures_required', NULL, p_withdrawal::text,  p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'write_off_signatures_required',  NULL, p_write_off::text,   p_operator, p_login, p_ref, p_date, p_reason),
         (v_id, 'signatures_required',            NULL, p_signatures::text,  p_operator, p_login, p_ref, p_date, p_reason);
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_create_society(TEXT, TEXT, TEXT, TEXT, INT, INT, INT, TEXT, TEXT, TEXT, DATE, TEXT) FROM PUBLIC;
COMMIT;
