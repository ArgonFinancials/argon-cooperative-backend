#!/usr/bin/env bash
# Tier 1 probe. Tries to break the database's own constraints and reports
# whether the database refused.
#
#   70-verify-tier1.sh              build a scratch argon_verify_<ts> from the
#                                   migrations, probe it, drop it
#   70-verify-tier1.sh --db NAME    probe an existing scratch/restored copy
#   70-verify-tier1.sh --keep       keep the scratch database afterwards
#
# NEVER point --db at the live database: probe fixtures land in ledger tables,
# and ledger rows cannot be deleted. That is the point.
#
# A case counts as REFUSED only when the SPECIFIC expected error appears (by
# SQLSTATE + message/constraint). A refusal for any other reason — a typo, a
# foreign key, a missing fixture — is WRONG ERROR, not a pass. Each refusal
# case has a CONTROL that must succeed, proving the statement shape is valid
# and only the constraint under test stops it.
#
# Exit 1 if any REQUIRED case is not refused as expected. GAP cases are
# reported (they probe edges of the constraints) but do not fail the run.
source "$(dirname "$0")/lib.sh"
require_env ARGON_APP_PASSWORD ARGON_MIGRATOR_PASSWORD

db=""; keep=0
while (($#)); do case $1 in
  --db) db=$2; shift 2 ;;
  --keep) keep=1; shift ;;
  *) die "unknown arg $1" ;;
esac; done
[[ -z $db || $db =~ ^argon_(drill|verify)_ ]] || die "--db must be a scratch database (argon_drill_* / argon_verify_*), never '$db'"

if [[ -z $db ]]; then
  db="argon_verify_$(date -u +%Y%m%dt%H%M%S)"
  info "building scratch database $db from $MIGRATIONS_DIR"
  create_scratch_db "$db"
  ((keep)) || trap 'drop_scratch_db "$db" >/dev/null || true' EXIT
  run_migrate "$db" up >&2
  apply_grants "$db"
fi

# ---- fixtures (as argon_app, one transaction) --------------------------------
U() { printf "'a0000000-0000-4000-8000-%012d'" "$1"; }
S=$(U 1) B=$(U 2) M1=$(U 11) M2=$(U 12) M3=$(U 13) A1=$(U 21) A2=$(U 22)
BT=$(U 31) E1=$(U 41) EBAD=$(U 42) EOK=$(U 43) EZERO=$(U 44) L1=$(U 51) L2=$(U 52)
R1=$(U 61) ACTOR=$(U 91) U1=$(U 81) U2=$(U 82) U3=$(U 83)

# Written for the schema at migration 0004 (users, line-level member_id, no
# ledger_batches.as_of; account roles, so every savings line names its member).
# M1 and M3 hold current signing posts; M2 is auditor.
info "seeding fixtures in $db as argon_app"
pg_as argon_app "$db" -q <<SQL >/dev/null
BEGIN;
INSERT INTO users (id, full_name) VALUES
  ($ACTOR, 'Probe Actor'), ($U1, 'Probe Treasurer'), ($U2, 'Probe Auditor'), ($U3, 'Probe Chairman');
INSERT INTO societies (id, code, registered_name, registration_number, state_directorate)
  VALUES ($S, 'ZZPROBE', 'Probe Cooperative', 'PROBE/0001', 'Lagos');
INSERT INTO branches (id, society_id, code, name) VALUES ($B, $S, 'HQ', 'Head office');
INSERT INTO members (id, society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id) VALUES
  ($M1, $S, $B, 'P-001', 'Probe Treasurer', '+2340000000001', 'payroll', '2020-01-01', $U1),
  ($M2, $S, $B, 'P-002', 'Probe Auditor',   '+2340000000002', 'payroll', '2020-01-01', $U2),
  ($M3, $S, $B, 'P-003', 'Probe Chairman',  '+2340000000003', 'cash',    '2020-01-01', $U3);
INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from) VALUES
  ($S, $M1, 'treasurer', true, '2020-01-01'),
  ($S, $M3, 'chairman',  true, '2020-01-01');
INSERT INTO accounts (id, society_id, code, name, kind, role) VALUES
  ($A1, $S, '1000', 'Cash at VFD', 'asset', 'bank'),
  ($A2, $S, '2000', 'Member savings', 'liability', 'member_savings');
INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo)
  VALUES ($BT, $S, 'sha256:probe', $ACTOR, 1, 10000);
INSERT INTO ledger_entries (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
  VALUES ($E1, $S, $BT, 'contribution_in', '2026-01-31', $ACTOR, 'treasurer', 'Treasurer');
INSERT INTO ledger_lines (id, entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
  ($L1, $E1, $A1,  10000, 'principal', NULL),
  ($L2, $E1, $A2, -10000, 'principal', $M1);
INSERT INTO approval_requests (id, society_id, reference, member_id, kind, amount_kobo, required, expires_at)
  VALUES ($R1, $S, 'APR-PROBE-1', $M1, 'withdrawal_out', 50000000, 2, now() + interval '1 day');
INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M1, 'treasurer');
COMMIT;
SQL
n=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM ledger_lines WHERE entry_id = $E1" </dev/null)
[[ $n == 2 ]] || die "fixtures not in place (ledger_lines for E1 = '$n') — every result below would be meaningless"
ok "fixtures committed: 1 posted entry with 2 lines, 1 approval request with 1 signature"

# ---- harness -----------------------------------------------------------------
declare -a ROWS; req_fail=0; gaps=0
run_sql() {  # role sql -> sets OUT, RC
  local role=$1 sql=$2
  RC=0
  OUT=$(printf '\\set VERBOSITY verbose\n%s\n' "$sql" | pg_as "$role" "$db" -q 2>&1) || RC=$?
}
first_error() { grep -m1 -E '^(psql:.*)?ERROR' <<<"$OUT" | sed -E 's/^psql:[^E]*//' | cut -c1-110; }

# probe CLASS ID ROLE EXPECT_ERE DESCRIPTION SQL [ALSO_REQUIRE_ERE]
#   CLASS: req (must be refused) | gap (should be refused; reported) | ctl (must SUCCEED)
probe() {
  local class=$1 id=$2 role=$3 expect=$4 desc=$5 sql=$6 also=${7:-}
  run_sql "$role" "$sql"
  local verdict detail
  if [[ $class == ctl ]]; then
    if ((RC == 0)); then verdict="OK        ✓"; detail="control committed"
    else verdict="CTL FAIL  ?"; detail="control should succeed: $(first_error)"; req_fail=1; fi
  # Matched against the whole report on one line: psql prints a RAISE's
  # constraint name on its own "CONSTRAINT NAME:" line, apart from the SQLSTATE.
  elif ((RC != 0)) && grep -qE "$expect" <<<"$(tr '\n' ' ' <<<"$OUT")" && { [[ -z $also ]] || grep -qE "$also" <<<"$OUT"; }; then
    verdict="REFUSED   ✓"; detail=$(first_error)
  elif ((RC != 0)); then
    verdict="WRONG ERR ?"; detail="$(first_error)"; [[ $class == req ]] && req_fail=1
  else
    if [[ $class == req ]]; then verdict="SUCCEEDED ✗ DECORATIVE"; req_fail=1
    else verdict="ACCEPTED  ✗ GAP"; gaps=$((gaps+1)); fi
    detail="statement committed without error"
  fi
  ROWS+=("$(printf '%-4s %-3s %-14s %-23s %s\n                                              → %s' "$id" "$class" "$role" "$verdict" "$desc" "$detail")")
  printf '%-4s %-24s %s\n' "$id" "$verdict" "$desc" >&2
}

OWNER=argon_migrator   # lands in argon_owner on connect

info "probing $db"

# == Your five cases ===========================================================
# 1–2. Immutability at the TRIGGER layer. Run as the owner, who has UPDATE and
# DELETE privileges, so nothing but the trigger stands in the way.
probe req T1 $OWNER 'P0001.*immutable: UPDATE on ledger_entries' "UPDATE a posted ledger_entries row" \
  "UPDATE ledger_entries SET reason = 'probe: rewriting a posted entry' WHERE id = $E1;"
probe req T2 $OWNER 'P0001.*immutable: UPDATE on ledger_lines'   "UPDATE a posted ledger_lines amount" \
  "UPDATE ledger_lines SET amount_kobo = 99999 WHERE id = $L1;"
probe req T3 $OWNER 'P0001.*immutable: UPDATE on ledger_batches' "UPDATE a posted ledger_batches total" \
  "UPDATE ledger_batches SET total_kobo = 0 WHERE id = $BT;"
probe req T4 $OWNER 'P0001.*immutable: DELETE on ledger_entries' "DELETE a posted ledger_entries row" \
  "DELETE FROM ledger_entries WHERE id = $E1;"
probe req T5 $OWNER 'P0001.*immutable: DELETE on ledger_lines'   "DELETE a posted ledger_lines row" \
  "DELETE FROM ledger_lines WHERE id = $L1;"
probe req T6 $OWNER 'P0001.*immutable: DELETE on ledger_batches' "DELETE a posted ledger_batches row" \
  "DELETE FROM ledger_batches WHERE id = $BT;"

# 3. Balance, enforced at COMMIT. The marker proves both INSERTs succeeded and
# the refusal came from COMMIT — i.e. the deferred trigger, not an early check.
probe req C3 argon_app "P0001.*entry a0000000-0000-4000-8000-000000000042 does not balance: debits minus credits = 100 kobo" \
  "entry whose lines sum to +100 kobo fails at COMMIT" \
  "BEGIN;
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
     VALUES ($EBAD, $S, 'contribution_in', '2026-02-01', $ACTOR, 'treasurer', 'Treasurer');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($EBAD, $A1,  10000, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($EBAD, $A2,  -9900, 'principal', $M1);
   \\echo PROBE_MARKER_LINES_INSERTED_BEFORE_COMMIT
   COMMIT;" 'PROBE_MARKER_LINES_INSERTED_BEFORE_COMMIT'
probe req C3b argon_app 'P0001.*C3b-entry-persisted' "…and the unbalanced entry left nothing behind" \
  "DO \$\$ BEGIN IF (SELECT count(*) FROM ledger_entries WHERE id = $EBAD) + (SELECT count(*) FROM ledger_lines WHERE entry_id = $EBAD) > 0
     THEN RAISE NOTICE 'persisted'; ELSE RAISE EXCEPTION 'C3b-entry-persisted: none (this refusal is the pass)'; END IF; END \$\$;"
probe ctl K3 argon_app '' "control: the same entry balanced (+10000/−10000) commits" \
  "BEGIN;
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
     VALUES ($EOK, $S, 'contribution_in', '2026-02-01', $ACTOR, 'treasurer', 'Treasurer');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($EOK, $A1,  10000, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($EOK, $A2, -10000, 'principal', $M1);
   COMMIT;"

# 4. Internal auditor as signatory.
probe req C4 argon_app '23514.*auditor_never_signs' "appoint an internal_auditor with is_signatory = true" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M2, 'internal_auditor', true, '2026-01-01');"
probe ctl K4 argon_app '' "control: same appointment with is_signatory = false commits" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M2, 'internal_auditor', false, '2026-01-01');"

# 5. One member, one signature per request.
probe req C5 argon_app '23505.*signatory_approvals_one_per_user' "same member signs the same approval request twice" \
  "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M1, 'treasurer');"
probe req C5b argon_app '23505.*signatory_approvals_one_per_user' "…twice, under a different post label" \
  "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M1, 'chairman');"
probe ctl K5 argon_app '' "control: a different person, the current chairman, signs the same request" \
  "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M3, 'chairman');"

# == Privilege layer: what argon_app cannot even attempt =======================
probe req P1 argon_app '42501.*permission denied for table ledger_entries' "app UPDATE on ledger_entries" \
  "UPDATE ledger_entries SET reason = 'probe tamper' WHERE id = $E1;"
probe req P2 argon_app '42501.*permission denied for table ledger_lines' "app DELETE on ledger_lines" \
  "DELETE FROM ledger_lines WHERE id = $L1;"
probe req P3 argon_app '42501.*permission denied for table ledger_lines' "app TRUNCATE ledger_lines" \
  "TRUNCATE ledger_lines;"
probe req P4 argon_app '42501.*must be owner of table ledger_entries' "app DISABLE TRIGGER on ledger_entries" \
  "ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable;"
probe req P5 argon_app '42501.*permission denied to set parameter "session_replication_role"' "app SET session_replication_role = replica" \
  "SET session_replication_role = replica;"
probe req P6 argon_app '42501.*permission denied for table wallet_reconciliations' "app rewrites a reconciliation difference" \
  "UPDATE wallet_reconciliations SET difference_kobo = 0;"
probe req P7 argon_app '42501.*permission denied for table signatory_approvals' "app edits a signature (post/time frozen at signing, §4)" \
  "UPDATE signatory_approvals SET post_at_signing = 'chairman' WHERE request_id = $R1 AND member_id = $M1;"
probe req P8 argon_app '42501.*permission denied for table appointments' "app back-dates an appointment (handover must not rewrite, §4.3)" \
  "UPDATE appointments SET held_from = '2020-01-01' WHERE member_id = $M2;"
probe ctl K8 argon_app '' "control: app closes an appointment (sets held_until)" \
  "UPDATE appointments SET held_until = '2099-12-31' WHERE member_id = $M2 AND post = 'internal_auditor';"

probe req P9 argon_app '42501.*permission denied for table ledger_entries' "app supplies posted_at (no INSERT privilege on the column)"   "INSERT INTO ledger_entries (society_id, kind, value_date, posted_at, actor_user_id, actor_post_label)
     VALUES ($S, 'contribution_in', '2026-02-01', '2099-01-01', $ACTOR, 'Treasurer');"
probe req P10 $OWNER '23514.*CONSTRAINT NAME: +ledger_entries_posted_at_is_now' "owner supplies posted_at (refused for every role)"   "INSERT INTO ledger_entries (society_id, kind, value_date, posted_at, actor_user_id, actor_post_label)
     VALUES ($S, 'contribution_in', '2026-02-01', '2099-01-01', $ACTOR, 'Treasurer');"

# == The step-up's ROLE half (0005): the path that uses a proof cannot mint it ==
# The PIN is checked in Go, not in the database, so whoever can INSERT a PIN
# proof can skip the PIN. The API's handlers run as argon_app, which must not
# be able to mint a proof or record a sign-in attempt (a forged success would
# end a lockout run). Only argon_auth, the auth service's role, can; and
# argon_auth can neither use a proof (credential_acts) nor post (ledger_*).
probe req P11 argon_app '42501.*permission denied for table auth_proofs' "app mints a PIN proof without a PIN" \
  "INSERT INTO auth_proofs (user_id, method) VALUES ($U1, 'pin');"
probe req P12 argon_app '42501.*permission denied for table sign_in_attempts' "app records a sign-in success (would end a lockout run)" \
  "INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($U1, repeat('a', 64), true);"
if [[ -n ${ARGON_AUTH_PASSWORD:-} ]]; then
  probe req P13 argon_auth '42501.*permission denied for table credential_acts' "the auth role uses a proof (writes an attributable act)" \
    "INSERT INTO credential_acts (kind, actor_user_id) VALUES ('ledger_batch', $U1);"
  probe req P14 argon_auth '42501.*permission denied for table ledger_batches' "the auth role posts a ledger batch" \
    "INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo) VALUES ($S, 'P14', $U1, 1, 1);"
  # Scoped once 0005 has added act_kind (which it then requires: an unscoped
  # PIN proof is refused); plain before it.
  probe ctl K11 argon_auth '' "control: the auth role mints a PIN proof (rolled back)" \
    "BEGIN;
     DO \$k\$ BEGIN
       IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'auth_proofs' AND column_name = 'act_kind') THEN
         EXECUTE format('INSERT INTO auth_proofs (user_id, method, act_kind) VALUES (%L, ''pin'', ''ledger_batch'')', $U1);
       ELSE
         EXECUTE format('INSERT INTO auth_proofs (user_id, method) VALUES (%L, ''pin'')', $U1);
       END IF;
     END \$k\$;
     ROLLBACK;"
  probe ctl K12 argon_auth '' "control: the auth role records a sign-in attempt (rolled back)" \
    "BEGIN; INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($U1, repeat('a', 64), false); ROLLBACK;"
