-- Local development data: LOCAL DOCKER ONLY, never the EC2 database.
-- Ledger rows cannot be deleted, so this lands once in a fresh database:
--   source deploy/lib.sh; pg_as argon_app "$ARGON_DB" -v ON_ERROR_STOP=1 -q < deploy/sql/dev_seed.sql
-- (stdin, not -f: on the docker target psql runs inside the container.)
-- One society, one member with an opening balance and a contribution whose
-- charges are split partner/margin (D-03); every account carries a role (0004).
BEGIN;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM societies) THEN
    RAISE EXCEPTION 'dev_seed: database is not empty; seed a freshly migrated database only';
  END IF;
END $$;

INSERT INTO users (id, full_name) VALUES
  ('b0000000-0000-4000-8000-000000000099', 'Dev Treasurer'),
  ('b0000000-0000-4000-8000-000000000098', 'Dev Member');
INSERT INTO societies (id, code, registered_name, registration_number, state_directorate)
  VALUES ('b0000000-0000-4000-8000-000000000001', 'DEV01', 'Dev Cooperative', 'DEV/1', 'Lagos');
INSERT INTO branches (id, society_id, code, name)
  VALUES ('b0000000-0000-4000-8000-000000000002', 'b0000000-0000-4000-8000-000000000001', 'HQ', 'HQ');
INSERT INTO members (id, society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
  VALUES ('b0000000-0000-4000-8000-000000000003', 'b0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000002',
          'ARG-0142', 'Dev Member', '+234000', 'payroll', '2019-03-01', 'b0000000-0000-4000-8000-000000000098');
INSERT INTO accounts (id, society_id, code, name, kind, role) VALUES
  ('b0000000-0000-4000-8000-000000000011', 'b0000000-0000-4000-8000-000000000001', '1000', 'Bank',                      'asset',     'bank'),
  ('b0000000-0000-4000-8000-000000000012', 'b0000000-0000-4000-8000-000000000001', '2000', 'Member savings',            'liability', 'member_savings'),
  ('b0000000-0000-4000-8000-000000000013', 'b0000000-0000-4000-8000-000000000001', '2100', 'Charges payable - partner', 'liability', 'charges_partner'),
  ('b0000000-0000-4000-8000-000000000014', 'b0000000-0000-4000-8000-000000000001', '2110', 'Charges payable - Argon',   'liability', 'charges_margin'),
  ('b0000000-0000-4000-8000-000000000015', 'b0000000-0000-4000-8000-000000000001', '3900', 'Migration control',         'equity',    'migration_control');

INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo)
  VALUES ('b0000000-0000-4000-8000-000000000031', 'b0000000-0000-4000-8000-000000000001', 'sha256:dev-import',
          'b0000000-0000-4000-8000-000000000099', 1, 48000000);
-- Opening balance carried in from the old register: value_date NULL ("accumulated before migration").
INSERT INTO ledger_entries (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
  VALUES ('b0000000-0000-4000-8000-000000000020', 'b0000000-0000-4000-8000-000000000001', 'b0000000-0000-4000-8000-000000000031',
          'opening_balance', NULL, 'b0000000-0000-4000-8000-000000000099', 'treasurer', 'Treasurer');
INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
  ('b0000000-0000-4000-8000-000000000020', 'b0000000-0000-4000-8000-000000000015',  48000000, 'principal', NULL),
  ('b0000000-0000-4000-8000-000000000020', 'b0000000-0000-4000-8000-000000000012', -48000000, 'principal', 'b0000000-0000-4000-8000-000000000003');
-- ₦25,000 contribution: ₦24,912.50 to savings, ₦50 partner charge, ₦37.50 Argon margin.
INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
  VALUES ('b0000000-0000-4000-8000-000000000021', 'b0000000-0000-4000-8000-000000000001',
          'contribution_in', '2026-09-01', 'b0000000-0000-4000-8000-000000000099', 'treasurer', 'Treasurer');
INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
  ('b0000000-0000-4000-8000-000000000021', 'b0000000-0000-4000-8000-000000000011',  2500000, 'principal',      NULL),
  ('b0000000-0000-4000-8000-000000000021', 'b0000000-0000-4000-8000-000000000012', -2491250, 'principal',      'b0000000-0000-4000-8000-000000000003'),
  ('b0000000-0000-4000-8000-000000000021', 'b0000000-0000-4000-8000-000000000013',    -5000, 'partner_charge', NULL),
  ('b0000000-0000-4000-8000-000000000021', 'b0000000-0000-4000-8000-000000000014',    -3750, 'argon_margin',   NULL);

COMMIT;
