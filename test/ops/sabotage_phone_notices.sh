#!/usr/bin/env bash
# Every check in phone_notices_test.sh made to fire: each sabotage breaks one
# rule of 68-check-phone-notices.sh, and the test must go red where named.
#
#   bash test/ops/sabotage_phone_notices.sh
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
  patch "$repo/deploy/68-check-phone-notices.sh" "$3" "$4" "$d/deploy/68-check-phone-notices.sh" || { echo "$1: text not found once"; bad=1; return; }
  PHONE_NOTICES_SCRIPT="$d/deploy/68-check-phone-notices.sh" bash "$here/phone_notices_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(grep -oE '^  RED  N[0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  miss=""; for e in $2; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then echo "$1 | WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then echo "$1 | STAYED GREEN on:$miss"; bad=1
  else echo "$1 | red: $reds| red as expected"; fi
}
brk waiting_unseen "N02" "WHERE (s.id IS NULL AND n.at < now() - interval '15 minutes')" "WHERE (false)"
brk refused_unseen "N03" "OR (s.outcome <> 'accepted' AND s.at > now() - interval '26 hours')" "OR (false)"
brk waiting_at_once "N04" "n.at < now() - interval '15 minutes'" "n.at < now()"
brk refused_forever "N05" "AND s.at > now() - interval '26 hours')" ")"
brk whole_number "N02" "'...' || right(n.old_phone, 4)" "n.old_phone"
brk alert_silent "N02 N03" '} | alert "old phone not told of a number change (${#untold[@]})"' '} >/dev/null'
brk db_defaults "N06" 'db=
while' 'db=${ARGON_DB:-}
while'
brk unreadable_as_told "N07" '  *) echo "The notices to changed numbers'"'"' old phones could not be read from $db." | alert "old phones told: cannot read"; exit 3 ;;' '  *) exit 0 ;;'
((bad)) && { echo "SABOTAGE: a check did not fire"; exit 1; }
echo "sabotage phone notices: every check fired"
