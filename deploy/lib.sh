#!/usr/bin/env bash
# Shared by every deploy script. The ONLY thing that differs between targets
# is the env file named by ARGON_ENV — never the scripts. See deploy/README.md.
#
# TARGET says where Postgres is and how its superuser is reached:
#   docker — the compose container; superuser via `docker compose exec`.
#   local  — a server already running on THIS machine (a developer's native
#            install); superuser over TCP to 127.0.0.1:$PGPORT with
#            POSTGRES_PASSWORD. Never the EC2 host.
#   host   — the EC2 instance; superuser via `sudo -u postgres` (peer auth).
#
# Two ways of reaching Postgres, and only two:
#   pg_super  — the cluster superuser, used for roles and databases only.
#   pg_as     — a named argon_* role over TCP to 127.0.0.1, password from the
#               environment (never argv, so it never shows in `ps`).
# Client tools (psql, pg_dump, pg_restore) must be the server's major version:
# inside the container (docker), the server's own install (local), PGDG $PG_MAJOR (host).

set -Eeuo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$DEPLOY_DIR/.." && pwd)"
ARGON_ENV="${ARGON_ENV:-$DEPLOY_DIR/env/local.env}"

die()  { printf '\n\033[31mFATAL:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[36m==>\033[0m %s\n' "$*" >&2; }
ok()   { printf '\033[32m ok\033[0m %s\n' "$*" >&2; }

[[ -f "$ARGON_ENV" ]] || die "env file $ARGON_ENV not found — copy one of deploy/env/*.env.example"
# shellcheck disable=SC1090
set -a; source "$ARGON_ENV"; set +a

: "${TARGET:?TARGET must be docker, local or host}"
: "${ARGON_DB:=argon}"
: "${PGPORT:=5432}"
: "${MIGRATIONS_DIR:=$REPO_DIR/migrations}"
: "${MIGRATE_BIN:=migrate}"
: "${BACKUP_STORE:?BACKUP_STORE must be s3://bucket/prefix or file:///path}"
: "${BACKUP_SSE:=AES256}"
: "${COMPOSE_FILE:=$REPO_DIR/docker-compose.yml}"
case "$TARGET" in docker|local|host) ;; *) die "TARGET=$TARGET (want docker|local|host)";; esac
: "${POSTGRES_SUPERUSER:=postgres}"
# The PostgreSQL major version every target runs: local, docker and EC2 alike.
: "${PG_MAJOR:=18}"
# ARGON_HOST=1 marks THE SERVER (the EC2 instance), independent of TARGET,
# which says only where Postgres runs: on EC2 it runs in a container
# (TARGET=docker), so "is this the server" can no longer be read from TARGET.
: "${ARGON_HOST:=0}"
on_server() { [[ $TARGET == host || $ARGON_HOST == 1 ]]; }
# Go for the drill's test binary when scripts run under sudo, whose PATH lacks it.
[[ -x /usr/local/go/bin/go ]] && PATH="/usr/local/go/bin:$PATH"
# Executable suffix for built binaries. Never `x=$( [[ … ]] && echo .exe )`:
# where the test is false (Linux) the assignment's status is 1 and set -e
# exits on it silently.
EXE=""; if [[ $OSTYPE == msys* || $OSTYPE == cygwin* ]]; then EXE=.exe; fi
# A relative file:// store is relative to the repo, not the caller's cwd.
if [[ $BACKUP_STORE == file://* && ${BACKUP_STORE#file://} != /* ]]; then
  BACKUP_STORE="file://$REPO_DIR/${BACKUP_STORE#file://./}"
fi

require_env() { local v; for v in "$@"; do [[ -n "${!v:-}" ]] || die "$v is empty in $ARGON_ENV"; done; }

dc() { docker compose --env-file "$ARGON_ENV" -f "$COMPOSE_FILE" "$@"; }

# Superuser access. Reads SQL on stdin.
pg_super() {
  case $TARGET in
    docker) dc exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U postgres "$@" ;;
    local)  require_env POSTGRES_PASSWORD
            PGPASSWORD="$POSTGRES_PASSWORD" psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$PGPORT" -U "$POSTGRES_SUPERUSER" "$@" ;;
    host)   sudo -u postgres psql -X -q -v ON_ERROR_STOP=1 "$@" ;;
  esac
}

