#!/usr/bin/env bash
# Every check in prune_images_test.sh made to fire: each sabotage breaks one
# rule of 32-prune-api-images.sh, and the test must go red where named.
#
#   bash test/ops/sabotage_prune_images.sh
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
M=$(mktemp -d); trap 'rm -rf "$M"' EXIT
docker info >/dev/null 2>&1 || { echo "sabotage prune: NOT RUN (no Docker daemon here)"; exit 2; }

patch() { python3 - "$@" <<'PY'
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
  patch "$repo/deploy/32-prune-api-images.sh" "$3" "$4" "$d/deploy/32-prune-api-images.sh" || { echo "$1: text not found once"; bad=1; return; }
  PRUNE_SCRIPT="$d/deploy/32-prune-api-images.sh" bash "$here/prune_images_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(grep -oE '^  RED  I[0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  miss=""; for e in $2; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then echo "$1 | WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then echo "$1 | STAYED GREEN on:$miss"; bad=1
  else echo "$1 | red: $reds| red as expected"; fi
}
brk no_confirm_gate "I01" '((confirm)) || {' 'true || {'
brk keeps_too_few "I02" 'keep=3' 'keep=1'
brk release_unprotected "I02" '  [[ -f $f ]] && protect' '  false && protect'
brk any_repository "I03" "docker images \"\$repo\" --format" "docker images --format"
((bad)) && { echo "SABOTAGE: a check did not fire"; exit 1; }
echo "sabotage prune: every check fired"
