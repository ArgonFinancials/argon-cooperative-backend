#!/usr/bin/env bash
# 66-check-vfd-holds.sh: a payout held waiting for VFD is told to a person.
# A scratch database with every migration; the held transfers are written as
# the superuser with triggers off (session_replication_role = replica): test
# scaffolding for the state, not a path anything real takes.
#
#   bash test/ops/vfd_holds_test.sh
# VFD_HOLDS_SCRIPT (a sabotaged copy) overrides.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e
SCRIPT="${VFD_HOLDS_SCRIPT:-$DEPLOY_DIR/66-check-vfd-holds.sh}"
W=$(mktemp -d)
db="argon_verify_holds_$(date -u +%Y%m%dt%H%M%S)_$$"
create_scratch_db "$db" >/dev/null || die "cannot create $db"
trap 'rm -rf "$W"; drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"

cat >"$W/alert" <<EOF
#!/usr/bin/env bash
{ echo "TITLE: \$1"; cat; } >>"$W/alerts"
EOF
chmod +x "$W/alert"

# held REF SENT_AGO [REQUERY_RESULT TXN_STATUS]: a transfer sent SENT_AGO, and
# VFD's latest answer to a requery, if any.
held() {
  local extra=""
  [[ -n ${3:-} ]] && extra="INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_status) VALUES ('$4'::text::uuid, 'requeried', '$3', '00', '${5:-}');"
  pg_super -d "$db" -q <<SQL >/dev/null
SET session_replication_role = replica;
INSERT INTO withdrawal_holds (id, request_id, society_id, member_id, amount_kobo, actor_user_id)
  VALUES ('$2'::uuid, gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 5000, gen_random_uuid());
INSERT INTO vfd_transfers (id, hold_id, reference, from_account, to_account, amount_kobo)
  VALUES ('$4'::text::uuid, '$2'::uuid, '$1', '1000000001', '1000000002', 5000);
INSERT INTO vfd_transfer_events (transfer_id, kind, result, at) VALUES ('$4'::text::uuid, 'sent', 'unknown', now() - interval '$6');
$extra
SQL
}
uuid() { python3 -c 'import uuid; print(uuid.uuid4())' 2>/dev/null || python -c 'import uuid; print(uuid.uuid4())'; }

T=""; RED=(); P=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; P=$((P + 1)); }
run() { rm -f "$W/alerts"; VFD_HOLDS_ALERT="$W/alert" bash "$SCRIPT" "$@" >"$W/out" 2>&1; }

T="H01 nothing held: exit 0, no alert"
run --db "$db"; rc=$?
[[ $rc == 0 && ! -f $W/alerts ]] && pass || red "exit $rc, alerts: $(cat "$W/alerts" 2>/dev/null)"

T="H02 VFD answered 91: an alert naming the transfer and VFD's answer, exit 3"
held "Argon-HELD91" "$(uuid)" pending "$(uuid)" 91 "1 hour"
run --db "$db"; rc=$?
[[ $rc == 3 ]] && grep -q "Argon-HELD91" "$W/alerts" && grep -q "transaction status 91" "$W/alerts" && grep -q "never released automatically" "$W/alerts" && pass ||
  red "exit $rc: $(tr '\n' ' ' <"$W/alerts" 2>/dev/null | cut -c1-200)"

T="H03 a day with no definitive answer: alerted"
held "Argon-SILENT" "$(uuid)" "" "$(uuid)" "" "25 hours"
run --db "$db"; rc=$?
[[ $rc == 3 ]] && grep -q "Argon-SILENT" "$W/alerts" && pass || red "exit $rc"

T="H04 two hours with no answer: not yet a person's"
held "Argon-YOUNG" "$(uuid)" "" "$(uuid)" "" "2 hours"
run --db "$db"; rc=$?
! grep -q "Argon-YOUNG" "$W/alerts" 2>/dev/null && pass || red "a transfer two hours old was alerted"

T="H05 --db is required, never defaulted"
run; rc=$?
[[ $rc != 0 ]] && grep -q -- "--db is required" "$W/out" && pass || red "exit $rc"

T="H06 the held payouts cannot be read: an alert saying so, never 'nothing held'"
run --db argon_verify_no_such_db_$$; rc=$?
[[ $rc == 3 ]] && grep -q "cannot read" "$W/alerts" && pass || red "exit $rc: $(cat "$W/out" | tail -2)"

echo
declared=$(grep -cE '^T="H[0-9]+ ' "$0")
if ((${#RED[@]})); then echo "vfd holds: ${#RED[@]} RED of $declared: ${RED[*]}"; exit 1; fi
((P == declared)) || { echo "vfd holds: $P passed of $declared declared"; exit 1; }
echo "vfd holds: all $declared green"
