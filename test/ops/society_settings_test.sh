#!/usr/bin/env bash
# Tests for deploy/45-society-settings.sh, the operator's way of changing a
# society's rules (0014's functions, LEDGER_CONTRACT.md §10). Written BEFORE the
# script, 2026-10-14.
#
# The database enforces the rules (suite 0014). These tests are about what the
# SCRIPT adds or could lose on the way:
#   - it asks for the operator, the reason and, for a society's own settings,
#     the society's resolution, and refuses a call without them before the
#     database is touched;
#   - it records the server login itself: the caller cannot supply one;
#   - it shows the change and applies nothing without --confirm;
#   - it refuses a resolution where none belongs (lifecycle, charge series)
#     rather than dropping it silently;
#   - it prints the database's refusal as written (the "why" is the point);
#   - it passes the operator's words through verbatim, quotes and all;
#   - it never prints a credential, on any path.
# Every refusal is also checked to have changed nothing: the society, its log,
# its charge series.
#
#   bash test/ops/society_settings_test.sh [--keep]
#   SETTINGS_SCRIPT=/path/to/copy  runs against another copy (sabotage runs)
#
# Runs against a scratch argon_verify_ops_<ts> built from the migrations, never
# a real database. Needs the schema at 14 or later.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e   # each test reads its own statuses; -u and pipefail stay

SCRIPT="${SETTINGS_SCRIPT:-$DEPLOY_DIR/45-society-settings.sh}"
keep=0; [[ ${1:-} == --keep ]] && keep=1

db="argon_verify_ops_$(date -u +%Y%m%dt%H%M%S)_$$"   # and the process (finding 38)
create_scratch_db "$db" >/dev/null || die "cannot create $db"
((keep)) || trap 'drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"

sql() { pg_as argon_migrator "$db" -At -q -c "$1" </dev/null; }
[[ $(sql "SELECT (to_regclass('public.society_settings_log') IS NOT NULL)::int") == 1 ]] ||
  die "schema before 0014: nothing for the script to call"

T=""; RED=(); N=0
OUT=$(mktemp); ALL=$(mktemp)
((keep)) || trap 'rm -f "$OUT" "$ALL"; drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT

red()  { echo "  RED  $T — $*"; RED+=("$T"); }
pass() { echo "  pass $T"; }

# A fresh society per test, so no test reads another's log. Sets $c. Called
# in THIS shell, never as $(fresh): in a subshell the counter does not advance
# and die stops only the subshell, so every test after the first ran against
# no society at all (the first red run, 2026-10-14).
fresh() {
  c="OPS$((++N))"
  sql "INSERT INTO societies (code, registered_name, registration_number, state_directorate)
       VALUES ('$c', 'Ops Test Cooperative $N', 'T/OPS/$N', 'Lagos')" >/dev/null || die "cannot seed $c"
}
# Everything a refusal must leave as it was.
snap() {
  sql "SELECT (SELECT to_jsonb(s) - 'created_at' FROM societies s WHERE code = '$1')::text
           || ' log=' || (SELECT count(*) FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = '$1')
           || ' series=' || (SELECT count(*) FROM charge_schedules c JOIN societies s ON s.id = c.society_id WHERE s.code = '$1')"
}
logn()  { sql "SELECT count(*) FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = '$1'"; }
# The society's latest log row, one field: setting|old|new|operator|login|ref|date|reason
lastlog() {
  sql "SELECT concat_ws('|', setting, coalesce(old_value,''), new_value, operator_name, server_login,
                        coalesce(resolution_ref,''), coalesce(resolution_date::text,''), reason)
         FROM society_settings_log l JOIN societies s ON s.id = l.society_id
        WHERE s.code = '$1' ORDER BY at DESC, l.id DESC LIMIT 1"
}
col() { sql "SELECT $2 FROM societies WHERE code = '$1'"; }

