#!/usr/bin/env bash
# The red half for test/ops/deploy_roles_test.sh: copies deploy/, breaks one
# rule per copy (40-migrate.sh or lib.sh), runs the tests against the copy, and
# requires the named tests to go red. A break whose text is no longer in the
# script stops the run (a sabotage that does not apply proves nothing).
#
#   bash test/ops/sabotage_deploy_roles.sh
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
export ARGON_ENV="${ARGON_ENV:-$repo/deploy/env/local.env}"
M=$(mktemp -d)
trap 'rm -rf "$M"' EXIT
python3 -c '' 2>/dev/null && py=python3 || py=python

bad=0
# sabotage NAME FILE EXPECT OLD NEW
sabotage() {
  local name=$1 file=$2 exp=$3 old=$4 new=$5 d="$M/$1"
  mkdir -p "$d"; cp -r "$repo/deploy" "$d/deploy"
  "$py" - "$old" "$new" "$d/deploy/$file" <<'PY' || { echo "$name: its text is not in $file"; bad=1; return; }
import sys
old, new, p = sys.argv[1:]
t = open(p, encoding="utf-8").read()
if t.count(old) != 1: sys.exit(1)
open(p, "w", encoding="utf-8", newline="\n").write(t.replace(old, new))
PY
  DEPLOY_SRC="$d/deploy" bash "$here/deploy_roles_test.sh" >"$d/log" 2>&1; local rc=$?
  local reds miss="" e v
  reds=$(grep -oE '^  RED  [CM][0-9]+' "$d/log" | awk '{print $2}' | sort | tr '\n' ' ')
  for e in $exp; do [[ " $reds" == *" $e "* ]] || miss+=" $e"; done
  if ((rc == 0)); then v="WHOLE SUITE GREEN"; bad=1
  elif [[ -n $miss ]]; then v="STAYED GREEN on:$miss"; bad=1
  else v="red as expected"; fi
  echo "$name | expect $exp | red: $reds| $v"
  grep '^  RED' "$d/log" | cut -c1-200 | sed 's/^/    /'
}

# 40-migrate.sh: the roles are not run at all (the deploy of e05a166).
sabotage roles_skipped 40-migrate.sh "M1 M2" \
  'bash "$DEPLOY_DIR/20-roles.sh" || die' 'true || die'
# The roles run, but their failure is ignored.
sabotage roles_failure_ignored 40-migrate.sh "M2" \
  'bash "$DEPLOY_DIR/20-roles.sh" || die "20-roles.sh failed: nothing migrated, nothing backed up"' \
  'bash "$DEPLOY_DIR/20-roles.sh" || true'
# lib.sh: a failed grant leaves the half-made database behind (the probe's leftover).
sabotage half_made_kept lib.sh "C1" \
  'drop_scratch_db "$db" >/dev/null || printf' 'true || printf'
# The easy wrong fix: drop on ANY failure, the CREATE's included.
sabotage drop_on_create_failure lib.sh "C2" \
  'TEMPLATE template0" </dev/null || return 1' \
  'TEMPLATE template0" </dev/null || { drop_scratch_db "$db" >/dev/null; return 1; }'
# The new role left out of the scratch grants.
sabotage wallet_not_granted lib.sh "C3" \
  'argon_auth, argon_wallet}"' 'argon_auth}"'

((bad)) && { echo "SABOTAGE NOT CAUGHT"; exit 1; }
echo "every sabotaged copy was caught"
