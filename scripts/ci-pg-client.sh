#!/usr/bin/env bash
# Install the PostgreSQL 18 client on a CI runner, or FAIL FAST SAYING WHY.
#
#   CI_PG_DEB_CACHE=<dir> bash scripts/ci-pg-client.sh
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
#
# THE CACHE (2026-11-10, after four stalls in one day on the same mirror): the
# packages a network install downloads are kept in CI_PG_DEB_CACHE, which the
# workflow saves with actions/cache. When that directory comes back full, the
# client is installed from it with dpkg and NO MIRROR AT ALL; the network is
# only for an empty cache, or a cached set that no longer installs (the runner
# image moved on), and then the cache is refilled. Either way the result is
# read back: psql must answer as major 18, or it is not installed.
set -uo pipefail

as_root() { if [[ $(id -u) == 0 ]]; then "$@"; else sudo "$@"; fi; }
DEBS=${CI_PG_DEB_CACHE:-}
APT=(apt-get -y -q -o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20)
if [[ -n $DEBS ]]; then
  mkdir -p "$DEBS/partial"
  APT+=(-o "Dir::Cache::Archives=$DEBS" -o APT::Keep-Downloaded-Packages=true)
fi
ATTEMPT_SECONDS=${CI_APT_ATTEMPT_SECONDS:-240}
PSQL=/usr/lib/postgresql/18/bin/psql

installed() { [[ -x $PSQL ]] && "$PSQL" --version 2>/dev/null | grep -qE '\(PostgreSQL\) 18(\.|$)'; }
done_ok() {
  if [[ -n ${GITHUB_PATH:-} ]]; then echo /usr/lib/postgresql/18/bin >> "$GITHUB_PATH"; fi
  echo "the PostgreSQL 18 client is installed ($1): $("$PSQL" --version)"
  exit 0
}

# From the cache: no network. Every cached package at once, so dpkg orders
# their dependencies among themselves.
if [[ -n $DEBS ]] && compgen -G "$DEBS/*.deb" >/dev/null; then
  n=$(ls "$DEBS"/*.deb | wc -l)
  echo "installing the PostgreSQL 18 client from the cache ($n packages, no mirror)"
  if as_root timeout 120 dpkg -i "$DEBS"/*.deb && installed; then
    done_ok "from the cache"
  fi
  echo "the cached packages did not install; repairing dpkg and going to the mirror"
  as_root dpkg --configure -a >/dev/null 2>&1 || true
  as_root rm -f "$DEBS"/*.deb
fi

install() {
  as_root "${APT[@]}" update &&
    as_root "${APT[@]}" install postgresql-common &&
    as_root env APT_OPTS="-o Acquire::Retries=3 -o Acquire::http::Timeout=20" /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y &&
    as_root "${APT[@]}" install postgresql-client-18
}

for attempt in 1 2; do
  echo "installing the PostgreSQL 18 client: attempt $attempt of 2 (each cut off at ${ATTEMPT_SECONDS}s)"
  timeout "$ATTEMPT_SECONDS" bash -c "$(declare -f as_root install); APT=($(printf '%q ' "${APT[@]}")); install"
  rc=$?
  if ((rc == 0)) && installed; then
    if [[ -n $DEBS ]]; then
      # What the cache keeps: the downloaded packages, readable by the save
      # step (which runs as the runner user), and nothing of apt's own.
      as_root rm -rf "$DEBS/partial" "$DEBS/lock"
      as_root chown -R "$(id -u):$(id -g)" "$DEBS"
      # Only the version now installed of each package: two versions of one
      # package (Ubuntu's postgresql-common and PGDG's) given to dpkg -i
      # together could leave the older one installed.
      for f in "$DEBS"/*.deb; do
        [[ -e $f ]] || continue
        p=$(dpkg-deb -f "$f" Package); v=$(dpkg-deb -f "$f" Version)
        [[ $(dpkg-query -W -f='${Version}' "$p" 2>/dev/null) == "$v" ]] || rm -f "$f"
      done
      echo "kept $(ls "$DEBS"/*.deb 2>/dev/null | wc -l) packages for the cache"
    fi
    done_ok "from the mirror"
  fi
  if ((rc == 124)); then why="cut off after ${ATTEMPT_SECONDS}s"; elif ((rc == 0)); then why="apt finished but psql 18 does not answer"; else why="apt failed, exit $rc"; fi
  echo "attempt $attempt did not finish ($why); apt's own output is above"
  # A cut-off attempt leaves its apt-get running: timeout ends the bash
  # wrapper, not the sudo apt-get under it, which keeps the lock. On
  # 2026-11-10 the second attempt died in a second on that lock ("Could not
  # get lock ... held by process 3473 (apt-get)"), so the retry never retried.
  # A CI runner is thrown away after the job: end what is left, repair dpkg,
  # and only then try again.
  if ((attempt == 1)); then
    as_root pkill -9 -x apt-get 2>/dev/null; as_root pkill -9 -f apt.postgresql.org.sh 2>/dev/null; as_root pkill -9 -x dpkg 2>/dev/null
    sleep 2
    as_root dpkg --configure -a >/dev/null 2>&1 || true
    echo "cleared what attempt 1 left running (it held apt's lock)"
  fi
done

msg="CI SETUP FAILED, NOT A TEST: the package mirror did not answer while installing the PostgreSQL 18 client (apt-get stalled or timed out, twice, and there was no usable cache). No suite ran and nothing was tested. Re-run the job; if it persists, the runner's Ubuntu or PGDG mirror is down."
if [[ -n ${GITHUB_ACTIONS:-} ]]; then echo "::error title=CI setup, not a test::$msg"; fi
echo "$msg" >&2
exit 1