# urlenc: a value made safe inside a postgres:// URL (a developer's own
# superuser password may contain anything).
urlenc() { python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1" 2>/dev/null ||
           python -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$1"; }

# The superuser's URL for the Go test binary (docker and local only).
admin_url() {
  require_env POSTGRES_PASSWORD
  local user=postgres; [[ $TARGET == local ]] && user=$POSTGRES_SUPERUSER
  printf 'postgres://%s:%s@127.0.0.1:%s/postgres?sslmode=disable' "$(urlenc "$user")" "$(urlenc "$POSTGRES_PASSWORD")" "$PGPORT"
}

# Run a Postgres client tool as a role. Usage: pg_tool ROLE PASSWORD_VAR tool args...
pg_tool() {
  local role=$1 pwvar=$2 tool=$3; shift 3
  require_env "$pwvar"
  if [[ $TARGET == docker ]]; then
    PGPASSWORD="${!pwvar}" dc exec -T -e PGPASSWORD postgres \
      "$tool" -h 127.0.0.1 -p 5432 -U "$role" "$@"
  else
    PGPASSWORD="${!pwvar}" "$tool" -h 127.0.0.1 -p "$PGPORT" -U "$role" "$@"
  fi
}

# psql as a role. Usage: pg_as ROLE DB [psql args...]
pg_as() {
  local role=$1 db=$2; shift 2
  local pwvar
  case "$role" in
    argon_app)      pwvar=ARGON_APP_PASSWORD ;;
    argon_migrator) pwvar=ARGON_MIGRATOR_PASSWORD ;;
    argon_backup)   pwvar=ARGON_BACKUP_PASSWORD ;;
    argon_auth)     pwvar=ARGON_AUTH_PASSWORD ;;     # the auth service's role (0005)
    *) die "pg_as: unknown role $role" ;;
  esac
  pg_tool "$role" "$pwvar" psql -X -v ON_ERROR_STOP=1 -d "$db" "$@"
}

# THE GUARD. On a shared server (TARGET=local) the scratch-name rule is the
# only thing between a mistake and someone else's databases, so every
# destructive operation calls this BEFORE touching anything: dropping,
# restoring over, migrating down, or writing probe/test fixtures (rows that
# can never be deleted). It refuses every other name, argon_test and the real
# argon included. The pattern is anchored at both ends and admits no quotes,
# dots, spaces or capitals.
SCRATCH_RE='^argon_(drill|verify)_[0-9a-z_]+$'
assert_scratch() {
  [[ ${1:-} =~ $SCRATCH_RE ]] ||
    die "refusing '${1:-}': destructive operations run only on scratch databases (argon_verify_*, argon_drill_*)"
}

# Scratch databases (restore drills, constraint probes). Owned by argon_owner,
# connectable only by the argon roles.
create_scratch_db() {
  local db=$1
  assert_scratch "$db"
  pg_super -d postgres <<SQL
CREATE DATABASE "$db" OWNER argon_owner ENCODING 'UTF8' TEMPLATE template0;
REVOKE ALL ON DATABASE "$db" FROM PUBLIC;
GRANT CONNECT ON DATABASE "$db" TO argon_app, argon_migrator, argon_backup, argon_auth;
SQL
}
drop_scratch_db() {
  local db=$1
  assert_scratch "$db"
  pg_super -d postgres -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" </dev/null
}

# Run golang-migrate against a database as argon_migrator. The password goes
# through PGPASSWORD (lib/pq honours it), not into the URL, so it stays out of argv.
run_migrate() {
  local db=$1; shift
  require_env ARGON_MIGRATOR_PASSWORD
  command -v "$MIGRATE_BIN" >/dev/null || die "$MIGRATE_BIN not found (host: run 15-install-tools.sh; local: see deploy/README.md)"
  PGPASSWORD="$ARGON_MIGRATOR_PASSWORD" "$MIGRATE_BIN" -path "$MIGRATIONS_DIR" \
    -database "postgres://argon_migrator@127.0.0.1:${PGPORT}/${db}?sslmode=disable" "$@"
}

