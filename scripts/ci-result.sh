#!/usr/bin/env bash
# Wait for CI on a pushed commit and report its result (finding 51).
#
#   bash scripts/ci-result.sh COMMIT [--appear-within SECONDS] [--finish-within SECONDS]
#
# From 19 to 21 October four pushes to main went red and nobody was told for
# two days: push-main.sh proved each push LANDED and nothing read whether CI
# RAN. GitHub had refused to start any job (a billing hold); the jobs had no
# steps at all. So this passes ONLY on a successful run of the push, and
# otherwise says why, in GitHub's own words:
#   - no run appears for the commit at all                    -> FAILED
#   - the run does not finish in time                         -> FAILED
#   - the run finishes other than success                     -> FAILED, each
#     failed job named with its annotation (a refusal to start says so there)
#     and the last lines of its log
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

sha=$(git rev-parse "${1:?usage: ci-result.sh COMMIT}")
shift
appear=300 finish=4500
while (($#)); do case $1 in
  --appear-within) appear=$2; shift 2 ;;
  --finish-within) finish=$2; shift 2 ;;
  *) echo "unknown argument $1" >&2; exit 2 ;;
esac; done
repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)

# gh retried: the API's 502s and TLS timeouts are not the result.
ghr() { local i out; for i in 1 2 3 4 5; do out=$("$@" 2>/dev/null) && { printf '%s' "$out"; return 0; }; sleep 10; done; return 1; }

run="" waited=0
while [[ -z $run ]]; do
  run=$(ghr gh run list --commit "$sha" --event push --limit 1 --json databaseId --jq '.[0].databaseId // empty' || true)
  [[ -n $run ]] && break
  ((waited >= appear)) && { echo "FAILED: no CI run appeared for ${sha:0:7} within ${appear}s (the push may not have triggered the workflow, or the workflow file is invalid)" >&2; exit 1; }
  sleep 15; waited=$((waited + 15))
done
echo "CI run $run for ${sha:0:7}: waiting for it to finish"

waited=0
while :; do
  st=$(ghr gh run view "$run" --json status --jq .status || echo unknown)
  [[ $st == completed ]] && break
  ((waited >= finish)) && { echo "FAILED: CI run $run has not finished after ${finish}s (status: $st)" >&2; exit 1; }
  sleep 30; waited=$((waited + 30))
done

conclusion=$(ghr gh run view "$run" --json conclusion --jq .conclusion)
jobs=$(ghr gh api "repos/$repo/actions/runs/$run/jobs?per_page=100" --jq '.jobs[] | "\(.conclusion)\t\(.name)\t\(.id)\t\(.steps | length)"')
ok=$(awk -F'\t' '$1=="success"' <<<"$jobs" | wc -l)
skipped=$(awk -F'\t' '$1=="skipped"{print $2}' <<<"$jobs" | tr '\n' ' ')
if [[ $conclusion == success ]]; then
  echo "CI PASSED on ${sha:0:7}: $ok job(s) succeeded${skipped:+; skipped as designed: $skipped}"
  exit 0
fi
bad=$(awk -F'\t' '$1!="success" && $1!="skipped"' <<<"$jobs")
echo "CI FAILED on ${sha:0:7} (run $run: $conclusion). $ok job(s) succeeded, $(wc -l <<<"$bad") did not." >&2
# GitHub's own reason, grouped: one refusal shared by every job prints once.
while IFS=$'\t' read -r _ name id _; do
  msg=$(ghr gh api "repos/$repo/check-runs/$id/annotations" --jq '[.[].message] | join(" / ")' || echo "(annotations unreadable)")
  printf '%s\t%s\n' "${msg:-(no annotation)}" "$name"
done <<<"$bad" | sort | awk -F'\t' '{n[$1]++; j[$1] = j[$1] (j[$1] ? ", " : "") $2} END {for (m in n) printf "  %d job(s): %s\n     GitHub: %s\n", n[m], j[m], m}' >&2
# The log, only for a job that ran a step (one that never started has none),
# one attempt each: the reason is above; this is detail.
while IFS=$'\t' read -r _ name id steps; do
  ((steps > 0)) || continue
  echo "  == $name, the last lines of its log:" >&2
  { gh run view --job "$id" --log-failed 2>/dev/null || echo "(log unreadable)"; } | tail -8 | cut -c1-200 | sed 's/^/     /' >&2
done <<<"$bad"
exit 1
