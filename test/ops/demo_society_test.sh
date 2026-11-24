#!/usr/bin/env bash
# The operator's demonstration society (49-demo-society.sh, 0035), end to end:
# a scratch database with every migration, the REAL API built and started on
# it as argon_app and argon_auth, and the script run against both. Every
# check below is one the reviewer asked for: one run makes it all; the five
# sign in on the PINs in the table; only a new society; marked TEST DATA; the
# PINs only as hashes; nothing made when it cannot finish the first step.
#
#   bash test/ops/demo_society_test.sh [--keep]
# DEMO_SCRIPT (a sabotaged copy) and DEMO_PINGEN (a built argon-api) override.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e

SCRIPT="${DEMO_SCRIPT:-$DEPLOY_DIR/49-demo-society.sh}"
keep=0; [[ ${1:-} == --keep ]] && keep=1
W=$(mktemp -d)
EXE=""; [[ $OSTYPE == msys* || $OSTYPE == cygwin* ]] && EXE=.exe
db="argon_verify_demo_$(date -u +%Y%m%dt%H%M%S)_$$"
create_scratch_db "$db" >/dev/null || die "cannot create $db"
api_pid=
cleanup() { [[ -n $api_pid ]] && kill "$api_pid" 2>/dev/null; rm -rf "$W"; ((keep)) || drop_scratch_db "$db" >/dev/null 2>&1 || true; }
trap cleanup EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"
sql() { pg_as argon_migrator "$db" -At -q -c "$1" </dev/null; }
[[ $(sql "SELECT (to_regproc('operator_create_demo_society') IS NOT NULL)::int") == 1 ]] || die "schema before 0035: nothing for the script to call"

BIN="${DEMO_PINGEN:-$W/argon-api$EXE}"
if [[ -z ${DEMO_PINGEN:-} ]]; then
  (cd "$REPO_DIR" && go build -o "$BIN" ./cmd/api) || die "cannot build argon-api"
fi
port=$((20000 + RANDOM % 20000))
url() { printf 'postgres://%s:%s@%s:%s/%s?sslmode=disable' "$1" "$(urlenc "$2")" "${PGHOST:-127.0.0.1}" "${PGPORT:-5432}" "$db"; }
DATABASE_URL=$(url argon_app "$ARGON_APP_PASSWORD") AUTH_DATABASE_URL=$(url argon_auth "$ARGON_AUTH_PASSWORD") \
  AUTH_CODE_KEY=$(openssl rand -hex 32) ARGON_CSRF_KEY=$(openssl rand -hex 32) PORT=$port ENV=test \
  ARGON_SMS=refusing ARGON_VFD_WEBHOOK=off ARGON_EMAIL=off "$BIN" >"$W/api.log" 2>&1 &
api_pid=$!
API="http://127.0.0.1:$port"
for _ in $(seq 1 50); do curl -fsS "$API/healthz" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$API/healthz" >/dev/null 2>&1 || { tail -5 "$W/api.log"; die "the API did not start"; }

T=""; RED=(); P=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; P=$((P + 1)); }
OP=(--operator "Test Operator" --reason "an ops test" --db "$db")
run() { DEMO_API="$API" DEMO_PINGEN="$BIN" bash "$SCRIPT" "$@" >"$W/out" 2>"$W/err"; }
nsoc() { sql "SELECT count(*) FROM societies WHERE code LIKE 'DT%'"; }
signin() { curl -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d "{\"credential\":\"$1\",\"pin\":\"$2\"}" "$API/auth/sign-in"; }

T="D01 without --confirm it makes nothing (exit 3)"
before=$(nsoc); run --code "DT1$$" --name "Shown only" "${OP[@]}"; rc=$?
[[ $rc == 3 && $(nsoc) == "$before" ]] && pass || red "exit $rc, societies $before -> $(nsoc)"

