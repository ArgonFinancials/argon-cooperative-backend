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
  target="argon_drill_$(date -u +%Y%m%dt%H%M%S | tr 'T' 't')"
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
  covered=()
  suites=(
    "ledger (0002)|.|^(TestAuth|TestRole|TestApi|TestProof|TestStepUp|TestLineSociety|TestLoan)"
    "auth (0003)|^TestAuth|"
    "roles (0004)|^TestRole|"
    "statement API|^TestApiStatement|"
    "postAll API|^TestApiBatch|"
    "step-up (0005)|^TestProof|"
    "step-up service|^TestStepUpS|"
    "step-up API|^TestApiStepUp|"
    "sign-in API|^TestApiSignIn|"
    "line society (0006)|^TestLineSociety|"
    "books API|^TestApiBooks|"
  )
  for suite in "${suites[@]}"; do
    IFS='|' read -r name re skip <<<"$suite"
    info "$name suite against the restored copy"
    run_tier1 "$target" "$re" "$skip" >&2 || die "the $name suite FAILED against the restored copy"
    covered+=("$name $RUN_TIER1_PASSED")
  done
  record="$work/drill-record"
  printf 'drill_at=%s\nsource=%s\nresult=PASS\nrestore_seconds=%s\n' \
    "$(date -u +%FT%TZ)" "$key" "$restore_secs" > "$record"
  for c in "${covered[@]}"; do n=${c% *}; printf 'tests.%s=%s\n' "${n//[ ()]/}" "${c##* }"; done >> "$record"
  store_put "$record" "drills/$(date -u +%Y/%m/%d)/drill-$(date -u +%Y%m%dT%H%M%SZ).ok"
  ok "RESTORE DRILL PASSED — $key restores, matches its manifest, and passes the probe and every acceptance suite:"
  for c in "${covered[@]}"; do ok "    ${c% *}: ${c##* } tests"; done
  info "NOT covered by this drill: mutation strength of those tests (test/tier1/mutate.sh, local only);"
  info "the live database (never touched); point-in-time recovery (nightly dumps only: RPO up to 24 h)."
else
  ok "restored $key into $target. Start the API: sudo systemctl start argon-api"
fi
