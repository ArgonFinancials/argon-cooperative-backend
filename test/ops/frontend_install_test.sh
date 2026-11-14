#!/usr/bin/env bash
# Tests for deploy/36-install-frontend.sh (2026-11-14): the release's static
# export installed where nginx serves it, switched in one rename.
#   F01 installed, and current points at it
#   F02 a second release switches current; the first is kept
#   F03 three releases are kept, older ones removed, the current never
#   F04 an export with no index.html is refused, current unchanged
#   F05 a symlink inside the export is refused, current unchanged
#   F06 a release with no frontend changes nothing, and says so
#   F07 the frontend commit is recorded BESIDE the release, not inside what nginx serves
#   F08 a release name installed twice is refused, current unchanged
#
#   bash test/ops/frontend_install_test.sh
#   DEPLOY_SRC=/path/to/deploy-copy   runs against another copy (sabotage runs)
# Runs nothing as root and touches nothing outside a temporary directory.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
SRC="${DEPLOY_SRC:-$here/../../deploy}"
W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT
# Its own env file: the installer touches no database, and the one named here
# does not exist (finding 69: a test never falls back to a real one).
printf 'TARGET=local
ARGON_DB=argon_verify_none_frontend
BACKUP_STORE=file://%s/backups
' "$W" >"$W/test.env"
export ARGON_ENV="$W/test.env"
export ARGON_APP_WWW="$W/www"

# The installer's switch IS a symlink. Git Bash on Windows copies instead of
# linking (or refuses), which would fail every test for the platform's reason,
# not the script's. Refuse to run there, saying so: CI runs it on Linux.
ln -s x "$W/probe-link" 2>/dev/null
if [[ ! -L $W/probe-link ]]; then
  echo "NOT RUN: this platform cannot make symlinks, and the installer's switch is one. It runs in CI (Linux)."
  exit 2
fi
rm -f "$W/probe-link"

T=""; RED=()
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }

# repo NAME [what]: a release checkout carrying a frontend export (or not).
repo() {
  local r="$W/rel-$1"; rm -rf "$r"; mkdir -p "$r"; cp -r "$SRC" "$r/deploy"
  case ${2:-good} in
    none) ;;
    *) mkdir -p "$r/frontend/_next/static/chunks" "$r/frontend/admin/ledger"
       echo "<p>release $1</p>" > "$r/frontend/index.html"
       echo "<p>not found</p>" > "$r/frontend/404.html"
       echo "<p>entry</p>" > "$r/frontend/admin/ledger/entry.html"
       echo "x" > "$r/frontend/_next/static/chunks/a1b2.js"
       printf '%040d' 7 | tr 0 a > "$r/FRONTEND_COMMIT" ;;
  esac
  case ${2:-good} in
    noindex) rm "$r/frontend/index.html" ;;
    symlink) ln -s /etc/passwd "$r/frontend/passwd" ;;
  esac
  echo "$r"
}
install_rel() {  # install_rel NAME [what] -> runs the installer, output in $W/out
  local r; r=$(repo "$1" "${2:-good}")
  ARGON_RELEASE="$1" bash "$r/deploy/36-install-frontend.sh" >"$W/out" 2>&1
}
current() { readlink "$ARGON_APP_WWW/current" 2>/dev/null; }
serves() { cat "$ARGON_APP_WWW/current/index.html" 2>/dev/null; }

T="F01 an export is installed, and current points at it"
if ! install_rel 20261114t100000z-aaaa; then red "refused: $(tail -2 "$W/out")"
elif [[ $(current) != releases/20261114t100000z-aaaa || $(serves) != "<p>release 20261114t100000z-aaaa</p>" ]]; then red "current is '$(current)', serving '$(serves)'"
else pass; fi

T="F02 a second release switches current; the first is kept"
install_rel 20261114t110000z-bbbb || red "refused: $(tail -2 "$W/out")"
if [[ $(current) != releases/20261114t110000z-bbbb ]]; then red "current is '$(current)'"
elif [[ ! -f $ARGON_APP_WWW/releases/20261114t100000z-aaaa/index.html ]]; then red "the previous release is gone: no step back"
else pass; fi

T="F03 three releases are kept, the older removed, the current never"
install_rel 20261114t120000z-cccc; install_rel 20261114t130000z-dddd
kept=$(cd "$ARGON_APP_WWW/releases" && ls -1d */ | sed 's#/$##' | tr '\n' ' ')
if [[ $kept != "20261114t110000z-bbbb 20261114t120000z-cccc 20261114t130000z-dddd " ]]; then red "kept: $kept"
elif [[ $(current) != releases/20261114t130000z-dddd ]]; then red "current is '$(current)'"
else pass; fi

T="F04 an export with no index.html is refused; current unchanged"
if install_rel 20261114t140000z-eeee noindex; then red "installed"
elif [[ $(current) != releases/20261114t130000z-dddd ]]; then red "current moved to '$(current)'"
else pass; fi

T="F05 a symlink inside the export is refused; current unchanged"
if install_rel 20261114t150000z-ffff symlink; then red "installed an export with a symlink to /etc/passwd"
elif [[ $(current) != releases/20261114t130000z-dddd || -e $ARGON_APP_WWW/releases/20261114t150000z-ffff ]]; then red "current is '$(current)', or the release was left installed"
else pass; fi

T="F06 a release with no frontend changes nothing, and says so"
if ! install_rel 20261114t160000z-gggg none; then red "failed: $(tail -2 "$W/out")"
elif [[ $(current) != releases/20261114t130000z-dddd ]]; then red "current moved to '$(current)'"
elif ! grep -q "carries no frontend" "$W/out"; then red "it did not say the release carries no frontend"
else pass; fi

T="F07 the frontend commit is recorded beside the release, not where nginx serves"
c="$ARGON_APP_WWW/releases/20261114t130000z-dddd.commit"
if [[ $(cat "$c" 2>/dev/null) != $(printf '%040d' 7 | tr 0 a) ]]; then red "no commit beside the release ($c)"
elif find "$ARGON_APP_WWW/releases/20261114t130000z-dddd" -name '*COMMIT*' -o -name '*.commit' | grep -q .; then red "the commit is inside the served directory"
else pass; fi

T="F08 a release name installed twice is refused; current unchanged"
if install_rel 20261114t120000z-cccc; then red "installed twice"
elif [[ $(current) != releases/20261114t130000z-dddd ]]; then red "current moved to '$(current)'"
else pass; fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all 8 green"