else
  # Not provisioned: the separation does not exist, which is a failure, not a skip.
  for c in "P13|the auth role uses a proof" "P14|the auth role posts a ledger batch" "K11|control: the auth role mints a proof" "K12|control: the auth role records an attempt"; do
    class=req; [[ ${c:0:1} == K ]] && class=ctl
    ROWS+=("$(printf '%-4s %-3s %-14s %-23s %s\n                                              → %s' "${c%%|*}" "$class" argon_auth "NOT PROVISIONED ✗" "${c#*|}" "ARGON_AUTH_PASSWORD is unset: there is no argon_auth role, so nothing separates minting from use")")
    printf '%-4s %-24s %s\n' "${c%%|*}" "NOT PROVISIONED ✗" "${c#*|}" >&2
  done
  req_fail=1
fi

# == Closed by 0002 — were gaps under 0001 =====================================
probe req G1 argon_app '23514.*CONSTRAINT NAME: +ledger_lines_entry_open' "add a balanced pair of new lines to an already-POSTED entry" \
  "BEGIN;
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($E1, $A1,  500, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($E1, $A2, -500, 'principal', $M1);
   COMMIT;"
probe req G3 argon_app '23514.*CONSTRAINT NAME: +signatory_not_auditor' "the current internal auditor signs" \
  "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M2, 'internal_auditor');"
probe req G4 argon_app '23514.*CONSTRAINT NAME: +auditor_holds_no_signing_post' "the current chairman (a signatory) is also appointed internal auditor" \
  "BEGIN;
   INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until)
     VALUES ($S, $M3, 'internal_auditor', false, '2025-01-01', '2026-12-31');
   INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M3, 'treasurer', true, '2026-01-01');
   COMMIT;"
# == Gaps: edges 0002 does not cover (accepted — see deploy/README.md) =========
probe gap G2 argon_app 'ERROR' "commit a ledger entry with ZERO lines" \
  "INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post_label)
     VALUES ($EZERO, $S, 'loan_disbursement', '2026-02-01', $ACTOR, 'Treasurer');"
probe gap G7 $OWNER 'ERROR' "owner edits a signature — signatory_approvals has no immutability trigger (rolled back)" \
  "BEGIN; UPDATE signatory_approvals SET post_at_signing = 'chairman', signed_at = now() - interval '1 year'
     WHERE request_id = $R1 AND member_id = $M1; ROLLBACK;"
probe gap G8 argon_app 'ERROR' "two treasurers over overlapping historical ranges (point-in-time attribution ambiguous)" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until) VALUES
     ($S, $M1, 'treasurer', true, '2025-01-01', '2025-12-31'),
     ($S, $M2, 'treasurer', true, '2025-06-01', '2025-09-30');"