apply_grants() {
  local db=$1
  pg_as argon_migrator "$db" -q -f - < "$DEPLOY_DIR/sql/grants.sql"
}

# ---- backup store: s3:// or file:// behind one interface -------------------
store_put() {  # local_file key
  case "$BACKUP_STORE" in
    s3://*)   aws s3 cp --only-show-errors --sse "$BACKUP_SSE" "$1" "$BACKUP_STORE/$2" ;;
    file://*) local d="${BACKUP_STORE#file://}"; mkdir -p "$(dirname "$d/$2")"; cp "$1" "$d/$2" ;;
    *) die "BACKUP_STORE must be s3:// or file://" ;;
  esac
}
store_get() {  # key local_file
  case "$BACKUP_STORE" in
    s3://*)   aws s3 cp --only-show-errors "$BACKUP_STORE/$1" "$2" ;;
    file://*) cp "${BACKUP_STORE#file://}/$1" "$2" ;;
  esac
}
store_latest_dump() {  # prints key of newest *.dump
  case "$BACKUP_STORE" in
    s3://*)
      local bucket="${BACKUP_STORE#s3://}"; bucket="${bucket%%/*}"
      local prefix="${BACKUP_STORE#s3://$bucket}"; prefix="${prefix#/}"
      aws s3api list-objects-v2 --bucket "$bucket" --prefix "${prefix:+$prefix/}dumps/" \
        --query 'Contents[].Key' --output text | tr '\t' '\n' | grep '\.dump$' | sort | tail -1 \
        | sed "s#^${prefix:+$prefix/}##" ;;
    file://*)
      local d="${BACKUP_STORE#file://}"
      (cd "$d" && find dumps -name '*.dump' 2>/dev/null | sort | tail -1) ;;
  esac
}

# Deterministic manifest of a database's state. Compared byte-for-byte after a
# restore, so every line must be stable: counts, the ledger sum, the Tier 1
# machinery (triggers, constraints) and the migration version.
MANIFEST_SQL="$(cat <<'SQL'
\pset tuples_only on
\pset format unaligned
SELECT 'schema_version=' || coalesce((SELECT version || CASE WHEN dirty THEN '-DIRTY' ELSE '' END FROM schema_migrations), 'none');
SELECT 'rows.' || c.relname || '=' ||
       (xpath('//n/text()', query_to_xml(format('SELECT count(*) AS n FROM %I', c.relname), false, false, '')))[1]::text
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY c.relname;
SELECT 'ledger_lines.sum_kobo=' || coalesce(sum(amount_kobo), 0) FROM ledger_lines;
SELECT 'trigger=' || c.relname || '.' || t.tgname || (CASE t.tgenabled WHEN 'O' THEN '' ELSE ':DISABLED' END)
  FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE NOT t.tgisinternal ORDER BY 1;
SELECT 'constraint=' || conrelid::regclass || '.' || conname || ':' || contype::text
  FROM pg_constraint WHERE connamespace = 'public'::regnamespace ORDER BY 1;
SELECT 'index=' || indexrelid::regclass FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid
 WHERE c.relnamespace = 'public'::regnamespace ORDER BY 1;
SQL
)"

