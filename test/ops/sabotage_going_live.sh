#!/usr/bin/env bash
# The red half for test/ops/going_live_test.sh: runs it against each sabotaged
# copy of deploy/ (sabotage_going_live.py) and says, per copy, whether the
# tests it should fail went red.
#
#   bash test/ops/sabotage_going_live.sh          (NGINX_BIN as for the test)
#
# Every copy is deleted at the end.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
M=$(mktemp -d)
trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python
"$py" "$here/sabotage_going_live.py" "$M" || { echo "the sabotage set does not apply to deploy/"; exit 1; }

bad=0
for d in "$M"/*/; do
  n=$(basename "$d"); exp=$(cat "$d/EXPECT")
  GL_DEPLOY="$d/deploy" bash "$here/going_live_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(grep -oE '^  RED  [A-Z]+[0-9]+' "$d/log" | awk '{print $2}' | sort -u | tr '\n' ' ')
  miss=""; for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  elif [[ " $reds" == *" N00 "* ]]; then v="NGINX DID NOT START: the copy proves nothing"; bad=1
  else v="red as expected"; fi
  echo "$n | expect $exp | red: $reds| $v"
done
((bad)) && { echo "SABOTAGE NOT CAUGHT"; exit 1; }
echo "every sabotaged copy was caught"
