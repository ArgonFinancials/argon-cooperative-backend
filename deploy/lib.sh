#!/usr/bin/env bash
# Shared by every deploy script. The ONLY thing that differs between local
# Docker and the EC2 host is the env file named by ARGON_ENV — never the
# scripts. See deploy/README.md.
#
# Two ways of reaching Postgres, and only two:
#   pg_super  — the cluster superuser, used for roles and databases only.
#               docker: `docker compose exec` over the container's socket.
#               host:   `sudo -u postgres` over the local socket (peer auth).
#   pg_as     — a named argon_* role over TCP to 127.0.0.1, password from the
#               environment (never argv, so it never shows in `ps`).
# Client tools (psql, pg_dump, pg_restore) always come from the same major
# version as the server: inside the container locally, PGDG 16 on the host.

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

: "${TARGET:?TARGET must be docker or host}"
: "${ARGON_DB:=argon}"
: "${PGPORT:=5432}"
: "${MIGRATIONS_DIR:=$REPO_DIR/migrations}"
: "${MIGRATE_BIN:=migrate}"
: "${BACKUP_STORE:?BACKUP_STORE must be s3://bucket/prefix or file:///path}"
: "${BACKUP_SSE:=AES256}"
: "${COMPOSE_FILE:=$REPO_DIR/docker-compose.yml}"
case "$TARGET" in docker|host) ;; *) die "TARGET=$TARGET (want docker|host)";; esac
# A relative file:// store is relative to the repo, not the caller's cwd.
if [[ $BACKUP_STORE == file://* && ${BACKUP_STORE#file://} != /* ]]; then
  BACKUP_STORE="file://$REPO_DIR/${BACKUP_STORE#file://./}"
fi

require_env() { local v; for v in "$@"; do [[ -n "${!v:-}" ]] || die "$v is empty in $ARGON_ENV"; done; }

dc() { docker compose --env-file "$ARGON_ENV" -f "$COMPOSE_FILE" "$@"; }

# Superuser access. Reads SQL on stdin.
pg_super() {
  if [[ $TARGET == docker ]]; then
    dc exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U postgres "$@"
  else
    sudo -u postgres psql -X -q -v ON_ERROR_STOP=1 "$@"
  fi
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
    *) die "pg_as: unknown role $role" ;;
  esac
  pg_tool "$role" "$pwvar" psql -X -v ON_ERROR_STOP=1 -d "$db" "$@"
}

# Scratch databases (restore drills, constraint probes). Owned by argon_owner,
# connectable only by the argon roles. Name must match the scratch pattern so a
# typo can never point this at the real database.
create_scratch_db() {
  local db=$1
  [[ $db =~ ^argon_(drill|verify)_[0-9a-z_]+$ ]] || die "refusing scratch db name '$db'"
  pg_super -d postgres <<SQL
CREATE DATABASE "$db" OWNER argon_owner;
REVOKE ALL ON DATABASE "$db" FROM PUBLIC;
GRANT CONNECT ON DATABASE "$db" TO argon_app, argon_migrator, argon_backup;
SQL
}
drop_scratch_db() {
  local db=$1
  [[ $db =~ ^argon_(drill|verify)_[0-9a-z_]+$ ]] || die "refusing to drop '$db'"
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
run_tier1() {
  local db=$1 re=$2 bin rc=0 log
  log=$(mktemp)
  require_env ARGON_MIGRATOR_PASSWORD
  local mig="postgres://argon_migrator:${ARGON_MIGRATOR_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable"
  if [[ $TARGET == docker ]]; then
    require_env POSTGRES_PASSWORD
    bin="${TIER1_BIN:-$REPO_DIR/bin/tier1.test$( [[ $OSTYPE == msys* || $OSTYPE == cygwin* ]] && echo .exe )}"
    if [[ -z ${TIER1_BIN:-} ]]; then
      (cd "$REPO_DIR" && GOMAXPROCS=1 go test -p 1 -c -tags tier1 -o "$bin" ./test/tier1/) || die "cannot build the tier1 test binary"
    fi
    ARGON_TEST_ADMIN_URL="postgres://postgres:${POSTGRES_PASSWORD}@127.0.0.1:${PGPORT}/postgres?sslmode=disable" \
    ARGON_TEST_MIGRATOR_URL="$mig" ARGON_TEST_DB="$db" "$bin" -test.run "$re" -test.count=1 -test.v > "$log" 2>&1 || rc=$?
  else
    bin="${TIER1_BIN:-/opt/argon/bin/tier1.test}"
    [[ -x $bin ]] || die "$bin missing — run 30-install-api.sh"
    sudo -u postgres env ARGON_TEST_ADMIN_URL="postgres://postgres@/postgres?host=/var/run/postgresql" \
      ARGON_TEST_MIGRATOR_URL="$mig" ARGON_TEST_DB="$db" "$bin" -test.run "$re" -test.count=1 -test.v > "$log" 2>&1 || rc=$?
  fi
  # A test binary prints PASS when its filter matches NOTHING. Count what ran.
  local passed failed
  passed=$(grep -c '^--- PASS' "$log" || true)
  failed=$(grep -c '^--- FAIL' "$log" || true)
  grep -E '^--- FAIL' -A3 "$log" | head -40 >&2 || true
  ((passed + failed > 0)) || { tail -5 "$log" >&2; rm -f "$log"; die "run_tier1: '$re' ran no tests — a pass here would be vacuous"; }
  rm -f "$log"
  info "run_tier1 $re: $passed passed, $failed failed"
  ((rc == 0 && failed == 0))
}
