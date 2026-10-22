#!/usr/bin/env bash
# ONE restore path, used by both the drill and a real recovery — so the drill
# exercises exactly the code a recovery would run.
#
#   60-restore.sh --drill [--key dumps/.../argon-X]
#       Restore into a scratch argon_drill_<ts>, verify it against the
#       manifest, run the Tier 1 probe and every acceptance suite (ledger,
#       auth, roles, statement API, postAll API, the step-up, sign-in) ON THE RESTORED COPY, drop it.
#       Exit 0 only if every check passes.
#
#   60-restore.sh --into argon --confirm [--key ...]
#       Real recovery. Drops and recreates the target database. Stop the API
#       first (sudo systemctl stop argon-api).
#
# Checks, in order: sha256 of the fetched dump; pg_restore with
# --exit-on-error in a single transaction; the manifest regenerated from the
# restored database must equal the stored one line for line (row counts per
# table, ledger sum, every trigger and constraint, schema version).
source "$(dirname "$0")/lib.sh"
require_env ARGON_MIGRATOR_PASSWORD

mode=""; key=""; into=""; confirm=0; keep=0
while (($#)); do case $1 in
  --drill)   mode=drill; shift ;;
  --into)    mode=real; into=$2; shift 2 ;;
  --key)     key=$2; shift 2 ;;
  --confirm) confirm=1; shift ;;
  --keep)    keep=1; shift ;;
  *) die "unknown arg $1" ;;
esac; done
[[ -n $mode ]] || die "use --drill or --into DB --confirm"
# Restoring drops the target, so the name is checked before anything else
# runs. On a shared developer server only scratch names may be dropped; on
# EC2, only the real database or a scratch one.
if [[ $mode == real ]]; then
  if on_server; then
    [[ $into == "$ARGON_DB" ]] || assert_scratch "$into"
  else
    assert_scratch "$into"
  fi
fi

key=${key:-$(store_latest_dump)}; key=${key%.dump}
[[ -n $key ]] || die "no dumps found in $BACKUP_STORE"
info "restoring $key"

work=$(mktemp -d)
cleanup() { rm -rf "$work"; }
trap cleanup EXIT

store_get "$key.dump"     "$work/dump"
store_get "$key.manifest" "$work/manifest.full"
store_get "$key.sha256"   "$work/sha256"
[[ "$(sha256sum "$work/dump" | awk '{print $1}')" == "$(cat "$work/sha256")" ]] || die "checksum mismatch — dump is corrupt"
ok "sha256 $(cut -c1-16 "$work/sha256")…"

if [[ $mode == drill ]]; then
  target="argon_drill_$(date -u +%Y%m%dt%H%M%S | tr 'T' 't')_$$"   # and the process (finding 38)
  create_scratch_db "$target"
  ((keep)) || trap 'drop_scratch_db "$target" || true; cleanup' EXIT
else
  ((confirm)) || die "real restore into '$into' destroys it. Re-run with --confirm."
  if [[ $TARGET == host ]] && systemctl is-active -q argon-api; then
    die "argon-api is running — sudo systemctl stop argon-api first"
  fi
  if [[ $TARGET == docker ]] && on_server && [[ -n $(dc ps -q api 2>/dev/null) ]]; then
    die "the api container is running — stop it first: docker compose --env-file $ARGON_ENV -f $COMPOSE_FILE stop api"
  fi
  target=$into
  info "dropping and recreating $target"
  pg_super -d postgres -v db="$target" <<'SQL'
SELECT format('DROP DATABASE IF EXISTS %I WITH (FORCE)', :'db') \gexec
SELECT format('CREATE DATABASE %I OWNER argon_owner ENCODING ''UTF8'' TEMPLATE template0', :'db') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO argon_app, argon_migrator, argon_backup, argon_auth', :'db') \gexec
SQL
fi

# Match what 20-roles.sh does to a fresh database.
pg_super -d "$target" <<'SQL'
ALTER SCHEMA public OWNER TO argon_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO argon_app, argon_backup, argon_auth;
SQL

info "pg_restore into $target"
t0=$(date +%s)
pg_tool argon_migrator ARGON_MIGRATOR_PASSWORD pg_restore -d "$target" \
  --no-owner --role=argon_owner --exit-on-error --single-transaction < "$work/dump"
restore_secs=$(( $(date +%s) - t0 ))
ok "pg_restore finished in ${restore_secs}s"

info "comparing manifest"
# Both sides without CR: a native Windows psql writes CR LF to a file.
grep -v '^#' "$work/manifest.full" | tr -d '\r' > "$work/expected"
printf '%s\n' "$MANIFEST_SQL" | pg_as argon_migrator "$target" -q -At -f - | tr -d '\r' > "$work/actual"
if ! diff -u "$work/expected" "$work/actual" > "$work/diff"; then
  cat "$work/diff" >&2
  die "restored database does NOT match the manifest"
fi
ok "manifest matches: $(grep -c '^rows\.' "$work/expected") tables, $(grep -c '^trigger=' "$work/expected") triggers, $(grep -c '^constraint=' "$work/expected") constraints, $(grep '^ledger_lines.sum_kobo' "$work/expected")"

