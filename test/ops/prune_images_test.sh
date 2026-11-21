#!/usr/bin/env bash
# The image prune (32-prune-api-images.sh): six tiny images in a repository of
# the test's own, made a second apart; what is kept and what goes. Needs a
# Docker daemon: where there is none it says NOT RUN and fails, so a CI job
# without Docker cannot pass by default.
#
#   bash test/ops/prune_images_test.sh
# PRUNE_SCRIPT (a sabotaged copy) overrides.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e
SCRIPT="${PRUNE_SCRIPT:-$DEPLOY_DIR/32-prune-api-images.sh}"
docker info >/dev/null 2>&1 || { echo "prune images: NOT RUN (no Docker daemon here)"; exit 2; }
R="argon-prune-test-$$"; O="argon-prune-other-$$"; W=$(mktemp -d)
cleanup() { docker images --format '{{.Repository}}:{{.Tag}}' | grep -E "^($R|$O):" | xargs -r docker rmi -f >/dev/null 2>&1; rm -rf "$W"; }
trap cleanup EXIT
mk() { printf 'FROM scratch\nLABEL argon.prune.test=%s\n' "$2" | docker build -q -t "$1" - >/dev/null || die "cannot build $1"; }
for i in 1 2 3 4 5 6; do mk "$R:t$i" "$$-$i"; sleep 1.1; done
mk "$O:x" "$$-other"
echo t2 >"$W/RELEASE"; : >"$W/none"

T=""; RED=(); P=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; P=$((P + 1)); }
left() { docker images "$R" --format '{{.Tag}}' | sort | tr '\n' ' '; }
run() { PRUNE_REPO=$R PRUNE_RELEASES="$W/RELEASE $W/none" bash "$SCRIPT" "$@" >"$W/out" 2>&1; }

T="I01 without --confirm nothing is removed, and it says what would go"
run; rc=$?
[[ $rc == 0 && $(left) == "t1 t2 t3 t4 t5 t6 " ]] && grep -q "removing 2: t3 t1" "$W/out" && pass || red "exit $rc, left $(left): $(tr '\n' ' ' <"$W/out")"

T="I02 --confirm keeps the newest three and the one a RELEASE names, and removes the rest"
run --confirm; rc=$?
[[ $rc == 0 && $(left) == "t2 t4 t5 t6 " ]] && pass || red "exit $rc, left $(left)"

T="I03 nothing outside its repository is touched"
[[ -n $(docker images -q "$O:x") ]] && pass || red "$O:x was removed"

echo
declared=$(grep -cE '^T="I[0-9]+ ' "$0")
if ((${#RED[@]})); then echo "prune images: ${#RED[@]} RED of $declared: ${RED[*]}"; exit 1; fi
((P == declared)) || { echo "prune images: $P passed of $declared declared"; exit 1; }
echo "prune images: all $declared green"