# run ARGS...: the script against the scratch database. Output (both streams)
# in $OUT and appended to $ALL; status in $RC. Not under sudo unless a test says.
RC=0
run() {
  RC=0
  env -u SUDO_USER bash "$SCRIPT" "$@" --db "$db" >"$OUT" 2>&1 </dev/null || RC=$?
  cat "$OUT" >>"$ALL"
}
# Case-folded, in bash: Git Bash's grep -qiF aborts on this output, which
# made every check fail whatever the script printed (the first red run).
said() { local o; o=$(<"$OUT"); [[ ${o,,} == *"${1,,}"* ]]; }
shown() { sed 's/^/        | /' "$OUT" | head -12; }

OP="Adaeze Okafor (Argon operations)"
MINUTES="Minutes of the AGM, item 7 (resolution 2026/04)"
RESOLVED="2026-09-30"
WHY="Carrying out the AGM's resolution"
# The log is the attribution record: a reader must not have to know which part
# was verified and which only claimed (2026-10-14).
LOGIN="$(id -un)@$(hostname) [login, verified by id -un]"
SUDO_CLAIM="; sudo by aokafor [claimed: SUDO_USER from the environment, not verified]"
TODAY=$(sql "SELECT current_date")
YESTERDAY=$(sql "SELECT current_date - 1")
society_args=(--operator "$OP" --resolution "$MINUTES" --resolution-date "$RESOLVED" --reason "$WHY")

series_file() {  # series_file FROM -> path of a series file effective FROM
  local f; f=$(mktemp --suffix=.json)
  printf '{"kind":"withdrawal_out","component":"argon_margin","from":"%s","source":"Argon fee schedule v1 (TEST DATA)","bands":[{"from":0,"to":null,"flat":1000,"bp":0,"cap":null}]}\n' "$1" >"$f"
  echo "$f"
}

echo "settings script: $SCRIPT"
echo "scratch database: $db"

# ---------------------------------------------------------------------------
T="S01 a quorum set with --confirm is applied and logged: who, from where, on whose decision, why"
fresh
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}" --confirm
r=$(lastlog "$c")
if ((RC != 0)); then red "exit $RC"; shown
elif [[ $(col "$c" withdrawal_signatures_required) != 3 ]]; then red "the quorum is $(col "$c" withdrawal_signatures_required), not 3"
elif [[ $r != "withdrawal_signatures_required||3|$OP|$LOGIN|$MINUTES|$RESOLVED|$WHY" ]]; then red "log row: $r"
else pass; fi

T="S02 without --confirm nothing is applied, the change is shown, and the exit says NOT APPLIED"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}"
if [[ $(snap "$c") != "$before" ]]; then red "the society or its log changed without --confirm"
elif ((RC != 3)); then red "exit $RC; a preview exits 3, so no caller mistakes it for a change made"
elif ! said "not applied"; then red "the preview does not say NOT APPLIED"; shown
elif ! said "→ 3" && ! said "-> 3"; then red "the preview does not show the proposed value"; shown
else pass; fi

T="S03 no --operator: refused before the database, and the refusal names --operator"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value 3 --resolution "$MINUTES" --resolution-date "$RESOLVED" --reason "$WHY" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "--operator"; then red "the refusal does not name --operator"; shown
else pass; fi

T="S04 no --reason: refused before the database, and the refusal names --reason"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value 3 --operator "$OP" --resolution "$MINUTES" --resolution-date "$RESOLVED" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "--reason"; then red "the refusal does not name --reason"; shown
else pass; fi

T="S05 every society setting needs the society's resolution, and says why"
bad=""
for s in withdrawal-quorum write-off-quorum signature-count account-floor guard-threshold; do
  for missing in --resolution --resolution-date; do
    fresh; before=$(snap "$c")
    a=(--operator "$OP" --reason "$WHY" --resolution "$MINUTES" --resolution-date "$RESOLVED")
    [[ $missing == --resolution ]] && a=(--operator "$OP" --reason "$WHY" --resolution-date "$RESOLVED")
    [[ $missing == --resolution-date ]] && a=(--operator "$OP" --reason "$WHY" --resolution "$MINUTES")
    run "$s" --society "$c" --value 4 "${a[@]}" --confirm
    if ((RC == 0)) || [[ $(snap "$c") != "$before" ]]; then bad+=" $s(no ${missing#--}: applied)"
    elif ! said "$missing" || ! said "society's decision"; then bad+=" $s(no ${missing#--}: refusal does not name $missing and why)"; fi
  done
