#!/usr/bin/env bash
# Tests for the two deploy defects of 2026-11-11 (the deploy of e05a166 died at
# the probe: role "argon_wallet" does not exist):
#   M*  40-migrate.sh on the server runs 20-roles.sh FIRST, before the backup
#       and the migration, and stops if it fails. A release that adds a role
#       must not reach its grants or its probe without it.
#   C*  create_scratch_db drops a database it made when the grants after the
#       CREATE fail (C1, the probe's failure exactly: a role that does not
#       exist), and drops NOTHING when the CREATE itself fails (C2: the easy
#       wrong fix, a drop armed before the create, would drop a kept database).
#
#   bash test/ops/deploy_roles_test.sh
#   DEPLOY_SRC=/path/to/deploy-copy   runs against another copy (sabotage runs)
#
# Scratch databases only (argon_verify_roles_<ts>_<pid>_*). 20-roles.sh and
# 50-backup.sh are replaced in a copy of deploy/ by recorders: what is tested
# is that 40 calls them, in order. The real 20-roles.sh runs in every CI job.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e

SRC="${DEPLOY_SRC:-$DEPLOY_DIR}"
W=$(mktemp -d)
base="argon_verify_roles_$(date -u +%Y%m%dt%H%M%S)_$$"
made=()
cleanup() {
  local d; for d in "${made[@]}"; do drop_scratch_db "$d" >/dev/null 2>&1 || true; done
  rm -rf "$W"
}
trap cleanup EXIT
exists() { [[ $(pg_super -d postgres -At -c "SELECT count(*) FROM pg_database WHERE datname = '$1'" </dev/null) == 1 ]]; }

T=""; RED=()
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }

# lib.sh from the copy under test, in a subshell (its die exits). The role list
# is the COPY's default unless ROLES says otherwise: the real lib.sh, sourced
# above, set it already, and a copy that dropped a role inherited the real list
# (wallet_not_granted stayed green, 2026-11-11).
mklib() { ( unset SCRATCH_ROLES; [[ -z ${ROLES:-} ]] || export SCRATCH_ROLES=$ROLES; source "$SRC/lib.sh"; "$@" ); }

# ---- C: create_scratch_db ----------------------------------------------------
T="C1 grants fail after the CREATE: the half-made database is dropped"
db="${base}_c1"; made+=("$db")
ROLES="argon_app, argon_nosuch_role" mklib create_scratch_db "$db" >"$W/c1" 2>&1; rc=$?
if ((rc == 0)); then red "it succeeded granting to a role that does not exist"
elif exists "$db"; then red "it failed, and left $db behind"
elif ! grep -q "dropping it" "$W/c1"; then red "it did not say it dropped the half-made database"
else pass; fi

T="C2 the CREATE itself fails: the database already there is kept"
db="${base}_c2"; made+=("$db")
mklib create_scratch_db "$db" >/dev/null 2>&1 || red "fixture: could not create $db"
mklib create_scratch_db "$db" >"$W/c2" 2>&1; rc=$?
if ((rc == 0)); then red "creating an existing database succeeded"
elif ! exists "$db"; then red "a failed CREATE dropped the database that was already there"
else pass; fi

T="C3 a normal creation: the database exists and argon_wallet may connect"
db="${base}_c3"; made+=("$db")
if ! mklib create_scratch_db "$db" >"$W/c3" 2>&1; then red "it failed: $(tail -2 "$W/c3")"
elif [[ $(pg_super -d postgres -At -c "SELECT has_database_privilege('argon_wallet', '$db', 'CONNECT')" </dev/null) != t ]]; then
  red "argon_wallet cannot connect to it"
else pass; fi

# ---- M: 40-migrate.sh on the server ------------------------------------------
# A copy of deploy/ with 20-roles.sh and 50-backup.sh recording their calls.
mkcopy() {  # mkcopy ROLES_EXIT
  rm -rf "$W/repo"; mkdir -p "$W/repo"; cp -r "$SRC" "$W/repo/deploy"
  printf '#!/usr/bin/env bash\necho roles >>"%s/calls"\nexit %s\n' "$W" "$1" >"$W/repo/deploy/20-roles.sh"
  printf '#!/usr/bin/env bash\necho backup >>"%s/calls"\n' "$W" >"$W/repo/deploy/50-backup.sh"
  : >"$W/calls"
}
# The server's env: this env file, ARGON_HOST=1, the database named explicitly.
envf() { { cat "$ARGON_ENV"; printf 'ARGON_HOST=1\nARGON_DB=%s\nMIGRATIONS_DIR=%s\n' "$1" "$MIGRATIONS_DIR"; } >"$W/env"; }
version() { run_migrate "$1" version 2>&1; }

T="M1 the server's migrate runs the roles first, then migrates"
db="${base}_m1"; made+=("$db")
mklib create_scratch_db "$db" >/dev/null 2>&1 || red "fixture: could not create $db"
mkcopy 0; envf "$db"
ARGON_ENV="$W/env" bash "$W/repo/deploy/40-migrate.sh" up --confirm >"$W/m1" 2>&1; rc=$?
calls=$(paste -sd, "$W/calls")
if ((rc != 0)); then red "it failed (exit $rc): $(tail -3 "$W/m1")"
elif [[ ${calls%%,*} != roles ]]; then red "20-roles.sh was not the first thing run (calls: '${calls:-none}')"
elif [[ $(version "$db") == *"no migration"* ]]; then red "nothing was migrated"
else pass; fi

T="M2 the roles fail: nothing backed up, nothing migrated, and it says so"
db="${base}_m2"; made+=("$db")
mklib create_scratch_db "$db" >/dev/null 2>&1 || red "fixture: could not create $db"
# Migrated once, so a backup WOULD be due: the order is visible.
run_migrate "$db" up 1 >/dev/null 2>&1 || red "fixture: could not migrate $db to 1"
mkcopy 1; envf "$db"
ARGON_ENV="$W/env" bash "$W/repo/deploy/40-migrate.sh" up --confirm >"$W/m2" 2>&1; rc=$?
calls=$(paste -sd, "$W/calls")
if ((rc == 0)); then red "it succeeded with the roles failing"
elif [[ $calls == *backup* ]]; then red "it backed up after the roles failed (calls: $calls)"
elif [[ $(version "$db") != 1 ]]; then red "it migrated after the roles failed (now at $(version "$db"))"
elif ! grep -q "20-roles.sh failed" "$W/m2"; then red "it did not name 20-roles.sh as what failed"
else pass; fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all 5 green"