probe gap G9 argon_app 'ERROR' "reverse the same posted entry twice" \
  "BEGIN;
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, reverses_id, reason) VALUES
     ('a0000000-0000-4000-8000-000000000045', $S, 'contribution_in', '2026-02-02', $ACTOR, 'treasurer', 'Treasurer', $E1, 'probe: posted twice in error'),
     ('a0000000-0000-4000-8000-000000000046', $S, 'contribution_in', '2026-02-02', $ACTOR, 'treasurer', 'Treasurer', $E1, 'probe: posted twice in error');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ('a0000000-0000-4000-8000-000000000045', $A2,  10000, 'principal', $M1), ('a0000000-0000-4000-8000-000000000045', $A1, -10000, 'principal', NULL),
     ('a0000000-0000-4000-8000-000000000046', $A2,  10000, 'principal', $M1), ('a0000000-0000-4000-8000-000000000046', $A1, -10000, 'principal', NULL);
   COMMIT;"
probe gap G5 $OWNER 'ERROR' "owner TRUNCATEs ledger_lines (rolled back)" \
  "BEGIN; TRUNCATE ledger_lines; ROLLBACK;"
probe gap G6 $OWNER 'ERROR' "owner disables the immutability trigger and rewrites an entry (rolled back)" \
  "BEGIN;
   ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable;
   UPDATE ledger_entries SET reason = 'probe: history rewritten by owner' WHERE id = $E1;
   ROLLBACK;"

# ---- report ------------------------------------------------------------------
echo
echo "TIER 1 PROBE — database $db — $(date -u +%FT%TZ)"
echo "req = must be refused · ctl = control, must succeed · gap = edge case, reported"
echo "-------------------------------------------------------------------------------"
for r in "${ROWS[@]}"; do echo "$r"; done
echo "-------------------------------------------------------------------------------"
if ((req_fail)); then
  echo "RESULT: FAIL — at least one required constraint did not hold as specified."
  exit 1
fi
echo "RESULT: PASS — every required case refused with its specific error; controls succeeded."
((gaps)) && echo "        $gaps gap case(s) ACCEPTED — see G* rows."
exit 0
