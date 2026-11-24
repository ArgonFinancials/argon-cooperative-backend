#!/usr/bin/env bash
# Every check in vfd_holds_test.sh made to fire: each sabotage breaks one rule
# of 66-check-vfd-holds.sh, and the test must go red where named.
#
#   bash test/ops/sabotage_vfd_holds.sh
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
M=$(mktemp -d); trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python

patch() { "$py" - "$@" <<'PY'
import sys
p, old, new, out = sys.argv[1:]
t = open(p, encoding="utf-8").read()
if t.count(old) != 1: sys.exit(1)
open(out, "w", encoding="utf-8", newline="\n").write(t.replace(old, new))
PY
}
bad=0
brk() {  # NAME EXPECT OLD NEW
  local d="$M/$1"; mkdir -p "$d"; cp -r "$repo/deploy" "$d/deploy"
  patch "$repo/deploy/66-check-vfd-holds.sh" "$3" "$4" "$d/deploy/66-check-vfd-holds.sh" || { echo "$1: text not found once"; bad=1; return; }
  VFD_HOLDS_SCRIPT="$d/deploy/66-check-vfd-holds.sh" bash "$here/vfd_holds_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(grep -oE '^  RED  H[0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  miss=""; for e in $2; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then echo "$1 | WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then echo "$1 | STAYED GREEN on:$miss"; bad=1
  else echo "$1 | red: $reds| red as expected"; fi
}
brk needs_vfd_unread "H02" "WHERE kind = 'transfer_needs_vfd' OR (" "WHERE false AND kind = 'transfer_needs_vfd' OR ("
brk stuck_unread "H03" "(kind = 'transfer_stuck' AND since < now() - interval '24 hours')" "(false)"
brk stuck_too_soon "H04" "since < now() - interval '24 hours'" "since < now() - interval '1 hour'"
brk alert_silent "H02 H03" '} | alert "payouts held waiting for VFD (${#held[@]})"' '} >/dev/null'
brk db_defaults "H05" 'db=
while' 'db=${ARGON_DB:-}
while'
brk unreadable_as_none "H06" '  *) echo "The payouts held for VFD could not be read from $db." | alert "payouts held for VFD: cannot read"; exit 3 ;;' '  *) exit 0 ;;'
((bad)) && { echo "SABOTAGE: a check did not fire"; exit 1; }
echo "sabotage vfd holds: every check fired"