done
[[ -z $bad ]] && pass || red "$bad"

T="S06 lifecycle with a resolution: refused, saying it is Argon's state, not the society's decision"
fresh; before=$(snap "$c")
run lifecycle --society "$c" --value verifying --operator "$OP" --reason "Documents received" --resolution "$MINUTES" --resolution-date "$RESOLVED" --confirm
if ((RC == 0)); then red "accepted (the resolution would be dropped silently)"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "argon's state"; then red "the refusal does not say why a resolution does not belong"; shown
else pass; fi

T="S07 lifecycle with the operator and a reason, no resolution: applied and logged"
fresh
run lifecycle --society "$c" --value verifying --operator "$OP" --reason "Documents received" --confirm
r=$(lastlog "$c")
if ((RC != 0)); then red "exit $RC"; shown
elif [[ $(col "$c" lifecycle) != verifying ]]; then red "lifecycle is $(col "$c" lifecycle)"
elif [[ $r != "lifecycle|prospect|verifying|$OP|$LOGIN|||Documents received" ]]; then red "log row: $r"
else pass; fi

T="S08 the server login is the script's to record: --login is refused; the login is labelled verified, the sudo name claimed"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}" --login "someone@elsewhere" --confirm
if ((RC == 0)) || [[ $(snap "$c") != "$before" ]]; then red "a caller-supplied login was accepted"
else
  fresh; RC=0
  SUDO_USER=aokafor bash "$SCRIPT" withdrawal-quorum --society "$c" --value 3 "${society_args[@]}" --confirm --db "$db" >"$OUT" 2>&1 </dev/null || RC=$?
  cat "$OUT" >>"$ALL"
  got=$(lastlog "$c" | cut -d'|' -f5)
  if ((RC != 0)); then red "under sudo: exit $RC"; shown
  elif [[ $got != "$LOGIN$SUDO_CLAIM" ]]; then red "under sudo the login is recorded as '$got'"
  else pass; fi
fi

T="S09 the database's refusal is printed as written: a withdrawal quorum of one, and why"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value 1 "${society_args[@]}" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "REFUSED by the database (withdrawal_quorum_at_least_two)" || ! said "the single risk PRD 8.4 exists to prevent"; then red "the refusal lost its reason or its rule"; shown
else pass; fi

T="S10 a write-off quorum of one is refused with the database's reason"
fresh; before=$(snap "$c")
run write-off-quorum --society "$c" --value 1 "${society_args[@]}" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "REFUSED by the database (write_off_quorum_at_least_two)" || ! said "legitimate-looking record"; then red "the refusal lost its reason or its rule"; shown
else pass; fi

T="S11 the guard threshold has no minimum, and the script says so where it is set"
fresh
run guard-threshold --society "$c" --value 1 "${society_args[@]}"
if ! said "interface friction" || ! said "not authorisation"; then red "the preview does not say the threshold is interface friction, not authorisation"; shown
else
  run guard-threshold --society "$c" --value 1 "${society_args[@]}" --confirm
  if ((RC != 0)); then red "a threshold of 1 kobo refused: exit $RC"; shown
  elif [[ $(col "$c" guard_threshold_kobo) != 1 ]]; then red "the threshold is $(col "$c" guard_threshold_kobo)"
  else pass; fi
fi

T="S12 money is shown in naira beside the kobo it is set in"
fresh
run account-floor --society "$c" --value 100000 "${society_args[@]}"
o1=$(cat "$OUT")
run account-floor --society "$c" --value 123456789 "${society_args[@]}"
if [[ $o1 != *"₦1,000.00"* ]]; then red "the preview of a 100000-kobo floor does not show ₦1,000.00"; echo "$o1" | head -8
elif ! said "₦1,234,567.89"; then red "the preview of 123456789 kobo does not show ₦1,234,567.89"; shown
else pass; fi

