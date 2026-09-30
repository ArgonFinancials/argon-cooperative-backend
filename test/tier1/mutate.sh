#!/usr/bin/env bash
# Mutation run for the 0002 acceptance tests.
#
# Each schema is 0001 + 0002_interface.sql (+ one mutant). Every mutant is a
# DELIBERATELY WRONG implementation; the table below states which tests it
# must turn RED. The run fails if:
#   - a targeted test PASSES against the broken schema  (the test tests nothing)
#   - a targeted test fails in its own FIXTURE          (red for the wrong reason)
#   - any control fails against M0                      (the control is broken)
#   - some test is targeted by no mutant                (unproven)
#
# What this cannot prove: that each test goes GREEN against a correct 0002.
# That half needs the migration, which is written after review.
source "$(dirname "$0")/../../deploy/lib.sh"
[[ $TARGET == docker ]] || die "run the mutation suite against local docker only"
require_env POSTGRES_PASSWORD ARGON_MIGRATOR_PASSWORD

export PYTHONIOENCODING=utf-8   # test messages contain −, ₦, §
# Paths handed to the test binary must be native: on Windows, C:/..., not /c/...
native() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else echo "$1"; fi; }
HERE="$(native "$REPO_DIR/test/tier1")"
BIN="$REPO_DIR/bin/tier1.test$( [[ $OSTYPE == msys* || $OSTYPE == cygwin* ]] && echo .exe )"
REPO_DIR="$(native "$REPO_DIR")"
export ARGON_TEST_ADMIN_URL="postgres://postgres:${POSTGRES_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
export ARGON_TEST_MIGRATOR_URL="postgres://argon_migrator:${ARGON_MIGRATOR_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
BASE="$REPO_DIR/migrations/0001_init.up.sql,$HERE/0002_interface.sql"

# mutant => tests it must turn red (regex alternation on test names)
declare -A TARGET_OF=(
  [M0]='TestG1_BalancedPair|TestG1_CallerCannot|TestG1_OlderTransaction|TestS1_|TestS2_|TestS3_|TestV4_|TestG4_Relinking|TestU1_OnePerson|TestU2_SuppliedUserMust|TestU3_|TestU4_|TestU5_|TestG3_AuditorCannot.*|TestG4_(AuditorThen|SignatoryThen|AuditorAndSignatory|Extending)|TestV1_|TestV2_|TestM1_|TestM3_|TestM4_|TestP1_|TestP3_.*|TestP4_PeriodsInOne|TestA2_|TestA3_|TestA4_|TestA5_'
  [g1_posted_at_server_only]='TestG1_OlderTransactionCannotAppendToNewerEntry'
  [s_any_appointment]='TestS1_NonSigningPostCannotSign'
  [s_ignores_term]='TestS2_'
  [s_trusts_signed_at]='TestS3_'
  [s_chairman_only]='TestS_Control_CurrentSignatorySigns'
  [v_batch_as_of_kept]='TestV4_BatchesCarryNoAsOfColumn'
  [g1_xmin_ignores_savepoints]='TestG1_Control_EntryCreatedInsideSavepoint'
  [g1_latest_entry_only]='TestG1_Control_BatchOfEntriesInOneTransaction'
  [u_keyed_on_member]='TestU1_OnePersonTwoMemberRecordsCannotSignTwice'
  [u_trusts_supplied_user]='TestU1_OnePerson|TestU2_SuppliedUserMust|TestU3_|TestU4_'
  [u_one_signature_per_request]='TestU1_Control_DifferentPeopleSignSameRequest'
  [u_one_signature_per_person_ever]='TestU1_Control_SamePersonSignsDifferentRequests'
  [u_rejects_supplied_user]='TestU2_Control_SuppliedMatchingUserIsAccepted'
  [u_synced_not_frozen]='TestU4_SignatureUserIsFrozenAtSigning'
  [g3_label_only]='TestG3_AuditorCannotSignUnderAnotherLabel|TestG3_AuditorCannotSignThroughSecondMemberRecord'
  [g3_member_level]='TestG3_AuditorCannotSignThroughSecondMemberRecord'
  [g3_ignores_term]='TestG3_Control_FormerAuditorMaySign'
  [g4_member_level]='TestG4_AuditorAndSignatoryAcrossTwoMemberRecords'
  [g4_signatory_insert_only]='TestG4_SignatoryThenAuditorSameMember|TestG4_ExtendingAuditorTermIntoSignatoryTerm'
  [g4_ignores_term]='TestG4_Control_ConsecutiveTerms'
  [g4_any_post]='TestG4_Control_AuditorMayHoldNonSigningPost'
  [v_filled_with_posting_date]='TestV2_UnknownValueDateStaysNull'
  [v_null_for_any_kind]='TestV3_OnlyOpeningBalanceMayHaveUnknownDate'
  [v_opening_balance_always_null]='TestV_Control_OpeningBalanceWithKnownAsOf'
  [m_entry_column_kept]='TestM1_EntriesCarryNoMemberColumn'
  [m_no_fk]='TestM3_LineMemberMustExist|TestM4_LineMemberMustBelongToEntrysSociety'
  [m_no_society_check]='TestM4_LineMemberMustBelongToEntrysSociety'
  [m_one_member_per_entry]='TestM2_MemberToMemberTransferInOneEntry'
  [m_member_required]='TestM_Control_LinesWithoutMember'
  [p_checks_posted_at]='TestP1_PostingIntoClosedPeriodIsRefused'
  [p_close_not_final]='TestP3_ClosedPeriodCannotBeReopened|TestP3_ClosedPeriodBoundariesCannotMove'
  [p_only_reopen_refused]='TestP3_ClosedPeriodBoundariesCannotMove'
  [p_ignores_closed]='TestP2_Control_PostingIntoOpenPeriod'
  [p_overlap_across_societies]='TestP4_Control_SameDatesInAnotherSociety'
  [a_fk_only]='TestA2_|TestA4_|TestA5_'
  [a_no_society_check]='TestA5_AdjustmentMustBeInSameSociety'
  [a_allows_both]='TestA4_CorrectionIsReversalOrAdjustmentNotBoth'
  [a_period_rule_on_original]='TestA1_AdjustmentOfClosedPeriodEntryPostsInOpenPeriod'
)

