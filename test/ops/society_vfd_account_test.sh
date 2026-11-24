#!/usr/bin/env bash
# Tests for deploy/48-society-vfd-account.sh and the API image's
# vfd-account-lookup (2026-11-20). LEDGER_CONTRACT §12: a society's VFD account
# is "recorded by the operator from VFD's answer, never typed".
#
#   V01-V10  the script, with VFD_LOOKUP a fake that prints what the real
#            lookup prints (one JSON line; exit 0 found, 3 refused, 4 not reached)
#   V11-V13  the REAL lookup (cmd/api, built here) against a stub VFD over
#            HTTPS. V11 and V12 need a test certificate the Go binary trusts:
#            SSL_CERT_FILE, honoured on Linux only, so they run in CI.
#
#   bash test/ops/society_vfd_account_test.sh [--keep]
#   VFD_SCRIPT=/path/to/copy   runs the script from another copy (sabotage runs)
#   VFD_LOOKUP_BIN=/path       the lookup binary to test (sabotage runs; default: built here)
# Runs against a scratch argon_verify_vfdacct_<ts>_<pid>, never a real database.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e

SCRIPT="${VFD_SCRIPT:-$DEPLOY_DIR/48-society-vfd-account.sh}"
keep=0; [[ ${1:-} == --keep ]] && keep=1
W=$(mktemp -d)
db="argon_verify_vfdacct_$(date -u +%Y%m%dt%H%M%S)_$$"
STUB_PID=""
create_scratch_db "$db" >/dev/null || die "cannot create $db"
cleanup() {
  [[ -n $STUB_PID ]] && kill "$STUB_PID" 2>/dev/null
  rm -rf "$W"; ((keep)) || drop_scratch_db "$db" >/dev/null 2>&1 || true
}
trap cleanup EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"
sql() { pg_as argon_migrator "$db" -At -q -c "$1" </dev/null; }
PY=python3; python3 -c '' 2>/dev/null || PY=python

T=""; RED=(); PASSED=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; PASSED=$((PASSED+1)); }

# A society to record against (0023's founding function, as 46 calls it).
found() {
  sql "SELECT operator_create_society('$1', 'Test Society $1', 'REG-$1', 'Lagos', 2, 3, 2, 7, 10000000, 'Test Operator', 'test@host', 'Founding resolution', DATE '2026-01-01', 'a society for the VFD account tests')" >/dev/null ||
    die "cannot found society $1"
}
found VFDA; found VFDB; found VFDC; found VFDD

# The fake lookup: prints $W/answer.json and exits with $W/answer.rc.
cat >"$W/lookup" <<EOF
#!/usr/bin/env bash
echo "\$1" > "$W/asked"
cat "$W/answer.json"; exit \$(cat "$W/answer.rc")
EOF
chmod +x "$W/lookup"
answer() { printf '%s\n' "$2" >"$W/answer.json"; echo "$1" >"$W/answer.rc"; }
FOUND='{"outcome":"accepted","code":"00","accountNo":"1000000001","name":"VFD SAYS THIS NAME","clientId":"C-VFD-1","accountId":"A-VFD-1"}'

run() {  # run SOCIETY ACCOUNT [extra args...]
  local s=$1 a=$2; shift 2
  VFD_LOOKUP="$W/lookup" bash "$SCRIPT" --society "$s" --account "$a" --operator "Adaeze Okafor (Argon operations)" \
    --reason "VFD's onboarding letter of 2026-11-18" --db "$db" "$@" >"$W/out" 2>&1
}
recorded() { sql "SELECT account_number || '|' || coalesce(account_name,'') || '|' || vfd_client_id || '|' || coalesce(vfd_account_id,'') || '|' || how FROM vfd_accounts a JOIN societies s ON s.id = a.society_id WHERE s.code = '$1' AND a.member_id IS NULL"; }

T="V01 VFD's answer is what is recorded, with the operator, the login and why"
answer 0 "$FOUND"
run VFDA 1000000001 --confirm; rc=$?
got=$(recorded VFDA)
log=$(sql "SELECT operator_name || '|' || server_login || '|' || reason FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = 'VFDA' AND setting = 'vfd_account'")
if ((rc != 0)); then red "exit $rc: $(tail -3 "$W/out")"
elif [[ $got != "1000000001|VFD SAYS THIS NAME|C-VFD-1|A-VFD-1|recorded_by_operator" ]]; then red "recorded '$got'"
elif [[ $log != "Adaeze Okafor (Argon operations)|"*"[login, verified by id -un]"*"|VFD's onboarding letter of 2026-11-18" ]]; then red "the log reads '$log'"
elif [[ $(cat "$W/asked") != 1000000001 ]]; then red "VFD was asked about '$(cat "$W/asked")'"
else pass; fi

