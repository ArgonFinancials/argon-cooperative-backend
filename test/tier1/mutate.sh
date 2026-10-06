#!/usr/bin/env bash
# Mutation run for a Tier 1 acceptance suite.
#
#   mutate.sh [0002|0003|0004|0005|0006|0007|0008|stepup|api|batch|stepup_api|signin_api|books|loans_api] [--only GLOB] [--partial] [--svc]
#
# Each suite (suite_<n>.sh) names its tests, its no-rules baseline "M0", its
# mutants (DELIBERATELY WRONG implementations, one file each) and which tests
# each must turn RED. The run fails if:
#   - a targeted test PASSES against the broken schema/service (tests nothing)
#   - a targeted test fails in its own FIXTURE           (red for the wrong reason)
#   - a control fails against M0                         (the control is broken)
#   - a test is targeted by no mutant                    (unproven)
#
# --only GLOB   run only the mutants whose name matches (a batch)
# --partial     report untargeted tests as NOT YET COVERED instead of failing;
#               a partial run never prints the all-clear
# --svc         also run the service breaks (SVC_TARGET_OF)
#
# GREEN (every test passes against the real migrations) runs only once the
# migration the suite is for exists. Until then the run says so, rather than
# counting an absent half as a pass.
source "$(dirname "$0")/../../deploy/lib.sh"
[[ $TARGET != host ]] || die "the mutation suite runs on a local server (TARGET=docker or local), never the EC2 host"
require_env POSTGRES_PASSWORD ARGON_MIGRATOR_PASSWORD

SUITE=0002 ONLY='*' PARTIAL=0 SVC=0
while (($#)); do case $1 in
  0002|0003|0004|0005|0006|0007|0008|stepup|api|batch|stepup_api|signin_api|books|loans_api) SUITE=$1; shift ;;
  --only) ONLY=$2; shift 2 ;;
  --partial) PARTIAL=1; shift ;;
  --svc) SVC=1; shift ;;
  *) die "unknown arg $1" ;;
esac; done

export PYTHONIOENCODING=utf-8   # test messages contain −, ₦, §
# Paths handed to the test binary must be native: on Windows, C:/..., not /c/...
native() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else echo "$1"; fi; }
HERE="$(native "$REPO_DIR/test/tier1")"
BIN="$REPO_DIR/bin/tier1.test$EXE"
REPO_DIR="$(native "$REPO_DIR")"
export ARGON_TEST_ADMIN_URL="$(admin_url)"
export ARGON_TEST_MIGRATOR_URL="postgres://argon_migrator:${ARGON_MIGRATOR_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"

# shellcheck disable=SC1090
source "$HERE/suite_$SUITE.sh"
SVC_RUN=${SVC_RUN:-'^TestAuthS'}
SVC_ENV=${SVC_ENV:-ARGON_TEST_AUTH_FAKE}   # the variable that selects a broken fake
abs() { local IFS=,; local out=(); for f in $1; do out+=("$REPO_DIR/$f"); done; echo "${out[*]}"; }
BASE=$(abs "$SUITE_BASE")

