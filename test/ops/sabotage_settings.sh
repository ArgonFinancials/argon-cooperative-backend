#!/usr/bin/env bash
# The red half for test/ops/society_settings_test.sh: runs it against each
# sabotaged copy of deploy/45-society-settings.sh (sabotage_settings.py) and
# says, per copy, whether the tests it should fail went red.
#
#   bash test/ops/sabotage_settings.sh
#
# Prints only the verdict and each red test's reason, never what a copy printed:
# one copy prints the migrator password, to prove the test that looks for it can
# fail. Every copy and its log are deleted at the end.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
export ARGON_ENV="${ARGON_ENV:-$here/../../deploy/env/local.env}"
M=$(mktemp -d)
trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python
"$py" "$here/sabotage_settings.py" "$M" || { echo "the sabotage set does not apply to the script"; exit 1; }

bad=0
for d in "$M"/*/; do
  n=$(basename "$d"); exp=$(cat "$d/EXPECT")
  SETTINGS_SCRIPT="$d/45-society-settings.sh" bash "$here/society_settings_test.sh" >"$d/log" 2>&1; rc=$?
  reds=$(sed 's/\x1b\[[0-9;]*m//g' "$d/log" | grep -oE '^  RED  S[0-9]+' | awk '{print $2}' | sort | tr '\n' ' ')
  miss=""; for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  else v="red as expected"; fi
  echo "$n | expect $exp | red: $reds| $v"
  sed 's/\x1b\[[0-9;]*m//g' "$d/log" | grep '^  RED' | sed 's/^  RED  \(S[0-9]*\) [^—]*—/      \1:/' | cut -c1-200
done
((bad)) && { echo "SABOTAGE NOT CAUGHT"; exit 1; }
echo "every sabotaged copy was caught"
