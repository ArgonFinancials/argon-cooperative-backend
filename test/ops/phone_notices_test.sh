#!/usr/bin/env bash
# 68-check-phone-notices.sh: a changed number's old phone, not told, is told
# to a person. A scratch database with every migration; the notices are
# written as the superuser with triggers off (session_replication_role =
# replica): test scaffolding for the state, not a path anything real takes.
#
#   bash test/ops/phone_notices_test.sh
# PHONE_NOTICES_SCRIPT (a sabotaged copy) overrides.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e
SCRIPT="${PHONE_NOTICES_SCRIPT:-$DEPLOY_DIR/68-check-phone-notices.sh}"
W=$(mktemp -d)
db="argon_verify_notices_$(date -u +%Y%m%dt%H%M%S)_$$"
create_scratch_db "$db" >/dev/null || die "cannot create $db"
trap 'rm -rf "$W"; drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"

cat >"$W/alert" <<EOF
#!/usr/bin/env bash
{ echo "TITLE: \$1"; cat; } >>"$W/alerts"
EOF
chmod +x "$W/alert"

# A society and a member to hang the notices on.
pg_super -d "$db" -q <<'SQL' >/dev/null
SET session_replication_role = replica;
INSERT INTO societies (id, code, registered_name, registration_number, state_directorate)
  VALUES ('00000000-0000-0000-0000-0000000000a1', 'NOTICE', 'Notice Test Cooperative', 'N/1', 'Lagos');
INSERT INTO branches (id, society_id, code, name) VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'HQ', 'HQ');
INSERT INTO members (id, society_id, branch_id, member_number, full_name, phone, status, collection_channel, joined_on)
  VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000b1',
          'N-1', 'Member', '+2348099990000', 'active', 'cash', '2020-01-01');
SQL

# notice OLD_LAST4 AGO [OUTCOME SENT_AGO]: a change AGO, its old number ending
# OLD_LAST4, and what came of telling it, if anything.
notice() {
  local send=""
  [[ -n ${3:-} ]] && send="INSERT INTO phone_change_notice_sends (notice_id, outcome, provider_message_id, at)
    SELECT id, '$3', CASE WHEN '$3' = 'accepted' THEN 'm-$1' END, now() - interval '$4' FROM phone_change_notices WHERE old_phone = '+234809000$1';"
  pg_super -d "$db" -q <<SQL >/dev/null
SET session_replication_role = replica;
INSERT INTO phone_change_notices (member_id, old_phone, new_phone, act_id, at)
  VALUES ('00000000-0000-0000-0000-0000000000c1', '+234809000$1', '+2348077770000', gen_random_uuid(), now() - interval '$2');
$send
SQL
}

T=""; RED=(); P=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; P=$((P + 1)); }
run() { rm -f "$W/alerts"; PHONE_NOTICES_ALERT="$W/alert" bash "$SCRIPT" "$@" >"$W/out" 2>&1; }

T="N01 every old phone told: exit 0, no alert"
notice 1111 "2 hours" accepted "2 hours"
run --db "$db"; rc=$?
[[ $rc == 0 && ! -f $W/alerts ]] && pass || red "exit $rc, alerts: $(cat "$W/alerts" 2>/dev/null)"

T="N02 a notice still waiting after 15 minutes: alerted, by its last four digits only"
notice 2222 "20 minutes"
run --db "$db"; rc=$?
[[ $rc == 3 ]] && grep -q "[.][.][.]2222 | waiting" "$W/alerts" && ! grep -q "+2348090002222" "$W/alerts" && pass ||
  red "exit $rc: $(tr '\n' ' ' <"$W/alerts" 2>/dev/null | cut -c1-240)"

T="N03 an old phone refused today: alerted (someone should look twice)"
notice 3333 "1 hour" refused "1 hour"
run --db "$db"; rc=$?
[[ $rc == 3 ]] && grep -q "[.][.][.]3333 | refused" "$W/alerts" && grep -q "in person" "$W/alerts" && pass ||
  red "exit $rc: $(tr '\n' ' ' <"$W/alerts" 2>/dev/null | cut -c1-240)"

T="N04 a notice sent a minute ago and still being sent: not yet alerted"
notice 4444 "1 minute"
run --db "$db"; rc=$?
! grep -q "cannot read" "$W/alerts" 2>/dev/null && ! grep -q "[.][.][.]4444" "$W/alerts" 2>/dev/null && pass || red "a notice a minute old was alerted as waiting (or nothing was read)"

T="N05 an old refusal, days ago: alerted once, on its day, not every day"
notice 5555 "3 days" not_reached "3 days"
run --db "$db"; rc=$?
! grep -q "cannot read" "$W/alerts" 2>/dev/null && ! grep -q "[.][.][.]5555" "$W/alerts" 2>/dev/null && pass || red "a refusal three days old is still alerted (or nothing was read)"

T="N06 --db is required, never defaulted"
run; rc=$?
[[ $rc != 0 ]] && grep -q -- "--db is required" "$W/out" && pass || red "exit $rc"

T="N07 the notices cannot be read: an alert saying so, never 'all told'"
run --db argon_verify_no_such_db_$$; rc=$?
[[ $rc == 3 ]] && grep -q "cannot read" "$W/alerts" && pass || red "exit $rc: $(tail -2 "$W/out")"

echo
declared=$(grep -cE '^T="N[0-9]+ ' "$0")
if ((${#RED[@]})); then echo "phone notices: ${#RED[@]} RED of $declared: ${RED[*]}"; exit 1; fi
((P == declared)) || { echo "phone notices: $P passed of $declared declared"; exit 1; }
echo "phone notices: all $declared green"