T="D02 one run makes the society, its rules, three officers and two members with balances; the table's PINs sign each in"
C="DT2$$"; run --code "$C" --name "Ikeja demonstration" "${OP[@]}" --confirm; rc=$?
row=$(sql "SELECT s.demo, s.registered_name, s.account_floor_kobo, s.lifecycle,
  (SELECT count(*) FROM charge_schedules WHERE society_id = s.id AND kind = 'withdrawal_out'),
  (SELECT count(*) FROM appointments a WHERE a.society_id = s.id AND a.is_signatory AND a.post IN ('chairman','treasurer','secretary')),
  (SELECT count(*) FROM members WHERE society_id = s.id),
  (SELECT count(DISTINCT l.member_id) FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id WHERE e.society_id = s.id AND e.kind = 'opening_balance' AND l.member_id IS NOT NULL)
  FROM societies s WHERE s.code = '$C'")
mapfile -t table < <(grep -E "^  Demo " "$W/out")
ins=0; for l in "${table[@]}"; do num=$(awk '{print $(NF-1)}' <<<"$l"); pin=$(awk '{print $NF}' <<<"$l"); [[ $(signin "$num" "$pin") == 200 ]] && ins=$((ins + 1)); done
if [[ $rc != 0 ]]; then red "exit $rc: $(grep -E 'ERROR|DETAIL|FATAL|:' "$W/err" | grep -v '^ ' | head -4 | tr '
' ' ')"
elif [[ $row != "t|TEST DATA: Ikeja demonstration|100000|active|4|3|5|2" ]]; then red "made: $row"
elif ((${#table[@]} != 5 || ins != 5)); then red "${#table[@]} rows in the table, $ins signed in on the PIN shown"
else pass; fi

T="D03 the PINs reach the database only as hashes"
leak=0; for l in "${table[@]}"; do pin=$(awk '{print $NF}' <<<"$l"); [[ -n $pin ]] && [[ $(sql "SELECT count(*) FROM auth_credentials WHERE pin_hash LIKE '%$pin%'") != 0 ]] && leak=1; done
hashes=$(sql "SELECT count(*) FROM auth_credentials c JOIN members m ON m.user_id = c.user_id JOIN societies s ON s.id = m.society_id WHERE s.code = '$C' AND c.pin_hash LIKE '\$argon2id\$%'")
((leak == 0)) && [[ $hashes == 5 ]] && pass || red "leak $leak, $hashes argon2id hashes for five people"

T="D04 a code in use is refused, and the society is not touched"
before=$(sql "SELECT count(*) FROM members m JOIN societies s ON s.id = m.society_id WHERE s.code = '$C'")
run --code "$C" --name "Again" "${OP[@]}" --confirm; rc=$?
after=$(sql "SELECT count(*) FROM members m JOIN societies s ON s.id = m.society_id WHERE s.code = '$C'")
[[ $rc != 0 && $before == "$after" ]] && grep -q "never touches an existing one" "$W/err" && pass || red "exit $rc, members $before -> $after"

T="D05 PINs given are the PINs in the table"
C5="DT5$$"; run --code "$C5" --name "Given PINs" "${OP[@]}" --pins 380915,472816,591037,603418,714295 --confirm; rc=$?
got=$(grep -E "^  Demo " "$W/out" | awk '{print $NF}' | tr '\n' ',')
[[ $rc == 0 && $got == "380915,472816,591037,603418,714295," ]] && pass || red "exit $rc, table PINs $got"

T="D06 an easy PIN is refused before anything is made"
before=$(nsoc); run --code "DT6$$" --name "Easy" "${OP[@]}" --pins 123456,472816,591037,603418,714295 --confirm; rc=$?
[[ $rc != 0 && $(nsoc) == "$before" ]] && pass || red "exit $rc, societies $before -> $(nsoc)"

T="D07 an API that does not answer: nothing is made"
before=$(nsoc); DEMO_API="http://127.0.0.1:9" DEMO_PINGEN="$BIN" bash "$SCRIPT" --code "DT7$$" --name "No API" "${OP[@]}" --confirm >"$W/out" 2>"$W/err"; rc=$?
[[ $rc != 0 && $(nsoc) == "$before" ]] && pass || red "exit $rc, societies $before -> $(nsoc)"

T="D08 --db is required, never defaulted"
DEMO_API="$API" DEMO_PINGEN="$BIN" bash "$SCRIPT" --code "DT8$$" --name "No db" --operator "Test Operator" --reason "t" --confirm >"$W/out" 2>"$W/err"; rc=$?
[[ $rc != 0 ]] && grep -q -- "--db is required" "$W/err" && pass || red "exit $rc"

T="D10 a name already saying TEST DATA is refused, not doubled, and nothing is made"
before=$(nsoc); run --code "DT10$$" --name "TEST DATA: Lagos" "${OP[@]}" --confirm; rc=$?
[[ $rc != 0 && $(nsoc) == "$before" ]] && grep -q -- '--name "Lagos"' "$W/err" && pass || red "exit $rc, societies $before -> $(nsoc): $(head -c 200 "$W/err")"

T="D09 a real society's member gets no PIN from the operator, and no device on the PIN alone"
R="DR$$"
sql "SELECT operator_create_society('$R', 'Ikeja Teachers Cooperative', 'LSCS/2019/0412', 'Lagos', 2, 2, 2, 7, 10000000, 'Test Operator', 'test@host', 'Founding resolution', current_date, 'test')" >/dev/null
rm=$(sql "SELECT operator_appoint_founding_officer((SELECT id FROM societies WHERE code = '$R'), 'Real Chair', '+2347099999901', '$R-1', '2024-01-01', 'chairman', true, 'Test Operator', 'test@host', 'Founding resolution', current_date, 'test')")
h=$(printf '380915\n' | "$BIN" demo-pin | awk '{print $2}')
pg_as argon_migrator "$db" -At -q -v u="$rm" -v h="$h" -f - >"$W/out" 2>"$W/err" <<'SQL'
SELECT operator_set_demo_pin((SELECT user_id FROM members WHERE id = :'u'), :'h', 'Test Operator', 'test@host', 'test');
SQL
rc=$?
sql "INSERT INTO auth_credentials (user_id, pin_hash) SELECT user_id, '$h' FROM members WHERE id = '$rm'" >/dev/null
answer=$(curl -sS -H 'Content-Type: application/json' -d "{\"credential\":\"$R-1\",\"pin\":\"380915\"}" "$API/auth/sign-in")
[[ $rc != 0 ]] && grep -q demo_pin_only_for_demo "$W/err" && [[ $answer != *sessionToken* && -n $answer ]] && pass ||
  red "the operator's PIN: exit $rc; a real officer on a new device answered: ${answer:0:120}"

echo
declared=$(grep -cE '^T="D[0-9]+ ' "$0")
if ((${#RED[@]})); then echo "demo society: ${#RED[@]} RED of $declared: ${RED[*]}"; exit 1; fi
((P == declared)) || { echo "demo society: $P passed of $declared declared: a test did not run"; exit 1; }
echo "demo society: all $declared green"