info "suite $SUITE: building test binary"
mkdir -p "$REPO_DIR/bin"
(cd "$REPO_DIR" && GOMAXPROCS=1 go test -p 1 -c -tags tier1 -o "$BIN" ./test/tier1/)
# From the source, not -test.list: listing still runs TestMain, which needs a DB.
mapfile -t ALL < <(grep -ohE '^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)' "$HERE"/*_test.go \
  | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' | grep -E "$SUITE_INCLUDE" | grep -vE "$SUITE_EXCLUDE")
# An empty list would make every check below pass vacuously.
((${#ALL[@]} > 0)) || die "found no tests for suite $SUITE in $HERE"
info "${#ALL[@]} tests in suite $SUITE"

out=$(mktemp -d); trap 'rm -rf "$out"' EXIT
# Labels become file names: no characters Windows forbids (":" among them).
# run SCHEMA LABEL RUNREGEX [ENV=VAL] -> $out/LABEL.status lines "Test STATUS detail"
run() {
  local schema=$1 label=$2 re=$3; shift 3
  local args=(-test.v -test.count=1)
  [[ -n $re ]] && args+=(-test.run "$re")
  [[ -n $SUITE_SKIP ]] && args+=(-test.skip "$SUITE_SKIP")
  env "$@" ARGON_TEST_SCHEMA="$schema" "$BIN" "${args[@]}" > "$out/$label.log" 2>&1 || true
  python - "$out/$label.log" > "$out/$label.status" <<'PY'
import re, sys
cur, buf, res = None, [], {}
for ln in open(sys.argv[1], encoding="utf-8", errors="replace"):
    m = re.match(r"=== RUN\s+(\S+)", ln)
    if m: cur, buf = m.group(1), []; continue
    m = re.match(r"\s*--- (PASS|FAIL): (\S+)", ln)
    if m:
        st, name = m.groups(); text = " ".join(b.strip() for b in buf)
        if st == "FAIL": st = "BROKEN" if "FIXTURE:" in text else "RED"
        msg = re.sub(r"^\S+\.go:\d+:\s*", "", text)[:150]
        res[name] = (st, msg); cur = None; continue
    if cur: buf.append(ln)
for k, (s, m) in res.items(): print(k, s, m)
if not res:
    print("__SETUP__ BROKEN", open(sys.argv[1], encoding="utf-8", errors="replace").read()[-300:].replace("\n", " "))
PY
}

status_of() { awk -v t="$1" '$1==t {print $2; exit}' "$out/$2.status"; }
detail_of() { awk -v t="$1" '$1==t {$1=$2=""; sub(/^  /,""); print; exit}' "$out/$2.status"; }

fail=0
declare -A COVERED

report() {  # label pattern describe-file
  local label=$1 pattern=$2 desc=${3:-}
  if grep -q '^__SETUP__' "$out/$label.status"; then
    printf '\n\033[31m%s: SCHEMA DID NOT APPLY\033[0m %s\n' "$label" "$(cut -d' ' -f3- "$out/$label.status")"; fail=1; return
  fi
  printf '\n\033[1m%s\033[0m  %s\n' "$label" "$( [[ -f $desc ]] && sed -n 's/^-- //p' "$desc" | head -1)"
  for t in "${ALL[@]}"; do
    [[ $t =~ ^($pattern) ]] || continue   # prefix match
    COVERED[$t]=1
    local st; st=$(status_of "$t" "$label")
    case $st in
      RED)    printf '   \033[32mred   ✓\033[0m %-62s %s\n' "$t" "$(detail_of "$t" "$label" | cut -c1-88)" ;;
      PASS)   printf '   \033[31mPASSED ON BROKEN %s ✗\033[0m %s\n' "$( [[ $label == svc_* ]] && echo SERVICE || echo SCHEMA)" "$t"; fail=1 ;;
      BROKEN) printf '   \033[33mBROKEN FIXTURE ?\033[0m %s — %s\n' "$t" "$(detail_of "$t" "$label")"; fail=1 ;;
      *)      printf '   \033[31mNOT RUN ?\033[0m %s\n' "$t"; fail=1 ;;
    esac
  done
}

# ---- GREEN ------------------------------------------------------------------
if [[ -f $REPO_DIR/$SUITE_GREEN_NEEDS ]]; then
  REAL=$(ls "$REPO_DIR"/migrations/*.up.sql | sort | tr '\n' ',' | sed 's/,$//')
  info "GREEN: the real migrations"
  # The real code: the fake's variable must be UNSET here, or GREEN would test the fake.
  run "$REAL" GREEN "${SUITE_GREEN_RUN:-$SUITE_RUN}" -u "$SVC_ENV"
  green_n=0
  for t in "${ALL[@]}"; do
    st=$(status_of "$t" GREEN)
    if [[ $st == PASS ]]; then green_n=$((green_n+1))
    else printf '   \033[31m%s ✗\033[0m %s — %s\n' "${st:-NOT RUN}" "$t" "$(detail_of "$t" GREEN)"; fail=1; fi
  done
  printf '   %d/%d pass against the real schema\n' "$green_n" "${#ALL[@]}"
else
  printf '\n\033[33mGREEN NOT RUN\033[0m — %s does not exist yet (tests first). The red half below is all this run proves.\n' "$SUITE_GREEN_NEEDS"
fi

# ---- M0 ---------------------------------------------------------------------
if [[ -n ${TARGET_OF[M0]:-} ]]; then   # a suite with no SQL baseline (the API) has no M0
info "M0: the no-rules baseline"
run "$BASE" M0 "$SUITE_RUN"
report M0 "${TARGET_OF[M0]}"
printf '\n\033[1mM0 controls\033[0m  (must PASS on a schema with no rules — proves each statement shape is valid)\n'
for t in "${ALL[@]}"; do
  [[ $t =~ $CONTROL_RE ]] || continue
  [[ $t =~ $SVC_RUN ]] && continue
  st=$(status_of "$t" M0)
  if [[ $st == PASS ]]; then printf '   \033[32mpass  ✓\033[0m %s\n' "$t"
  else printf '   \033[31m%s ✗\033[0m %s — %s\n' "$st" "$t" "$(detail_of "$t" M0)"; fail=1; fi
done
fi

# ---- SQL mutants ------------------------------------------------------------
shopt -s nullglob
for f in "$HERE/$SUITE_MUTDIR"/*.sql; do
  label=$(basename "$f" .sql)
  [[ $label == $ONLY ]] || continue
  [[ -n ${TARGET_OF[$label]:-} ]] || { printf '\n\033[31m%s has no expectation\033[0m\n' "$label"; fail=1; continue; }
  run "$BASE,$f" "$label" "$SUITE_RUN"
  report "$label" "${TARGET_OF[$label]}" "$f"
done
for label in "${!TARGET_OF[@]}"; do
  [[ $label == M0 || -f "$HERE/$SUITE_MUTDIR/$label.sql" ]] || { printf '\n\033[31mexpectation for %s has no mutant file\033[0m\n' "$label"; fail=1; }
done

# ---- service breaks ---------------------------------------------------------
if ((SVC)) && ((${#SVC_TARGET_OF[@]})); then
  info "REFERENCE: the service fake with no break (test code, not the service)"
  run "$BASE" svc_none "$SVC_RUN" "$SVC_ENV="
  for t in "${ALL[@]}"; do
    [[ $t =~ $SVC_RUN ]] || continue
    st=$(status_of "$t" svc_none)
    if [[ $st == PASS ]]; then printf '   \033[32mpass  ✓\033[0m %s\n' "$t"
    else printf '   \033[31m%s ✗\033[0m %s — %s\n' "${st:-NOT RUN}" "$t" "$(detail_of "$t" svc_none)"; fail=1; fi
  done
  for brk in $(printf '%s\n' "${!SVC_TARGET_OF[@]}" | sort); do
    run "$BASE" "svc_$brk" "$SVC_RUN" "$SVC_ENV=$brk"
    report "svc_$brk" "${SVC_TARGET_OF[$brk]}"
  done
fi

# ---- coverage ---------------------------------------------------------------
printf '\n\033[1mCoverage\033[0m\n'
uncovered=0
for t in "${ALL[@]}"; do
  [[ -n ${COVERED[$t]:-} ]] && continue
  uncovered=$((uncovered+1))
  if ((PARTIAL)); then printf '   \033[33mnot yet covered\033[0m %s\n' "$t"
  else printf '   \033[31mUNPROVEN — no mutant turns it red:\033[0m %s\n' "$t"; fail=1; fi
done
((fail)) && { echo; die "mutation run FAILED — see above"; }
if ((PARTIAL)); then
  echo; info "partial run: nothing above failed; $uncovered test(s) not yet covered. NOT an all-clear."
else
  echo; ok "every test goes red on what is broken for it; every control passes on M0; every test covered"
fi
