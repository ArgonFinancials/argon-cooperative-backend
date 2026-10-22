#!/usr/bin/env bash
# Every suite runs everywhere it must, or this fails (finding 52).
#
#   bash scripts/check-suite-list.sh
#
# The suites ARE their files: test/tier1/suite_<name>.sh. Two lists are
# written by hand and drift: the CI matrix in .github/workflows/deploy.yml,
# and the suite names mutate.sh accepts. balance_api reached main and CI never
# ran it, while the run reported 26 green and nobody asked which 26. A list
# someone maintains always drifts; this compares both lists with the files,
# both ways (a suite never run; a name with no suite behind it).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

files=$(ls test/tier1/suite_*.sh | sed -E 's#.*/suite_(.*)\.sh#\1#' | sort)
matrix=$(grep -E '^\s*suite: \[' .github/workflows/deploy.yml | head -1 | sed -E 's/.*\[(.*)\].*/\1/' | tr ',' '\n' | tr -d ' "' | grep -v '^$' | sort)
mutate=$(grep -E '^\s*0002\|' test/tier1/mutate.sh | head -1 | sed -E 's/^\s*//; s/\).*//' | tr '|' '\n' | grep -v '^$' | sort)

[[ -n $matrix ]] || { echo "FAILED: found no suite matrix in .github/workflows/deploy.yml (has its shape changed?)" >&2; exit 1; }
[[ -n $mutate ]] || { echo "FAILED: found no suite list in test/tier1/mutate.sh (has its shape changed?)" >&2; exit 1; }

fail=0
report() {  # report LIST_NAME LIST
  local name=$1 list=$2 missing extra
  missing=$(comm -23 <(echo "$files") <(echo "$list") | tr '\n' ' ')
  extra=$(comm -13 <(echo "$files") <(echo "$list") | tr '\n' ' ')
  if [[ -n $missing ]]; then echo "FAILED: suites $name never runs: $missing" >&2; fail=1; fi
  if [[ -n $extra ]]; then echo "FAILED: $name names suites with no suite file: $extra" >&2; fail=1; fi
}
report "the CI matrix" "$matrix"
report "mutate.sh" "$mutate"

# And every test belongs to EXACTLY ONE suite, chosen as mutate.sh chooses
# (SUITE_INCLUDE, less SUITE_EXCLUDE). Suite 0002 claims every test no other
# suite's prefix is excluded from it, by a list written by hand: 0017's tests
# went to main claimed by both 0002 and 0017, and 0002's CI run failed them as
# unproven (2026-10-22). Claimed by none: never run. By two: run where no
# mutant can prove them.
tests=$(grep -ohE '^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)' test/tier1/*_test.go | sed -E 's/^func (Test[A-Za-z0-9_]+).*/\1/' | sort -u)
claims=$(for f in test/tier1/suite_*.sh; do
  n=$(basename "$f" .sh); n=${n#suite_}
  ( SUITE_INCLUDE='^$' SUITE_EXCLUDE='^$'; REPO_DIR=$PWD; die() { :; }
    # shellcheck disable=SC1090
    source "$f" >/dev/null 2>&1 || true
    echo "$tests" | grep -E "$SUITE_INCLUDE" | grep -vE "$SUITE_EXCLUDE" | sed "s/^/$n /" ) || true
done)
none=$(comm -23 <(echo "$tests") <(awk '{print $2}' <<<"$claims" | sort -u) | tr '\n' ' ')
[[ -z ${none// /} ]] || { echo "FAILED: tests no suite runs: $none" >&2; fail=1; }
twice=$(awk '{print $2}' <<<"$claims" | sort | uniq -d)
if [[ -n $twice ]]; then
  while read -r t; do
    echo "FAILED: $t is claimed by more than one suite: $(grep " $t$" <<<"$claims" | awk '{print $1}' | tr '\n' ' ')" >&2
  done <<<"$twice"
  fail=1
fi
((fail == 0)) || exit 1
echo "suite list: all $(echo "$files" | wc -l) suites are in the CI matrix and in mutate.sh, and nothing else is; all $(echo "$tests" | wc -l) tests belong to exactly one suite"