info "building test binary"
mkdir -p "$REPO_DIR/bin"
(cd "$REPO_DIR" && GOMAXPROCS=1 go test -p 1 -c -tags tier1 -o "$BIN" ./test/tier1/)
# From the source, not -test.list: listing still runs TestMain, which needs a DB.
mapfile -t ALL < <(grep -ohE '^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)' "$HERE"/*_test.go | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/')
# An empty list would make every check below pass vacuously.
((${#ALL[@]} > 0)) || die "found no tests in $HERE"
info "${#ALL[@]} tests, $(( ${#TARGET_OF[@]} - 1 )) mutants + M0"

out=$(mktemp -d); trap 'rm -rf "$out"' EXIT
# run SCHEMA LABEL -> writes $out/LABEL.status lines "Test STATUS detail"
run() {
  local schema=$1 label=$2
  ARGON_TEST_SCHEMA="$schema" "$BIN" -test.v -test.count=1 > "$out/$label.log" 2>&1 || true
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

report() {  # label
  local label=$1 pattern=${TARGET_OF[$1]}
  if grep -q '^__SETUP__' "$out/$label.status"; then
    printf '\n\033[31m%s: SCHEMA DID NOT APPLY\033[0m %s\n' "$label" "$(cut -d' ' -f3- "$out/$label.status")"; fail=1; return
  fi
  printf '\n\033[1m%s\033[0m  %s\n' "$label" "$(sed -n 's/^-- //p' "$HERE/mutants/$label.sql" 2>/dev/null | head -2 | tr '\n' ' ')"
  for t in "${ALL[@]}"; do
    [[ $t =~ ^($pattern) ]] || continue   # prefix match
    COVERED[$t]=1
    local st; st=$(status_of "$t" "$label")
    case $st in
      RED)    printf '   \033[32mred   ✓\033[0m %-58s %s\n' "$t" "$(detail_of "$t" "$label" | cut -c1-90)" ;;
      PASS)   printf '   \033[31mPASSED ON BROKEN SCHEMA ✗\033[0m %s\n' "$t"; fail=1 ;;
      BROKEN) printf '   \033[33mBROKEN FIXTURE ?\033[0m %s — %s\n' "$t" "$(detail_of "$t" "$label")"; fail=1 ;;
      *)      printf '   \033[31mNOT RUN ?\033[0m %s\n' "$t"; fail=1 ;;
    esac
  done
}

# ---- GREEN: every test must pass against the REAL migrations ----------------
REAL=$(ls "$REPO_DIR"/migrations/*.up.sql | sort | tr '\n' ',' | sed 's/,$//')
info "GREEN: the real migrations ($(ls "$REPO_DIR"/migrations/*.up.sql | wc -l) files)"
run "$REAL" GREEN
if grep -q '^__SETUP__' "$out/GREEN.status"; then fail=1; printf '   \033[31mSCHEMA DID NOT APPLY\033[0m\n'; fi
green_n=0
for t in "${ALL[@]}"; do
  st=$(status_of "$t" GREEN)
  if [[ $st == PASS ]]; then green_n=$((green_n+1))
  else printf '   \033[31m%s ✗\033[0m %s — %s\n' "${st:-NOT RUN}" "$t" "$(detail_of "$t" GREEN)"; fail=1; fi
done
printf '   %d/%d pass against the real schema\n' "$green_n" "${#ALL[@]}"

info "M0: 0001 + interface, no enforcement"
run "$BASE" M0
report M0
printf '\n\033[1mM0 controls\033[0m  (must PASS on a schema with no rules — proves each statement shape is valid)\n'
for t in "${ALL[@]}"; do
  [[ $t == *Control* || $t == TestM2_* || $t == TestA1_* ]] || continue
  st=$(status_of "$t" M0)
  if [[ $st == PASS ]]; then printf '   \033[32mpass  ✓\033[0m %s\n' "$t"
  else printf '   \033[31m%s ✗\033[0m %s — %s\n' "$st" "$t" "$(detail_of "$t" M0)"; fail=1; fi
done

for f in "$HERE"/mutants/*.sql; do
  label=$(basename "$f" .sql)
  [[ -n ${TARGET_OF[$label]:-} ]] || { printf '\n\033[31m%s has no expectation\033[0m\n' "$label"; fail=1; continue; }
  run "$BASE,$f" "$label"
  report "$label"
done

printf '\n\033[1mCoverage\033[0m\n'
for t in "${ALL[@]}"; do [[ -n ${COVERED[$t]:-} ]] || { printf '   \033[31mUNPROVEN — no mutant turns it red:\033[0m %s\n' "$t"; fail=1; }; done
((fail)) && { echo; die "mutation run FAILED — see above"; }
echo; ok "every test goes red on the schema broken for it; every control passes on M0; every test covered"
