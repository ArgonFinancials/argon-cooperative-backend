#!/usr/bin/env bash
# THE OPERATOR MAKES A DEMONSTRATION SOCIETY, with five people ready to sign
# in: no slips, no activation. (0035; LEDGER_CONTRACT.md §12, 2026-11-21.
# Tests: test/ops/demo_society_test.sh.)
#
#   49-demo-society.sh --code DEMO3 --name "Lagos demonstration" \
#       --operator "Your Name" --reason "why" --db NAME [--pins P1,P2,P3,P4,P5] [--confirm]
#
# In one run: a NEW society, marked a demonstration and named "TEST DATA: ...";
# its account floor (₦1,000) and withdrawal charges (DEMO figures); a chairman,
# a treasurer and a secretary, all signatories; two members, registered by
# that secretary through the API; their opening balances, posted by that
# treasurer through the API; a PIN for each of the five (--pins, in that
# order, or drawn). Then a table to hand out: name, role, member number, PIN.
#
# IT NEVER TOUCHES AN EXISTING SOCIETY. It only makes a new one (a code
# already in use is refused before anything is written), and the database
# sets an operator's PIN only for someone who belongs to demonstration
# societies and to nothing real (0035: demo_pin_only_for_demo). A real
# society's officers still enrol with a slip; this path cannot reach them.
#
# The PINs are made into hashes by the API image itself (its demo-pin
# command, the same rule as any new PIN). Only hashes reach the database. The
# PINs are printed once, here, for you to hand out. Run it where the API is
# up: the members and the balances go through it, as an officer's would.
#
# Without --confirm it shows what it would make and makes nothing (exit 3).
# --db is required, never defaulted (finding 69).
source "$(dirname "$0")/lib.sh"
set +E

usage() { sed -n '6,7p' "$0" | sed 's/^# \{0,1\}//' >&2; }

declare -A v=()
flags="code name operator reason"
confirm=0 db= pins=
while (($#)); do
  case $1 in
    --confirm) confirm=1; shift ;;
    --db)      db=${2-}; shift 2 ;;
    --pins)    pins=${2-}; shift 2 ;;
    --login)   die "--login is refused: the script records the server login itself (user@host, and who ran sudo)." ;;
    -h|--help) usage; exit 1 ;;
    --*)       f=${1#--}; [[ " $flags " == *" $f "* ]] || { usage; die "unknown argument '$1'"; }
               v[$f]=${2-}; shift 2 ;;
    *)         usage; die "unknown argument '$1'" ;;
  esac
done

