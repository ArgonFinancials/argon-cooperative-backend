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
((fail == 0)) || exit 1
echo "suite list: all $(echo "$files" | wc -l) suites are in the CI matrix and in mutate.sh, and nothing else is"