# Run Tier 1 acceptance tests against an EXISTING scratch database (the restore
# drill's restored copy). Usage: run_tier1 DB REGEX. The test binary refuses
# any database name that is not a scratch one.
#   docker: built from this checkout; admin is the container superuser over TCP.
#   host:   /opt/argon/bin/tier1.test (30-install-api.sh); admin is the postgres
#           OS user over the local socket (peer auth), so it runs as postgres.
# run_tier1 DB RUN-REGEX [SKIP-REGEX]
run_tier1() {
  local db=$1 re=$2 skip=${3:-} bin rc=0 log
  assert_scratch "$db"   # the suites write fixtures that can never be deleted
  # Each run in one database gets its own fixture-counter range (10^6 apart:
  # the phone fixtures have 8 digits, so 99 ranges fit), so suites sharing a
  # restored copy never re-create each other's member numbers, phones or
  # device tokens. The test binary FAILS a run that overflows its range
  # (seqRange in tier1_0002_test.go). 10^7 apart allowed only 9 suites.
  RUN_TIER1_N=$(( ${RUN_TIER1_N:-0} + 1 ))
  ((RUN_TIER1_N <= 99)) || die "run_tier1: more than 99 suites against one database would overlap fixture ranges"
  export ARGON_TEST_SEQ_BASE=$(( RUN_TIER1_N * 1000000 ))
  local args=(-test.run "$re" -test.count=1 -test.v)
  [[ -n $skip ]] && args+=(-test.skip "$skip")
  log=$(mktemp)
  require_env ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_AUTH_PASSWORD
  local mig="postgres://argon_migrator:${ARGON_MIGRATOR_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
  # The real server's roles (test/tier1/roles_test.go): the API suites run the
  # real handlers as argon_app and the auth service as argon_auth, against the
  # grants the restored copy carries.
  local app="postgres://argon_app:${ARGON_APP_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
  local authu="postgres://argon_auth:${ARGON_AUTH_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
  if [[ $TARGET == docker || $TARGET == local ]]; then
    bin="${TIER1_BIN:-$REPO_DIR/bin/tier1.test$EXE}"
    if [[ -z ${TIER1_BIN:-} ]]; then
      (cd "$REPO_DIR" && GOMAXPROCS=1 go test -p 1 -c -tags tier1 -o "$bin" ./test/tier1/) || die "cannot build the tier1 test binary"
    fi
    ARGON_TEST_ADMIN_URL="$(admin_url)" \
    ARGON_TEST_MIGRATOR_URL="$mig" ARGON_TEST_APP_URL="$app" ARGON_TEST_AUTH_URL="$authu"     ARGON_TEST_DB="$db" "$bin" "${args[@]}" > "$log" 2>&1 || rc=$?
  else
    bin="${TIER1_BIN:-/opt/argon/bin/tier1.test}"
    [[ -x $bin ]] || die "$bin missing — run 30-install-api.sh"
    sudo -u postgres env ARGON_TEST_ADMIN_URL="postgres://postgres@/postgres?host=/var/run/postgresql" \
      ARGON_TEST_MIGRATOR_URL="$mig" ARGON_TEST_APP_URL="$app" ARGON_TEST_AUTH_URL="$authu"       ARGON_TEST_DB="$db" "$bin" "${args[@]}" > "$log" 2>&1 || rc=$?   # argv: finding 30
  fi
  # A test binary prints PASS when its filter matches NOTHING. Count what ran.
  local passed failed
  passed=$(grep -c '^--- PASS' "$log" || true)
  failed=$(grep -c '^--- FAIL' "$log" || true)
  # A test's messages come BEFORE its "--- FAIL" line: print each failure with
  # the lines since its own "=== RUN", never the next test's.
  awk '/^=== RUN/ {buf=""; next} /^--- FAIL/ {print $0 buf; buf=""; next} {buf=buf "\n" $0}' "$log" | head -60 >&2 || true
  ((passed + failed > 0)) || { tail -5 "$log" >&2; rm -f "$log"; die "run_tier1: '$re' ran no tests — a pass here would be vacuous"; }
  # Failed with no failing test (TestMain refused, e.g. a fixture range
  # overflowed): say why, before the log is gone.
  if ((rc != 0 && failed == 0)); then
    printf '  !! the test binary exited %d with no failing test:\n' "$rc" >&2
    grep -v '^\(=== \|--- \|PASS\|ok \)' "$log" | tail -5 | sed 's/^/     /' >&2
  fi
  rm -f "$log"
  info "run_tier1 $re${skip:+ (skipping $skip)}: $passed passed, $failed failed"
  RUN_TIER1_PASSED=$passed
  ((rc == 0 && failed == 0))
}
