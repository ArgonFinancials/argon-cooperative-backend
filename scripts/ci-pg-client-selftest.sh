#!/usr/bin/env bash
# The PostgreSQL client install's own tests, and its sabotage set. CI only: it
# needs a throwaway Ubuntu runner with sudo, and it breaks that runner's apt.
#
#   CI_PG_DEB_CACHE=<filled dir> bash scripts/ci-pg-client-selftest.sh
#
# Run AFTER the cache is filled (a real install while the mirror answered) and
# psql 18 purged again. Points apt at a proxy that never answers (TEST-NET-1,
# RFC 5737), then checks, against scripts/ci-pg-client.sh:
#   C1  a filled cache installs with no mirror, and psql answers as 18, READ
#       BACK here, not taken from the script's word;
#   C2  a cache that does not install fails fast and says it is CI setup;
#   C3  with no cache, a stalled mirror fails fast, says so, and the retry
#       really retries (attempt 2 cut off by the clock, never apt's lock).
# Then the cache rules are broken one copy at a time, and each copy must fail
# the check that guards it. A check never seen failing is not a check.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
DEBS=${CI_PG_DEB_CACHE:?the filled cache directory}
compgen -G "$DEBS/*.deb" >/dev/null || { echo "no packages in $DEBS: fill the cache first"; exit 2; }
PSQL=/usr/lib/postgresql/18/bin/psql
WORK=$(mktemp -d)

printf 'Acquire::http::Proxy "http://192.0.2.1:3128";\nAcquire::https::Proxy "http://192.0.2.1:3128";\n' | sudo tee /etc/apt/apt.conf.d/99-never-answers >/dev/null
sudo rm -rf /var/lib/apt/lists/*

purge() { sudo dpkg --purge postgresql-client-18 >/dev/null 2>&1 || true; [[ ! -e $PSQL ]]; }
fail() { echo "  FAIL: $*"; return 1; }

c1() { # $1 = the install script
  purge || fail "psql 18 still there before C1" || return 1
  local d="$WORK/c1"; rm -rf "$d"; cp -r "$DEBS" "$d"
  CI_PG_DEB_CACHE="$d" CI_APT_ATTEMPT_SECONDS=45 bash "$1" > "$WORK/c1.log" 2>&1; local rc=$?
  tail -5 "$WORK/c1.log" | sed 's/^/    /'
  ((rc == 0)) || fail "a filled cache did not install with the mirror dead (exit $rc)" || return 1
  grep -q "installed (from the cache)" "$WORK/c1.log" || fail "it did not say it installed from the cache" || return 1
  ! grep -q "attempt 1 of 2" "$WORK/c1.log" || fail "it went to the mirror with a filled cache" || return 1
  local v; v=$("$PSQL" --version 2>/dev/null) || fail "no psql 18 after the cached install" || return 1
  [[ $v =~ \(PostgreSQL\)\ 18 ]] || fail "psql answered '$v', not 18" || return 1
  echo "  C1 ok: installed from the cache with no mirror: $v"
}

c2() {
  purge || fail "psql 18 still there before C2" || return 1
  local d="$WORK/c2"; rm -rf "$d"; cp -r "$DEBS" "$d"
  for f in "$d"/postgresql-client-18_*.deb; do : > "$f"; done
  local start=$(date +%s)
  CI_PG_DEB_CACHE="$d" CI_APT_ATTEMPT_SECONDS=45 bash "$1" > "$WORK/c2.log" 2>&1; local rc=$?
  local took=$(( $(date +%s) - start ))
  tail -5 "$WORK/c2.log" | sed 's/^/    /'
  ((rc == 1)) || fail "a broken cache with a dead mirror did not fail (exit $rc)" || return 1
  grep -q "the cached packages did not install" "$WORK/c2.log" || fail "it did not say the cache failed" || return 1
  grep -q "CI SETUP FAILED, NOT A TEST" "$WORK/c2.log" || fail "it did not say it was CI setup, not a test" || return 1
  ((took < 240)) || fail "it took ${took}s to fail" || return 1
  echo "  C2 ok: a broken cache failed in ${took}s, saying so"
}

c3() {
  purge || fail "psql 18 still there before C3" || return 1
  local start=$(date +%s)
  CI_APT_ATTEMPT_SECONDS=45 bash "$1" > "$WORK/c3.log" 2>&1; local rc=$?
  local took=$(( $(date +%s) - start ))
  tail -5 "$WORK/c3.log" | sed 's/^/    /'
  ((rc == 1)) || fail "the install did not fail against a mirror that never answers (exit $rc)" || return 1
  grep -q "CI SETUP FAILED, NOT A TEST" "$WORK/c3.log" || fail "it did not say it was CI setup, not a test" || return 1
  ((took < 240)) || fail "it took ${took}s to fail: the cut-off did not fire" || return 1
  # 2026-11-10: attempt 1's apt-get, left running by the cut-off, held the
  # lock and attempt 2 died on it in a second.
  ! grep -q "Could not get lock" "$WORK/c3.log" || fail "attempt 2 died on attempt 1's apt lock: the retry did not retry" || return 1
  grep -q "attempt 2 did not finish (cut off" "$WORK/c3.log" || fail "attempt 2 was not a real second attempt" || return 1
  echo "  C3 ok: no cache, a stalled mirror failed in ${took}s, saying so"
}

echo "== the real install script"
real=0
c1 scripts/ci-pg-client.sh || real=1
c2 scripts/ci-pg-client.sh || real=1
c3 scripts/ci-pg-client.sh || real=1
((real == 0)) || { echo "::error::the PostgreSQL client install failed its own tests (above)"; exit 1; }

# The sabotage set: each copy breaks one cache rule; the check named must fail.
sabotage() { # name, check, python replacement (old, new)
  local name=$1 check=$2 old=$3 new=$4 copy="$WORK/sab-$1.sh"
  python3 - "$old" "$new" scripts/ci-pg-client.sh "$copy" <<'PY' || { echo "::error::sabotage $1: its text is not in the script"; return 1; }
import sys
old, new, src, dst = sys.argv[1:]
t = open(src).read()
if t.count(old) != 1: sys.exit(1)
open(dst, "w").write(t.replace(old, new))
PY
  echo "== sabotage $name (must fail $check)"
  if "$check" "$copy"; then echo "::error::sabotage $name was NOT caught by $check"; return 1; fi
  echo "  caught"
}
caught=0
# The cache is never read: a filled cache goes to the dead mirror.
sabotage cache_ignored c1 'if [[ -n $DEBS ]] && compgen -G' 'if false && compgen -G' || caught=1
# It says "from the cache" without installing anything.
sabotage cache_claims c1 'if as_root timeout 120 dpkg -i "$DEBS"/*.deb && installed; then' 'if true; then' || caught=1
# A cache that did not install is reported as installed.
sabotage cache_unchecked c2 'if as_root timeout 120 dpkg -i "$DEBS"/*.deb && installed; then' 'if as_root timeout 120 dpkg -i "$DEBS"/*.deb; [[ -n $DEBS ]]; then' || caught=1
((caught == 0)) || { echo "::error::a sabotaged install script passed its check (above)"; exit 1; }
echo "every check passed on the real script, and caught its sabotage"