T="S13 a charge series from a file: applied from today, logged with its source"
fresh; f=$(series_file "$TODAY")
run charge-series --society "$c" --file "$f" --operator "$OP" --reason "$WHY" --confirm
r=$(lastlog "$c")
n=$(sql "SELECT count(*) FROM charge_schedules cs JOIN charge_bands b ON b.schedule_id = cs.id JOIN societies s ON s.id = cs.society_id
          WHERE s.code = '$c' AND cs.component = 'argon_margin' AND cs.effective_from = '$TODAY' AND b.flat_kobo = 1000")
if ((RC != 0)); then red "exit $RC"; shown
elif [[ $n != 1 ]]; then red "$n matching series with its band"
elif [[ $r != charge_series* || $r != *"Argon fee schedule v1"* || $r != *"$OP|$LOGIN"* ]]; then red "log row: $r"
else pass; fi
rm -f "$f"

T="S14 a backdated series is refused with the database's reason"
fresh; before=$(snap "$c"); f=$(series_file "$YESTERDAY")
run charge-series --society "$c" --file "$f" --operator "$OP" --reason "$WHY" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "REFUSED by the database (charge_series_not_backdated)" || ! said "a figure the member was never told"; then red "the refusal lost its reason or its rule"; shown
else pass; fi
rm -f "$f"

T="S15 a series with a resolution: refused, not dropped silently"
fresh; before=$(snap "$c"); f=$(series_file "$TODAY")
run charge-series --society "$c" --file "$f" --operator "$OP" --reason "$WHY" --resolution "$MINUTES" --resolution-date "$RESOLVED" --confirm
if ((RC == 0)); then red "accepted (the resolution would be dropped silently)"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "--resolution"; then red "the refusal does not name --resolution"; shown
else pass; fi
rm -f "$f"

T="S16 a series file that is missing or not JSON: refused before the database"
fresh; before=$(snap "$c"); f=$(mktemp --suffix=.json); echo '{"kind": withdrawal_out' >"$f"
run charge-series --society "$c" --file "$f" --operator "$OP" --reason "$WHY" --confirm
rc1=$RC; o1=$(cat "$OUT")
run charge-series --society "$c" --file "$f.absent" --operator "$OP" --reason "$WHY" --confirm
if ((rc1 == 0 || RC == 0)); then red "accepted (rc $rc1 / $RC)"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif [[ $o1 != *JSON* ]] || ! said "$f.absent"; then red "the refusals do not say what is wrong with the file"; shown
else pass; fi
rm -f "$f"

T="S17 the operator's words are stored verbatim, quotes and SQL included"
fresh
odd_op="Ngozi O'Brien (Argon ops)"
odd_why="Per the AGM'; DROP TABLE societies; -- and \"quoted\" \$x :'v'"
run withdrawal-quorum --society "$c" --value 2 --operator "$odd_op" --reason "$odd_why" --resolution "$MINUTES" --resolution-date "$RESOLVED" --confirm
r=$(lastlog "$c")
if ((RC != 0)); then red "exit $RC"; shown
elif [[ $(sql "SELECT (to_regclass('public.societies') IS NOT NULL)::int") != 1 ]]; then red "THE SOCIETIES TABLE IS GONE"
elif [[ $r != *"|$odd_op|"* || $r != *"|$odd_why" ]]; then red "log row: $r"
else pass; fi

T="S18 an unknown society code is refused, and nothing is written anywhere"
before=$(sql "SELECT count(*) FROM society_settings_log")
run withdrawal-quorum --society "NO_SUCH_SOCIETY" --value 3 "${society_args[@]}" --confirm
if ((RC == 0)); then red "accepted"
elif [[ $(sql "SELECT count(*) FROM society_settings_log") != "$before" ]]; then red "the log grew"
elif ! said "NO_SUCH_SOCIETY"; then red "the refusal does not name the code"; shown
else pass; fi

T="S19 a value that is not a whole number is refused before the database"
fresh; before=$(snap "$c")
run withdrawal-quorum --society "$c" --value two "${society_args[@]}" --confirm
rc1=$RC
run account-floor --society "$c" --value 1000.50 "${society_args[@]}" --confirm
if ((rc1 == 0 || RC == 0)); then red "accepted (rc $rc1 / $RC)"
elif [[ $(snap "$c") != "$before" ]]; then red "refused, but something changed"
elif ! said "whole number"; then red "the refusal does not say what a value must be"; shown
else pass; fi

T="S20 an unknown setting is refused and the settings are listed"
run withdrawal_quorum_typo --society OPS1 --value 3 "${society_args[@]}" --confirm
if ((RC == 0)); then red "accepted"
elif ! said "withdrawal-quorum" || ! said "lifecycle" || ! said "charge-series"; then red "the refusal does not list the settings"; shown
else pass; fi

T="S21 show prints the society's settings and its log"
fresh
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}" --confirm
run show --society "$c"
if ((RC != 0)); then red "exit $RC"; shown
elif ! said "withdrawal_signatures_required" || ! said "$OP" || ! said "$MINUTES"; then red "show does not print the setting and who changed it on whose decision"; shown
else pass; fi

