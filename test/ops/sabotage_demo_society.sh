#!/usr/bin/env bash
# Every check in demo_society_test.sh is made to fire: each sabotage breaks one
# rule of 49-demo-society.sh (or the image's demo-pin) and the test must go
# red on the checks named, and on no fewer. A check that stays green on its
# own sabotage proves nothing (finding 47).
#
#   bash test/ops/sabotage_demo_society.sh
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
  reds=$(grep -oE '^  RED  D[0-9]+' "$log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  else v="red as expected"; fi
  echo "$name | expect $exp | red: $reds| $v"
}

REAL="$M/real/argon-api$EXE"; mkdir -p "$M/real"
(cd "$repo" && go build -o "$REAL" ./cmd/api) || { echo "cannot build argon-api"; exit 1; }

script_break() {  # NAME EXPECT OLD NEW
  local d="$M/$1"; mkdir -p "$d"; cp -r "$repo/deploy" "$d/deploy"
  patch "$repo/deploy/49-demo-society.sh" "$3" "$4" "$d/deploy/49-demo-society.sh" ||
    { echo "$1: its text is not in the script (once)"; bad=1; return; }
  DEMO_SCRIPT="$d/deploy/49-demo-society.sh" DEMO_PINGEN="$REAL" bash "$here/demo_society_test.sh" >"$d/log" 2>&1
  verdict "$1" "$2" "$d/log" $?
}

pin_break() {  # NAME EXPECT OLD NEW: in cmd/api/demopin.go
  local d="$M/$1"; mkdir -p "$d"
  patch "$repo/cmd/api/demopin.go" "$3" "$4" "$d/demopin.go" || { echo "$1: its text is not in demopin.go (once)"; bad=1; return; }
  printf '{"Replace":{"%s":"%s"}}' "$(cd "$repo" && pwd -W 2>/dev/null || pwd)/cmd/api/demopin.go" "$(cd "$d" && pwd -W 2>/dev/null || pwd)/demopin.go" >"$d/overlay.json"
  (cd "$repo" && go build -overlay "$d/overlay.json" -o "$d/argon-api$EXE" ./cmd/api) || { echo "$1: did not build"; bad=1; return; }
  DEMO_PINGEN="$d/argon-api$EXE" bash "$here/demo_society_test.sh" >"$d/log" 2>&1
  verdict "$1" "$2" "$d/log" $?
}

script_break no_confirm_gate "D01" '((confirm)) || { info "nothing done' 'true || { info "nothing done'
script_break code_reused "D04" '[[ $taken == 0 ]] ||' 'true ||'
script_break db_defaults "D08" 'confirm=0 db= pins=' 'confirm=0 db=$ARGON_DB pins='
script_break api_unchecked "D07" "python3 - \"\$API\" <<'PY' || die" "python3 - \"\$API\" <<'PY' || true"
script_break pins_ignored "D05" 'read -r p h < <(printf '"'"'%s\n'"'"' "${given[$i]:-}" | pingen' 'read -r p h < <(printf '"'"'\n'"'"' | pingen'
pin_break easy_pin_taken "D06" '	} else if err := auth.CheckNewPIN(pin, phones...); err != nil {' '	} else if err := auth.CheckNewPIN("380915"); err != nil {'
pin_break pin_in_hash "D03" '	fmt.Println(pin, h)' '	fmt.Println(pin, h[:len(h)-6]+pin)'

echo
((bad)) && { echo "SABOTAGE: a check did not fire"; exit 1; }
echo "sabotage: every check fired"
