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

info "migrate ${args[*]} on $ARGON_DB"
if ! run_migrate "$ARGON_DB" "${args[@]}"; then
  v=$(run_migrate "$ARGON_DB" version 2>&1 || true)
  die "migration failed; migrate now records version '$v'.
  Every migration here is ONE transaction (BEGIN…COMMIT), so a failure leaves the schema
  at the previous version — but migrate marks it dirty and refuses further runs.
  Read the error above, fix its cause, then:  $MIGRATE_BIN … force <previous version>
  Do not force forward past a failed migration."
fi
apply_grants "$ARGON_DB"
ok "schema version: $(run_migrate "$ARGON_DB" version 2>&1)"
