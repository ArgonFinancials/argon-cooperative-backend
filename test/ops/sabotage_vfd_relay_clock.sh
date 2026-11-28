#!/usr/bin/env bash
# Every check in vfd_relay_clock_test.sh made to fire: each sabotage breaks one
# rule of 67-check-vfd-relay-clock.sh, and the test must go red where named.
#
#   bash test/ops/sabotage_vfd_relay_clock.sh
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
  patch "$repo/deploy/67-check-vfd-relay-clock.sh" "$3" "$4" "$d/deploy/67-check-vfd-relay-clock.sh" || { echo "$1: text not found once"; bad=1; return; }
  VFD_CLOCK_SCRIPT="$d/deploy/67-check-vfd-relay-clock.sh" bash "$here/vfd_relay_clock_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(grep -oE '^  RED  K[0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  miss=""; for e in $2; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then echo "$1 | WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then echo "$1 | STAYED GREEN on:$miss"; bad=1
  else echo "$1 | red: $reds| red as expected"; fi
}
brk ahead_unseen "K03" 'abs=${skew#-}' 'abs=$skew'
brk alert_at_sixty "K02 K04" ': "${VFD_CLOCK_MAX_SKEW:=10}"' ': "${VFD_CLOCK_MAX_SKEW:=60}"'
brk alert_at_zero "K05" ': "${VFD_CLOCK_MAX_SKEW:=10}"' ': "${VFD_CLOCK_MAX_SKEW:=0}"'
brk way_unsaid "K02" 'way="behind this server'"'"'s"' 'way="apart"'
brk unread_as_in_step "K06 K07" 'if [[ -z $theirs ]]; then' 'if [[ -z $theirs ]]; then exit 0;'
brk alert_silent "K02 K03 K04" '} | alert "VFD relay clock ${abs}s apart"' '} >/dev/null'
brk url_defaults "K08" 'url=
while' 'url=${VFD_BASE_URL:-}
while'
((bad)) && { echo "SABOTAGE: a check did not fire"; exit 1; }
echo "sabotage vfd relay clock: every check fired"