T="V02 without --confirm: VFD's answer shown, nothing recorded, exit 3"
answer 0 "${FOUND//1000000001/1000000002}"
run VFDB 1000000002; rc=$?
if ((rc != 3)); then red "exit $rc"
elif [[ -n $(recorded VFDB) ]]; then red "recorded without --confirm"
elif ! grep -q "VFD SAYS THIS NAME" "$W/out"; then red "VFD's answer was not shown"
else pass; fi

T="V03 VFD knows no such account: nothing recorded, and it says VFD said so"
answer 3 '{"outcome":"refused","code":"104"}'
run VFDB 1000000002 --confirm; rc=$?
if ((rc == 0)); then red "succeeded"
elif [[ -n $(recorded VFDB) ]]; then red "recorded an account VFD does not know"
elif ! grep -q "VFD knows no account 1000000002" "$W/out"; then red "it did not say VFD knows no such account: $(tail -2 "$W/out")"
else pass; fi

T="V04 VFD not reached: nothing recorded, and it says so"
answer 4 '{"outcome":"not_reached","code":""}'
run VFDB 1000000002 --confirm; rc=$?
if ((rc == 0)); then red "succeeded"
elif [[ -n $(recorded VFDB) ]]; then red "recorded with VFD unreached"
elif ! grep -q "VFD did not answer" "$W/out"; then red "it did not say VFD did not answer: $(tail -2 "$W/out")"
else pass; fi

T="V05 a typed name or id is refused, and so is a typed login"
answer 0 "${FOUND//1000000001/1000000002}"
bad=""
for extra in "--name TYPED" "--client C-TYPED" "--account-id A-TYPED" "--login someone@host"; do
  # shellcheck disable=SC2086
  if run VFDB 1000000002 --confirm $extra; then bad+=" $extra (accepted)"
  # Refused for the RIGHT reason: an unknown-argument refusal would not say why.
  elif [[ $extra == --login* ]]; then grep -q "records the server login itself" "$W/out" || bad+=" $extra (refused, but not saying the script records the login)"
  else grep -q "never typed" "$W/out" || bad+=" $extra (refused, but not saying the account is VFD's answer, never typed)"; fi
done
if [[ -n $bad ]]; then red "accepted:$bad"
elif [[ -n $(recorded VFDB) ]]; then red "something was recorded"
else pass; fi

T="V06 --db is required, and an account is ten digits"
answer 0 "$FOUND"
VFD_LOOKUP="$W/lookup" bash "$SCRIPT" --society VFDB --account 1000000002 --operator o --reason r --confirm >"$W/out_nodb" 2>&1; a=$?
run VFDB 100000000 --confirm; b=$?
if ((a == 0)); then red "ran without --db"
elif ! grep -q -- "--db is required" "$W/out_nodb"; then red "refused without --db, but not saying --db is required: $(tail -1 "$W/out_nodb")"
elif ((b == 0)); then red "accepted a nine-digit account"
elif [[ -n $(recorded VFDB) ]]; then red "something was recorded"
else pass; fi

T="V07 an unknown society is refused before VFD is asked"
rm -f "$W/asked"
run NOSUCH 1000000002 --confirm; rc=$?
if ((rc == 0)); then red "succeeded"
elif [[ -e $W/asked ]]; then red "VFD was asked about a society that does not exist"
elif ! grep -q "no society with the code 'NOSUCH'" "$W/out"; then red "refused, but not naming the unknown society: $(tail -1 "$W/out")"
else pass; fi

T="V08 a society's second account is refused; the first stays as it was"
answer 0 "${FOUND//1000000001/1000000003}"
run VFDA 1000000003 --confirm; rc=$?
if ((rc == 0)); then red "recorded a second account"
elif [[ $(recorded VFDA) != "1000000001|VFD SAYS THIS NAME|C-VFD-1|A-VFD-1|recorded_by_operator" ]]; then red "the first account changed: $(recorded VFDA)"
else pass; fi

