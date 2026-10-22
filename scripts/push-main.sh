#!/usr/bin/env bash
# Push HEAD to main, and prove that what landed is what was meant (finding 42).
#
#   bash scripts/push-main.sh
#
# On 2026-10-18 a `git add` failed inside a chain, the commit never happened,
# and the push that followed still ran and reported success: main carried a
# stub for a minute, and nothing noticed. An action that partly failed and
# reported success. So, before pushing:
#   - the working tree must be CLEAN: no staged, unstaged or untracked change.
#     Anything there is work that was meant to be in a commit and is not.
#   - the commits about to go are listed, so the push is a decision about them.
#   - every suite must be in the CI matrix and in mutate.sh (finding 52).
# And after:
#   - the remote's main must BE the local HEAD, read back from the remote,
#     not taken from push's own report.
#   - CI must PASS on it (finding 51): a push that landed is not a push that
#     was verified. From 19 to 21 October four pushes went red for two days
#     and nothing read it. This waits for the run and fails with GitHub's own
#     reason; it takes as long as CI does (minutes), by design.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

bash scripts/check-suite-list.sh || { echo "REFUSED: a suite would not run in CI or in mutate.sh (above)" >&2; exit 1; }

dirty=$(git status --porcelain)
if [[ -n $dirty ]]; then
  echo "REFUSED: the working tree is not clean; this would push a HEAD that is not the work:" >&2
  echo "$dirty" | head -20 | sed 's/^/  /' >&2
  exit 1
fi
git fetch -q origin main
ahead=$(git rev-list --count origin/main..HEAD)
behind=$(git rev-list --count HEAD..origin/main)
((behind == 0)) || { echo "REFUSED: main has $behind commit(s) this branch does not; fetch and rebase first" >&2; exit 1; }
((ahead > 0)) || { echo "nothing to push: main is already at $(git rev-parse --short HEAD)"; exit 0; }
echo "pushing $ahead commit(s) to main:"
git log --oneline origin/main..HEAD | sed 's/^/  /'
intended=$(git rev-parse HEAD)
git push -q origin HEAD:main
landed=$(git ls-remote origin refs/heads/main | cut -f1)
if [[ $landed != "$intended" ]]; then
  echo "MISMATCH: intended $intended, the remote's main is $landed" >&2
  exit 1
fi
echo "main is now ${landed:0:7}, read back from the remote: what was intended"
if ! bash scripts/ci-result.sh "$intended"; then
  echo "PUSHED BUT NOT VERIFIED: main is ${landed:0:7} and CI did not pass on it (above). Say so to the reviewer now." >&2
  exit 1
fi