# signatories CODE N: N current signatories in the society, each a person.
signatories() {
  local i
  for ((i = 1; i <= $2; i++)); do
    sql "WITH u AS (INSERT INTO users (full_name) VALUES ('Signatory $i of $1') RETURNING id),
              m AS (INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
                    SELECT s.id, (SELECT id FROM branches WHERE society_id = s.id LIMIT 1), '$1-S$i', 'Signatory $i', '+2349$(printf '%09d' $((N * 100 + i)))', 'cash', '2020-01-01', u.id
                      FROM societies s, u WHERE s.code = '$1' RETURNING id, society_id)
         INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until)
         SELECT society_id, id, 'treasurer', true, '2020-01-01', '2099-12-31' FROM m" >/dev/null || die "cannot appoint signatory $i in $1"
  done
}

# Decided 2026-10-19: a quorum the society's signatories cannot meet is a
# WARNING, not a refusal (the society may be appointing another signatory next
# week; refusing would make us the arbiter of a bye-law). Loud, in the preview,
# and it says which requests become impossible.
T="S23 a withdrawal quorum the signatories cannot meet: applied, after a loud warning naming the impossible requests"
fresh; sql "INSERT INTO branches (society_id, code, name) SELECT id, 'HQ', 'HQ' FROM societies WHERE code = '$c'" >/dev/null
signatories "$c" 2
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}"
o1=$(cat "$OUT")
run withdrawal-quorum --society "$c" --value 3 "${society_args[@]}" --confirm
if [[ $o1 != *WARNING* || $o1 != *"EVERY withdrawal request"* ]]; then red "the preview does not warn that every request becomes impossible (2 signatories, quorum 3)"; echo "$o1" | head -14
elif ((RC != 0)) || [[ $(col "$c" withdrawal_signatures_required) != 3 ]]; then red "refused or not applied (exit $RC): a warning, not a refusal"; shown
else pass; fi

T="S24 a quorum the signatories can meet whoever raises: no warning"
fresh; sql "INSERT INTO branches (society_id, code, name) SELECT id, 'HQ', 'HQ' FROM societies WHERE code = '$c'" >/dev/null
signatories "$c" 4
run withdrawal-quorum --society "$c" --value 2 "${society_args[@]}"
if said "WARNING"; then red "warned with four signatories at a quorum of two"; shown
else pass; fi

# Last, over every line every run printed.
T="S22 no credential is printed, on any path"
leaked=""
for v in $(compgen -v); do
  [[ $v =~ (PASSWORD|SECRET|TOKEN|KEY|WEBHOOK) ]] || continue
  val=${!v:-}; ((${#val} >= 6)) || continue
  grep -qF -- "$val" "$ALL" && leaked+=" $v"
done
[[ -z $leaked ]] && pass || red "printed the value of:$leaked"

echo
total=24
if ((${#RED[@]})); then
  echo "settings script: $((total - ${#RED[@]}))/$total pass; RED: ${#RED[@]}"
  exit 1
fi
echo "settings script: $total/$total pass"