T="V09 VFD answering for another account number is refused"
answer 0 "$FOUND"   # answers for 1000000001
run VFDC 1000000004 --confirm; rc=$?
if ((rc == 0)); then red "recorded VFD's answer for 1000000001 as 1000000004"
elif [[ -n $(recorded VFDC) ]]; then red "something was recorded"
else pass; fi

T="V10 an answer with no client id is refused"
answer 0 '{"outcome":"accepted","code":"00","accountNo":"1000000005","name":"VFD SAYS THIS NAME","accountId":"A-1"}'
run VFDD 1000000005 --confirm; rc=$?
if ((rc == 0)); then red "recorded an account with no client id"
elif [[ -n $(recorded VFDD) ]]; then red "something was recorded"
elif ! grep -q "carries no client id" "$W/out"; then red "refused, but not saying VFD's answer has no client id: $(tail -1 "$W/out")"
else pass; fi

# ---- the real lookup ------------------------------------------------------------
BIN="${VFD_LOOKUP_BIN:-}"
if [[ -z $BIN ]]; then
  BIN="$W/argon-api$EXE"
  (cd "$REPO_DIR" && go build -o "$BIN" ./cmd/api) || die "cannot build the API binary"
fi

T="V13 the real lookup refuses a VFD address that is not https"
out=$(VFD_BASE_URL=http://127.0.0.1:1 VFD_ACCESS_TOKEN=t "$BIN" vfd-account-lookup 1000000001 2>&1); rc=$?
if ((rc != 2)); then red "exit $rc: $out"; else pass; fi

if [[ $(uname -s) != Linux ]]; then
  echo "  NOT RUN V11 V12: the stub's certificate is trusted through SSL_CERT_FILE, which Go honours on Linux only (CI runs them)"
  RED+=(V11-NOT-RUN V12-NOT-RUN)
else
  MSYS2_ARG_CONV_EXCL='/CN' openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
    -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost" -keyout "$W/key.pem" -out "$W/cert.pem" >/dev/null 2>&1
  cat >"$W/vfd_stub.py" <<'EOF'
import json, ssl, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        q = parse_qs(urlparse(self.path).query)
        if self.headers.get("AccessToken") != "test-token":
            body = {"status": "401", "message": "no token"}
        elif q.get("accountNo") == ["1000000001"]:
            body = {"status": "00", "message": "ok", "data": {"name": "ARGON TEST SOCIETY", "clientId": "C-1", "bvn": "22233344455", "account": {"id": "A-1"}}}
        else:
            body = {"status": "104", "message": "account not found"}
        b = json.dumps(body).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", int(sys.argv[1])), H)
c = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); c.load_cert_chain(sys.argv[2], sys.argv[3])
s.socket = c.wrap_socket(s.socket, server_side=True)
s.serve_forever()
EOF
  PORT=$((20000 + $$ % 10000))
  "$PY" "$W/vfd_stub.py" "$PORT" "$W/cert.pem" "$W/key.pem" & STUB_PID=$!
  for _ in $(seq 50); do (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null && break; sleep 0.1; done
  L() { SSL_CERT_FILE="$W/cert.pem" VFD_BASE_URL="https://localhost:$PORT" VFD_ACCESS_TOKEN=test-token "$BIN" vfd-account-lookup "$1" 2>"$W/lerr"; }

  T="V11 the real lookup prints VFD's answer, one JSON line, and never the BVN"
  out=$(L 1000000001); rc=$?
  if ((rc != 0)); then red "exit $rc: $out $(head -c 300 "$W/lerr")"
  elif [[ $out == *22233344455* || $(cat "$W/lerr") == *22233344455* ]]; then red "the BVN was printed"
  elif [[ $out != '{"outcome":"accepted","code":"00","accountNo":"1000000001","name":"ARGON TEST SOCIETY","clientId":"C-1","accountId":"A-1"}' ]]; then red "printed: $out"
  else pass; fi

  T="V12 the real lookup: VFD knows no such account, exit 3, no name"
  out=$(L 1000000009); rc=$?
  if ((rc != 3)); then red "exit $rc: $out"
  elif [[ $out == *name* ]]; then red "printed a name for an account VFD does not know: $out"
  else pass; fi
fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
declared=$(grep -cE '^ *T="V[0-9]+ ' "${BASH_SOURCE[0]}")
if ((PASSED != declared)); then echo "INCOMPLETE: $PASSED of $declared declared tests passed, and none was red"; exit 1; fi
echo "all $declared green"