if [[ $mode == drill ]]; then
  info "Tier 1 probe against the restored copy"
  bash "$DEPLOY_DIR/70-verify-tier1.sh" --db "$target"
  # The probe checks a subset of the ledger rules. A restore brings back the
  # constraints, not just the data, so EVERY acceptance suite runs against the
  # restored copy, each counted separately: a pass names what it covered.
  # (TestMain does not apply the schema to ARGON_TEST_DB: the tests run
  # against what the restore brought back.)
  covered=() skipped=()
  # The suites are their files (finding 52): every test/tier1/suite_*.sh
  # whose prerequisite exists in this release runs, selected as mutate.sh's
  # GREEN selects (SUITE_GREEN_RUN, else SUITE_RUN, else all; less SUITE_SKIP).
  # This replaced a list of 26 written by hand. A suite still tests-first (its
  # migration or handler not yet written) is SAID to be skipped, never absent.
  # And a suite runs only where the RESTORED COPY's schema has every migration
  # its base and prerequisite name: a backup taken before a migration ran does
  # not enforce that migration's rules, and the drill says so rather than
  # failing the suite for rules the copy never had (found 2026-10-22: a copy
  # at 15 failed suite 0016; the hand list had the same flaw).
  schema=$(pg_as argon_migrator "$target" -At -c "SELECT version FROM schema_migrations" </dev/null)
  [[ $schema =~ ^[0-9]+$ ]] || die "could not read the restored copy's schema version (got '$schema')"
  info "the restored copy is at schema $schema"
  # The release's own code (an API handler, the auth service) needs the
  # release's whole schema: a deploy migrates before that code runs, so code
  # against an older schema is a world production is never in.
  head=$(ls "$REPO_DIR"/migrations/*.up.sql | sed -nE 's#.*/0*([0-9]+)_[^/]*$#\1#p' | sort -n | tail -1)
  [[ $head =~ ^[0-9]+$ ]] || die "could not read the release's latest migration (got '$head')"
  for f in "$REPO_DIR"/test/tier1/suite_*.sh; do
    name=$(basename "$f" .sh); name=${name#suite_}
    sel=$(SUITE_RUN='' SUITE_SKIP='' SUITE_GREEN_RUN='' SUITE_GREEN_NEEDS='' SUITE_BASE=''
          # shellcheck disable=SC1090
          source "$f" >/dev/null 2>&1
          printf '%s\x1f%s\x1f%s\x1f%s' "${SUITE_GREEN_RUN:-${SUITE_RUN:-.}}" "$SUITE_SKIP" "$SUITE_GREEN_NEEDS" "$SUITE_BASE")
    # \x1f, never a tab: a tab is whitespace to `read`, so an empty field (most
    # suites have no skip) collapsed and the next field moved into its place.
    IFS=$'\x1f' read -r re skip needs base <<<"$sel"
    [[ -n $re ]] || die "suite $name: no run pattern could be read from $f"
    if [[ -n $needs && ! -f $REPO_DIR/$needs ]]; then
      info "suite $name: NOT RUN against the restored copy: tests first, $needs not yet written"
      skipped+=("$name: tests first, $needs not yet written")
      continue
    fi
    # The newest migration the suite needs: in its base, or its prerequisite.
    want=$(tr ',' '\n' <<<"$base,$needs" | sed -nE 's#^migrations/0*([0-9]+)_.*#\1#p' | sort -n | tail -1)
    if [[ -n $needs && $needs != migrations/* ]] && ((schema < head)); then
      info "suite $name: NOT RUN against the restored copy: it runs this release's code, which needs schema $head, and the copy is at schema $schema"
      skipped+=("$name: runs this release's code, which needs schema $head")
      continue
    fi
    if [[ -n $want ]] && ((want > schema)); then
      info "suite $name: NOT RUN against the restored copy: it needs migration $want, and the copy is at schema $schema"
      skipped+=("$name: needs migration $want")
      continue
    fi
    info "suite $name against the restored copy"
    run_tier1 "$target" "$re" "$skip" >&2 || die "suite $name FAILED against the restored copy"
    covered+=("$name $RUN_TIER1_PASSED")
  done
  ((${#covered[@]} > 0)) || die "no suite ran against the restored copy: the suite files were not found under $REPO_DIR/test/tier1"
  record="$work/drill-record"
  printf 'drill_at=%s\nsource=%s\nresult=PASS\nrestore_seconds=%s\n' \
    "$(date -u +%FT%TZ)" "$key" "$restore_secs" > "$record"
  for c in "${covered[@]}"; do n=${c% *}; printf 'tests.%s=%s\n' "${n//[ ()]/}" "${c##* }"; done >> "$record"
  store_put "$record" "drills/$(date -u +%Y/%m/%d)/drill-$(date -u +%Y%m%dT%H%M%SZ).ok"
  ok "RESTORE DRILL PASSED — $key (schema $schema) restores, matches its manifest, and passes the probe and the ${#covered[@]} acceptance suites that apply to it:"
  for c in "${covered[@]}"; do ok "    ${c% *}: ${c##* } tests"; done
  # Never "every suite" when some did not run: each is named, with why.
  if ((${#skipped[@]} > 0)); then
    info "NOT RUN against this copy (${#skipped[@]}), each with its reason:"
    for s in "${skipped[@]}"; do info "    $s"; done
  fi
  info "NOT covered by this drill: mutation strength of those tests (test/tier1/mutate.sh, local only);"
  info "the live database (never touched); point-in-time recovery (nightly dumps only: RPO up to 24 h)."
else
  ok "restored $key into $target. Start the API: sudo systemctl start argon-api"
fi
