#!/usr/bin/env bash
# The red half for test/ops/society_vfd_account_test.sh: one rule broken per
# copy, and the named tests must go red. Script breaks run on a copy of
# deploy/; lookup breaks build the real binary with `go build -overlay` (the
# patched file swapped in at build time). A break whose text is not in its
# file stops the run.
#
#   bash test/ops/sabotage_vfd_account.sh
# The lookup's V11/V12 breaks need Linux (the test's SSL_CERT_FILE); elsewhere
# they are reported NOT RUN, never passed.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
M=$(mktemp -d)
trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python
EXE=""; [[ $OSTYPE == msys* || $OSTYPE == cygwin* ]] && EXE=.exe

patch() {  # patch FILE OLD NEW OUT
  "$py" - "$@" <<'PY'
import sys
p, old, new, out = sys.argv[1:]
t = open(p, encoding="utf-8").read()
if t.count(old) != 1: sys.exit(1)
open(out, "w", encoding="utf-8", newline="\n").write(t.replace(old, new))
PY
}

bad=0
verdict() {  # verdict NAME EXPECT LOG RC
  local name=$1 exp=$2 log=$3 rc=$4 reds miss="" e v
  reds=$(grep -oE '^  RED  V[0-9]+' "$log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  else v="red as expected"; fi
  echo "$name | expect $exp | red: $reds| $v"
}

script_break() {  # NAME EXPECT OLD NEW
  local d="$M/$1"; mkdir -p "$d"; cp -r "$repo/deploy" "$d/deploy"
  patch "$repo/deploy/48-society-vfd-account.sh" "$3" "$4" "$d/deploy/48-society-vfd-account.sh" ||
    { echo "$1: its text is not in the script (once)"; bad=1; return; }
  VFD_SCRIPT="$d/deploy/48-society-vfd-account.sh" VFD_LOOKUP_BIN="$REAL" bash "$here/society_vfd_account_test.sh" >"$d/log" 2>&1
  verdict "$1" "$2" "$d/log" $?
}

lookup_break() {  # NAME EXPECT OLD NEW
  local d="$M/$1"; mkdir -p "$d"
  patch "$repo/cmd/api/vfdlookup.go" "$3" "$4" "$d/vfdlookup.go" ||
    { echo "$1: its text is not in vfdlookup.go (once)"; bad=1; return; }
  printf '{"Replace":{"%s":"%s"}}' "$(cd "$repo" && pwd -W 2>/dev/null || pwd)/cmd/api/vfdlookup.go" "$(cd "$d" && pwd -W 2>/dev/null || pwd)/vfdlookup.go" >"$d/overlay.json"
  (cd "$repo" && go build -overlay "$d/overlay.json" -o "$d/argon-api$EXE" ./cmd/api) || { echo "$1: the sabotaged lookup did not build"; bad=1; return; }
  VFD_LOOKUP_BIN="$d/argon-api$EXE" bash "$here/society_vfd_account_test.sh" >"$d/log" 2>&1
  verdict "$1" "$2" "$d/log" $?
}

REAL="$M/real/argon-api$EXE"; mkdir -p "$M/real"
(cd "$repo" && go build -o "$REAL" ./cmd/api) || { echo "cannot build the real lookup"; exit 1; }

# ---- the script ----------------------------------------------------------------
S48='--name|--client|--client-id|--account-id)'
script_break typed_name_ok "V05" "    $S48" "    --unused-flag)"
script_break login_from_caller "V05" '    --login)   die' '    --login)   login_in=${2-}; shift 2 ;; --login-was) die'
script_break no_confirm_gate "V02" '((confirm)) || { info "nothing done' 'true || { info "nothing done'
script_break refused_unsaid "V03" '  3) die "VFD knows no account' '  3) die "the lookup failed for'
script_break unreached_unsaid "V04" '  4) die "VFD did not answer' '  4) die "the lookup failed for'
script_break exit_code_lost "V03 V04" 'rc=0; answer=$(lookup "${v[account]}" 2>"${TMPDIR:-/tmp}/vfd-lookup.$$.err") || rc=$?' 'answer=$(lookup "${v[account]}" 2>"${TMPDIR:-/tmp}/vfd-lookup.$$.err"); rc=$?'
script_break db_defaults "V06" 'confirm=0 db=' 'confirm=0 db=$ARGON_DB'
script_break society_unchecked "V07" "[[ -n \$society ]] || die \"no society with the code '\${v[society]}' in \$db\"" ''
script_break number_unchecked "V09" '[[ $number == "${v[account]}" ]] ||' 'true ||'
script_break client_unchecked "V10" 'blank "$client" && die' 'false && die'

# ---- the lookup ----------------------------------------------------------------
lookup_break http_ok "V13" 'if !strings.HasPrefix(base, "https://") || token == "" {' 'if false && !strings.HasPrefix(base, "https://") || token == "" {'
if [[ $(uname -s) == Linux ]]; then
  # The BVN leaks into what is printed (here, beside the name).
  lookup_break bvn_printed "V11" 'a.AccountNo, a.Name, a.ClientID, a.AccountID = l.Party.AccountNo, l.Party.Name, l.Party.ClientID, l.Party.AccountID' 'a.AccountNo, a.Name, a.ClientID, a.AccountID = l.Party.AccountNo, l.Party.Name+" "+l.Party.BVN, l.Party.ClientID, l.Party.AccountID'
  lookup_break refused_as_found "V12" '	case vfd.Refused:
		return 3' '	case vfd.Refused:
		return 0'
else
  echo "NOT RUN bvn_printed, refused_as_found: they break V11/V12, which need Linux (CI runs them)"
  bad=1
fi

((bad)) && { echo "SABOTAGE NOT CAUGHT (or not run)"; exit 1; }
echo "every sabotaged copy was caught"
