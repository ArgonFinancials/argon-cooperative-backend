#!/usr/bin/env bash
# Apply migrations as argon_migrator (objects land owned by argon_owner), then
# re-apply grants.sql.
#
#   40-migrate.sh up             docker: runs. host: refuses without --confirm.
#   40-migrate.sh up --confirm   host: takes a backup, verifies it, then migrates.
#   40-migrate.sh down 1 --confirm
#   40-migrate.sh version
#
# On the host a migration is never the first write after an unverified backup.
source "$(dirname "$0")/lib.sh"

args=(); confirm=0
for a in "$@"; do [[ $a == --confirm ]] && confirm=1 || args+=("$a"); done
((${#args[@]})) || args=(up)

if [[ ${args[0]} == version ]]; then run_migrate "$ARGON_DB" version; exit; fi
# Going down drops tables and their rows. On a shared developer server that is
# refused for everything but a scratch database. (force only rewrites
# migrate's own version record, and is the recovery from "dirty".)
if [[ $TARGET == local && ${args[0]} =~ ^(down|drop|goto)$ ]]; then
  assert_scratch "$ARGON_DB"
fi

if [[ $TARGET == host ]]; then
  ((confirm)) || die "host target: re-run with --confirm. This will back up '$ARGON_DB' and then run: migrate ${args[*]}"
  current=$(run_migrate "$ARGON_DB" version 2>&1 || true)
  if [[ $current == *"no migration"* ]]; then
    info "empty database — no pre-migration backup needed"
  else
    info "pre-migration backup (schema at: $current)"
    bash "$DEPLOY_DIR/50-backup.sh" --label "pre-migrate"
  fi
fi

# What the schema is at BEFORE this run. If the run fails, this — not what
# migrate records afterwards — is the version to force back to.
before=$(run_migrate "$ARGON_DB" version 2>&1 || true)
info "migrate ${args[*]} on $ARGON_DB (schema now at: $before)"
if ! run_migrate "$ARGON_DB" "${args[@]}"; then
  v=$(run_migrate "$ARGON_DB" version 2>&1 || true)
  die "migration failed. migrate now records '$v' — that is where it was GOING, not where
  the schema IS. Every migration here is ONE transaction (BEGIN…COMMIT): the failed one
  changed nothing, so the schema is still at '$before'.
  (A failed up to N records N dirty; a refused down from N records N-1 dirty.)
  Read the error above, fix its cause, then:  $MIGRATE_BIN … force ${before%% *}
  Never force to the number migrate recorded. See deploy/README.md, \"migrate says dirty\"."
fi
apply_grants "$ARGON_DB"
ok "schema version: $(run_migrate "$ARGON_DB" version 2>&1)"