blank() { [[ -z ${1//[[:space:]]/} ]]; }
for f in $flags; do blank "${v[$f]-}" && die "--$f is required"; done
[[ -n $db ]] || die "--db is required: name the database (on the server, the one in ARGON_DB). The script never chooses one itself (finding 69)."
[[ ${v[code]} =~ ^[A-Z0-9]{3,12}$ ]] || die "--code '${v[code]}' is 3 to 12 capital letters or digits"
IFS=',' read -r -a given <<<"$pins"
[[ -z $pins || ${#given[@]} -eq 5 ]] || die "--pins is five PINs, comma-separated, in the order chairman, treasurer, secretary, member one, member two"

login="$(id -un)@$(hostname) [login, verified by id -un]"
[[ -n ${SUDO_USER:-} ]] && login+="; sudo by $SUDO_USER [claimed: SUDO_USER from the environment, not verified]"
API=${DEMO_API:-http://127.0.0.1:${API_PORT:-8080}}
q() { pg_as argon_migrator "$db" -q -At -v ON_ERROR_STOP=1 "$@"; }

c=${v[code]}
roles=(chairman treasurer secretary member member)
names=("Demo Chairman" "Demo Treasurer" "Demo Secretary" "Demo Member One" "Demo Member Two")
numbers=("$c-001" "$c-002" "$c-003" "$c-004" "$c-005")
phones=()
for i in 0 1 2 3 4; do phones+=("+23470$(printf '%08d' $(( (RANDOM * 32768 + RANDOM) % 100000000 )))"); done
balances=(25000000 18000000) # ₦250,000 and ₦180,000, the two members'

taken=$(q -v code="$c" -f - <<'SQL'
SELECT count(*) FROM societies WHERE code = :'code';
SQL
) || die "cannot read the database '$db'"
[[ $taken == 0 ]] || die "a society with the code $c exists: this command only makes NEW demonstration societies, and never touches an existing one. Choose another --code."

cat >&2 <<EOF

A demonstration society (TEST DATA)
  society     $c, "TEST DATA: ${v[name]}", marked a demonstration for good
  floor       ₦1,000; withdrawal charges: DEMO figures (₦10/₦25/₦50 fee by band, 7.5% VAT on it)
  people      ${names[0]} (chairman), ${names[1]} (treasurer), ${names[2]} (secretary): all sign
              ${names[3]}, ${names[4]}: members, opening balances ₦250,000 and ₦180,000
  PINs        $([[ -n $pins ]] && echo "as given" || echo "drawn"), printed once at the end
  through     the API at $API (the members and the balances, as an officer's would)
  by          ${v[operator]}  [$login]
  why         ${v[reason]}
EOF
((confirm)) || { info "nothing done: run again with --confirm"; exit 3; }

# The API first: if it cannot answer, nothing is made.
python3 - "$API" <<'PY' || die "the API at $API does not answer: nothing was made (start it, or set DEMO_API)"
import sys, urllib.request
urllib.request.urlopen(sys.argv[1] + "/healthz", timeout=10).read()
PY

# The PINs, as hashes, by the API image (or DEMO_PINGEN, the tests' binary).
pingen() {
  if [[ -n ${DEMO_PINGEN:-} ]]; then "$DEMO_PINGEN" demo-pin "$1"
  else docker run --rm -i "argon-api:$(cat "$REPO_DIR/RELEASE")" demo-pin "$1"; fi
}
pin=() hash=()
for i in 0 1 2 3 4; do
  read -r p h < <(printf '%s\n' "${given[$i]:-}" | pingen "${phones[$i]}") || true
  [[ $p =~ ^[0-9]{6}$ && $h == '$argon2id$'* ]] ||
    die "no PIN for ${names[$i]}$([[ -n $pins ]] && echo ": '${given[$i]}' is refused (six digits, not a run, not a repeat, not the end of the phone number)" || echo ": is argon-api built?"). Nothing was made."
  pin+=("$p"); hash+=("$h")
done

# 1. The society, its rules, its officers and their PINs: one transaction.
B='[{"from":0,"to":500000,"flat":%s,"bp":0,"cap":null},{"from":500001,"to":5000000,"flat":%s,"bp":0,"cap":null},{"from":5000001,"to":null,"flat":%s,"bp":0,"cap":null}]'
society=$(q -1 -v code="$c" -v name="${v[name]}" -v op="${v[operator]}" -v login="$login" -v why="${v[reason]}" \
  -v margin="$(printf "$B" 1000 2500 5000)" -v vat="$(printf "$B" 75 188 375)" \
  -v zero='[{"from":0,"to":null,"flat":0,"bp":0,"cap":null}]' -v src="DEMO DATA, not the real schedule" \
  -v n1="${names[0]}" -v n2="${names[1]}" -v n3="${names[2]}" -v p1="${phones[0]}" -v p2="${phones[1]}" -v p3="${phones[2]}" \
  -v m1="${numbers[0]}" -v m2="${numbers[1]}" -v m3="${numbers[2]}" -v h1="${hash[0]}" -v h2="${hash[1]}" -v h3="${hash[2]}" -f - <<'SQL'
SELECT operator_create_demo_society(:'code', :'name', :'op', :'login', :'why') AS sid \gset
SELECT operator_set_account_floor(:'sid', 100000, :'op', :'login', 'TEST DATA', current_date, :'why') AS _void \gset
SELECT operator_add_charge_series(:'sid', 'withdrawal_out', 'argon_margin', current_date, :'src', :'margin'::jsonb, :'op', :'login', :'why') AS _void \gset
SELECT operator_add_charge_series(:'sid', 'withdrawal_out', 'vat_on_argon_fee', current_date, :'src', :'vat'::jsonb, :'op', :'login', :'why') AS _void \gset
SELECT operator_add_charge_series(:'sid', 'withdrawal_out', 'partner_fee', current_date, :'src', :'zero'::jsonb, :'op', :'login', :'why') AS _void \gset
SELECT operator_add_charge_series(:'sid', 'withdrawal_out', 'statutory_levy', current_date, :'src', :'zero'::jsonb, :'op', :'login', :'why') AS _void \gset
SELECT operator_appoint_founding_officer(:'sid', :'n1', :'p1', :'m1', '2024-01-15', 'chairman', true, :'op', :'login', 'TEST DATA', current_date, :'why') AS o1 \gset
SELECT operator_appoint_founding_officer(:'sid', :'n2', :'p2', :'m2', '2024-01-15', 'treasurer', true, :'op', :'login', 'TEST DATA', current_date, :'why') AS o2 \gset
SELECT operator_appoint_founding_officer(:'sid', :'n3', :'p3', :'m3', '2024-01-15', 'secretary', true, :'op', :'login', 'TEST DATA', current_date, :'why') AS o3 \gset
SELECT operator_set_demo_pin((SELECT user_id FROM members WHERE id = :'o1'), :'h1', :'op', :'login', :'why') AS _void \gset
SELECT operator_set_demo_pin((SELECT user_id FROM members WHERE id = :'o2'), :'h2', :'op', :'login', :'why') AS _void \gset
SELECT operator_set_demo_pin((SELECT user_id FROM members WHERE id = :'o3'), :'h3', :'op', :'login', :'why') AS _void \gset
SELECT :'sid';
SQL
) || die "the database refused (its reason is above): nothing was made"
[[ $society =~ ^[0-9a-f-]{36}$ ]] || die "the database answered '$society', not a society: nothing was made"
ok "made $c, its rules, its three officers and their PINs"
incomplete() { die "$1. $c exists but is INCOMPLETE; it is a demonstration, harmless, and stays marked. Run again with a new --code."; }

# 2. The members, registered by the secretary; 3. their balances, posted by
# the treasurer. Both through the API, on their own PINs and step-ups.
# The API calls: the code in a variable, so the PINs reach python on stdin, never argv.
read -r -d '' APICODE <<'PY' || true # read -d '' ends at EOF with status 1, by design
import json, sys, uuid, datetime, urllib.request, urllib.error
api, soc, step = sys.argv[1], sys.argv[2], sys.argv[3]
nums = sys.argv[4:9]; mname = sys.argv[9:11]; mphone = sys.argv[11:13]; bal = [int(x) for x in sys.argv[13:15]]
pins = [l.strip() for l in sys.stdin.read().splitlines()]
def call(method, path, token=None, body=None, key=None):
    req = urllib.request.Request(api + path, data=json.dumps(body).encode() if body is not None else None, method=method)
    req.add_header("Content-Type", "application/json")
    if token: req.add_header("Authorization", "Bearer " + token)
    if key: req.add_header("Idempotency-Key", key)
    try:
        with urllib.request.urlopen(req, timeout=60) as r: return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try: return e.code, json.loads(raw)
        except Exception: return e.code, {"code": "http", "message": raw[:200].decode(errors="replace")}
def need(ok, what, b):
    if not ok: sys.exit(f"{what}: {b.get('code')} {b.get('message')}")
def signin(i):
    st, b = call("POST", "/auth/sign-in", body={"credential": nums[i], "pin": pins[i]})
    need(st == 200 and b.get("outcome") == "signed_in" and b.get("sessionToken"), f"{nums[i]} signing in", b if st != 200 else {"code": b.get("outcome"), "message": "not signed in on the PIN alone"})
    return b["sessionToken"]
def proof(tok, i, act):
    st, b = call("POST", "/auth/pin-proofs", tok, {"pin": pins[i], "act": act})
    need(st in (200, 201), f"{nums[i]}'s step-up for {act}", b)
    return b["proofId"]
if step == "members":
    tok = signin(2)
    for k in (0, 1):
        st, b = call("POST", f"/societies/{soc}/members", tok, {"memberNumber": nums[3 + k], "fullName": mname[k], "phone": mphone[k],
            "branchCode": "HQ", "joinedOn": "2024-01-15", "collectionChannel": "payroll", "pinProofId": proof(tok, 2, "member_register")})
        need(st == 201, f"the secretary registering {mname[k]}", b)
        print(b["memberId"])
elif step == "balances":
    tok = signin(1)
    st, b = call("POST", f"/societies/{soc}/ledger/batches", tok, {"kind": "opening_balance", "asOf": datetime.date.today().isoformat(),
        "confirmedTotalKobo": str(sum(bal)), "rows": [{"line": 2 + k, "memberNumber": nums[3 + k], "amountKobo": str(bal[k])} for k in (0, 1)],
        "pinProofId": proof(tok, 1, "ledger_batch")}, key="demo-" + uuid.uuid4().hex)
    need(st in (200, 201), "the treasurer posting the opening balances", b)
elif step == "check":
    for i in range(5): signin(i)
PY
api() {  # api STEP: prints its result lines
  printf '%s\n' "${pin[@]}" | python3 -c "$APICODE" "$API" "$society" "$1" "${numbers[@]}" "${names[3]}" "${names[4]}" "${phones[3]}" "${phones[4]}" "${balances[@]}"
}
mapfile -t members < <(api members | tr -d '\r') || true
((${#members[@]} == 2)) || incomplete "the secretary could not register the members (the reason is above)"
q -v op="${v[operator]}" -v login="$login" -v why="${v[reason]}" -v a="${members[0]}" -v b="${members[1]}" \
  -v h4="${hash[3]}" -v h5="${hash[4]}" -f - >/dev/null <<'SQL' || incomplete "the database refused the members' PINs (its reason is above)"
SELECT operator_set_demo_pin(user_id, :'h4', :'op', :'login', :'why') FROM members WHERE id = :'a';
SELECT operator_set_demo_pin(user_id, :'h5', :'op', :'login', :'why') FROM members WHERE id = :'b';
SQL
api balances || incomplete "the treasurer could not post the opening balances (the reason is above)"
# Founding is over: no more founding officers by the operator (0027).
q -v s="$society" -v op="${v[operator]}" -v login="$login" -v why="${v[reason]}" -f - >/dev/null <<'SQL' || incomplete "the society could not be made active"
SELECT operator_set_lifecycle(:'s', 'active', :'op', :'login', :'why');
SQL
# And every one of the five signs in, on the PIN alone, before the table is shown.
api check || incomplete "a PIN did not sign its person in (the reason is above)"
unset hash
ok "made $c: everyone signs in"

cat <<EOF

  TEST DATA: ${v[name]} ($c). Hand these out; they are shown once.

  NAME              ROLE        MEMBER NUMBER   PIN
EOF
for i in 0 1 2 3 4; do printf '  %-17s %-11s %-15s %s\n' "${names[$i]}" "${roles[$i]}" "${numbers[$i]}" "${pin[$i]}"; done
cat <<EOF

  Sign in with the member number and the PIN, on any device. Opening balances:
  ${numbers[3]} ₦250,000, ${numbers[4]} ₦180,000. Withdrawals need two of the three
  officers. No money moves: the wallet is off.
EOF
