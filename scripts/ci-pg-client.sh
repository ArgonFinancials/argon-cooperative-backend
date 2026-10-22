#!/usr/bin/env bash
# Install the PostgreSQL 18 client on a CI runner, or FAIL FAST SAYING WHY.
#
#   bash scripts/ci-pg-client.sh
#
# On 2026-10-22 three CI jobs hung for 40+ minutes in this step: apt-get stalled
# mid-download from the runner's Ubuntu mirror and waited forever, no suite had
# started, and nothing said so (it moved between suites because it was never
# about a suite). A failure with no path to report itself reports nothing. So:
#   - apt gives up on a stalled connection after 20 s and retries 3 times;
#   - each attempt at the whole install is cut off at 4 minutes, and tried twice;
#   - the step itself is cut off at 10 minutes in the workflow, as a backstop;
#   - and when it fails it SAYS it is CI setup, the package mirror, not a test,
#     with apt's own last words above it.
set -uo pipefail

as_root() { if [[ $(id -u) == 0 ]]; then "$@"; else sudo "$@"; fi; }
APT=(apt-get -y -q -o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20)
ATTEMPT_SECONDS=${CI_APT_ATTEMPT_SECONDS:-240}

install() {
  as_root "${APT[@]}" update &&
    as_root "${APT[@]}" install postgresql-common &&
    as_root env APT_OPTS="-o Acquire::Retries=3 -o Acquire::http::Timeout=20" /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y &&
    as_root "${APT[@]}" install postgresql-client-18
}

for attempt in 1 2; do
  echo "installing the PostgreSQL 18 client: attempt $attempt of 2 (each cut off at ${ATTEMPT_SECONDS}s)"
  timeout "$ATTEMPT_SECONDS" bash -c "$(declare -f as_root install); APT=(${APT[*]}); install"
  rc=$?
  if ((rc == 0)); then
    if [[ -n ${GITHUB_PATH:-} ]]; then echo /usr/lib/postgresql/18/bin >> "$GITHUB_PATH"; fi
    echo "the PostgreSQL 18 client is installed"
    exit 0
  fi
  if ((rc == 124)); then why="cut off after ${ATTEMPT_SECONDS}s"; else why="apt failed, exit $rc"; fi
  echo "attempt $attempt did not finish ($why); apt's own output is above"
done

msg="CI SETUP FAILED, NOT A TEST: the package mirror did not answer while installing the PostgreSQL 18 client (apt-get stalled or timed out, twice). No suite ran and nothing was tested. Re-run the job; if it persists, the runner's Ubuntu or PGDG mirror is down."
if [[ -n ${GITHUB_ACTIONS:-} ]]; then echo "::error title=CI setup, not a test::$msg"; fi
echo "$msg" >&2
exit 1
