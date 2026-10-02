#!/usr/bin/env bash
# Local development data: LOCAL ONLY (TARGET=local or docker), never the EC2
# database. Ledger rows cannot be deleted, so this lands once, in a freshly
# migrated database, and refuses a database that already has a society.
#
# One society, one member with an opening balance and a contribution whose
# charges are split partner/margin (D-03); every account carries a role (0004);
# the opening-balance batch rests on the Dev Treasurer's step-up act (0005):
# its PIN proof is minted by argon_auth, the only role that can, and spent by
# argon_app's act in the seed's transaction.
#
#   ARGON_ENV=deploy/env/local.env bash deploy/90-seed-dev.sh
source "$(dirname "$0")/lib.sh"
[[ $TARGET != host ]] || die "90-seed-dev.sh is local development data; never the EC2 database"
require_env ARGON_APP_PASSWORD

q() { pg_as argon_app "$ARGON_DB" -At -c "$1" </dev/null; }
[[ $(q "SELECT count(*) FROM societies") == 0 ]] || die "$ARGON_DB already has data; seed a freshly migrated database only"

info "seeding $ARGON_DB"
pg_as argon_app "$ARGON_DB" -q -c "BEGIN; INSERT INTO users (id, full_name) VALUES
  ('b0000000-0000-4000-8000-000000000099', 'Dev Treasurer'),
  ('b0000000-0000-4000-8000-000000000098', 'Dev Member'); COMMIT;" </dev/null >/dev/null
TREASURER="'b0000000-0000-4000-8000-000000000099'" BATCH="'b0000000-0000-4000-8000-000000000031'"
PROOF="'b0000000-0000-4000-8000-000000000073'" ACT="'b0000000-0000-4000-8000-000000000074'"
if [[ $(q "SELECT count(*) FROM information_schema.columns WHERE table_name = 'ledger_batches' AND column_name = 'authorised_by'") == 1 ]]; then
  require_env ARGON_AUTH_PASSWORD
  pg_as argon_auth "$ARGON_DB" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES ($PROOF, $TREASURER, 'pin', 'ledger_batch');" </dev/null >/dev/null
  BATCH_SQL="INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($ACT, 'ledger_batch', $TREASURER, $PROOF);
INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
  VALUES ($BATCH, 'b0000000-0000-4000-8000-000000000001', 'sha256:dev-import', $TREASURER, 1, 48000000, $ACT);"
else
  BATCH_SQL="INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo)
  VALUES ($BATCH, 'b0000000-0000-4000-8000-000000000001', 'sha256:dev-import', $TREASURER, 1, 48000000);"
fi
pg_as argon_app "$ARGON_DB" -v ON_ERROR_STOP=1 -q <<SQL >/dev/null
BEGIN;
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

$BATCH_SQL
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
SQL
ok "seeded: $(q "SELECT count(*) FROM ledger_lines") lines summing to $(q "SELECT sum(amount_kobo) FROM ledger_lines")"
