#!/usr/bin/env bash
# Mutation run for a Tier 1 acceptance suite.
#
#   mutate.sh [0002|…|0011|stepup|api|batch|stepup_api|signin_api|books|loans_api|approvals_api|money_api|me_api|requests_api|balance_api] [--only GLOB] [--partial] [--svc]
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
require_env POSTGRES_PASSWORD ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_AUTH_PASSWORD

SUITE=0002 ONLY='*' PARTIAL=0 SVC=0
while (($#)); do case $1 in
  0002|0003|0004|0005|0006|0007|0008|0009|0010|0011|0012|0013|0014|0015|0016|0017|stepup|api|batch|stepup_api|signin_api|books|loans_api|approvals_api|money_api|me_api|requests_api|balance_api|cors_api|spec_api) SUITE=$1; shift ;;
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
# The real server's roles (test/tier1/roles_test.go). Used only where the real
# grants are applied: GREEN, below. Exported, never argv: they carry passwords.
export ARGON_TEST_APP_URL="postgres://argon_app:${ARGON_APP_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
export ARGON_TEST_AUTH_URL="postgres://argon_auth:${ARGON_AUTH_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"

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
    # Red because ANOTHER rule refused is not red for this break: a neighbour
    # covered for the rule under test (seven times by 2026-10-18, each caught
    # only by reading the reasons; now a step). M0 has no rules to cover.
    local detail; detail=$(detail_of "$t" "$label")
    if [[ $st == RED && $label != M0 && $detail == "WRONG REFUSAL"* ]]; then
      printf '   \033[31mRED FOR ANOTHER RULE ✗\033[0m %s — %s\n' "$t" "$(cut -c1-160 <<<"$detail")"; fail=1; continue
    fi
    case $st in
      RED)    printf '   \033[32mred   ✓\033[0m %-62s %s\n' "$t" "$(cut -c1-88 <<<"$detail")" ;;
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
  # And as the roles production uses: grants.sql applied, the real server on
  # argon_app and argon_auth connections (findings 16 and 25, one layer up).
  run "$REAL" GREEN "${SUITE_GREEN_RUN:-$SUITE_RUN}" -u "$SVC_ENV" ARGON_TEST_GRANTS="$(native "$REPO_DIR/deploy/sql/grants.sql")"
  green_n=0
  # A setup that died (schema, grants, the role connections) runs no test:
  # say why, not "NOT RUN" once per test.
  if grep -q '^__SETUP__' "$out/GREEN.status"; then
    printf '   \033[31mGREEN SETUP FAILED\033[0m %s\n' "$(cut -d' ' -f3- "$out/GREEN.status" | sed -E 's#postgres://[^ ]*#<url>#g')"; fail=1
  fi
  for t in "${ALL[@]}"; do
    st=$(status_of "$t" GREEN)
    if [[ $st == PASS ]]; then green_n=$((green_n+1))
    else printf '   \033[31m%s ✗\033[0m %s — %s\n' "${st:-NOT RUN}" "$t" "$(detail_of "$t" GREEN)"; fail=1; fi
  done
  printf '   %d/%d pass against the real schema\n' "$green_n" "${#ALL[@]}"
  # A STEP, not a habit (findings 16 and 25, the same failure twice): the suites
  # run as the owner, the API as argon_app, and a correct rule the app cannot
  # execute passes every suite and dies in production. A migration is not done
  # until the probe, which seeds and runs as argon_app, passes on it.
  if [[ $SUITE_GREEN_NEEDS == migrations/* ]]; then
    info "GREEN as the app: the Tier 1 probe (deploy/70-verify-tier1.sh), as argon_app"
    if bash "$REPO_DIR/deploy/70-verify-tier1.sh" > "$out/probe.log" 2>&1; then
      printf '   \033[32mprobe passes as argon_app ✓\033[0m\n'
    else
      printf '   \033[31mPROBE FAILED as argon_app ✗\033[0m — the rule may be right and the app unable to live with it:\n'
      # `|| true`: under lib.sh's errexit and pipefail a grep that matches
      # nothing ENDED THIS SCRIPT here, before the last lines below, and the
      # trap deleted the probe's log: a failure with no reason (finding 47).
      { sed 's/\x1b\[[0-9;]*m//g' "$out/probe.log" | grep -E 'ERROR|✗ [^G]|FAILED|FATAL' | grep -v '✗ GAP' | head -15 | sed 's/^/     /'; } || true
      # Always the last lines too: a probe that dies before its report (a
      # missing tool, a fixture) matches none of the above, and a failure
      # with no reason shown is the dangerous kind.
      echo '     … last lines of the probe:'; sed 's/\x1b\[[0-9;]*m//g' "$out/probe.log" | tail -5 | sed 's/^/     /'
      fail=1
    fi
  fi
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
  # A suite whose SQL mutants are run against the REAL service (SUITE_MUTANTS_REAL=1:
  # a schema the service has not caught up with) runs them as GREEN does: the
  # fake's variable unset, the production roles' connections and grants.
  if [[ ${SUITE_MUTANTS_REAL:-0} == 1 ]]; then
    run "$BASE,$f" "$label" "$SUITE_RUN" -u "$SVC_ENV" ARGON_TEST_GRANTS="$(native "$REPO_DIR/deploy/sql/grants.sql")"
  else
    run "$BASE,$f" "$label" "$SUITE_RUN"
  fi
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
