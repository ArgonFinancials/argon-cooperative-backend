#!/usr/bin/env bash
# Roles and the argon database. Idempotent — re-running rotates passwords to
# whatever the env file now says.
#
#   argon_owner     NOLOGIN. Owns the database and every object in it.
#   argon_migrator  LOGIN. Lands in argon_owner on connect; used ONLY by
#                   migrations and restores.
#   argon_app       LOGIN. The API. Never the owner: the owner can TRUNCATE
#                   (row triggers do not fire) and DISABLE TRIGGER, either of
#                   which would reach around ledger immutability.
#   argon_backup    LOGIN. pg_read_all_data and nothing else.
#
# Passwords are sent on stdin as psql variables, never as argv.
source "$(dirname "$0")/lib.sh"
require_env ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_BACKUP_PASSWORD

for v in ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_BACKUP_PASSWORD; do
  [[ ${!v} =~ ^[A-Za-z0-9._~-]{16,}$ ]] || die "$v must be >=16 chars of [A-Za-z0-9._~-]"
done

info "roles"
{
printf "\\set migrator_pw '%s'\n\\set app_pw '%s'\n\\set backup_pw '%s'\n" \
  "$ARGON_MIGRATOR_PASSWORD" "$ARGON_APP_PASSWORD" "$ARGON_BACKUP_PASSWORD"
cat <<'SQL'
SET password_encryption = 'scram-sha-256';
SELECT format('CREATE ROLE %I NOLOGIN', r)
  FROM unnest(ARRAY['argon_owner']) r
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) \gexec
SELECT format('CREATE ROLE %I LOGIN', r)
  FROM unnest(ARRAY['argon_migrator','argon_app','argon_backup']) r
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) \gexec

ALTER ROLE argon_owner    NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
ALTER ROLE argon_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'migrator_pw';
ALTER ROLE argon_app      LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'app_pw';
ALTER ROLE argon_backup   LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'backup_pw';

GRANT argon_owner TO argon_migrator;
GRANT pg_read_all_data TO argon_backup;

-- Objects created by migrations are owned by argon_owner, not the login role.
ALTER ROLE argon_migrator SET role = 'argon_owner';
-- A migration waits at most 5s for a lock rather than queueing behind live
-- traffic and blocking everything that arrives after it.
ALTER ROLE argon_migrator SET lock_timeout = '5s';
-- Runaway protection for the API.
ALTER ROLE argon_app SET statement_timeout = '30s';
ALTER ROLE argon_app SET idle_in_transaction_session_timeout = '60s';
ALTER ROLE argon_app CONNECTION LIMIT 30;
SQL
} | pg_super -d postgres

pg_super -d postgres -v db="$ARGON_DB" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER argon_owner', :'db')
 WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'db') \gexec
SELECT format('ALTER DATABASE %I OWNER TO argon_owner', :'db') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO argon_app, argon_migrator, argon_backup', :'db') \gexec
SQL

info "schema privileges in $ARGON_DB"
pg_super -d "$ARGON_DB" <<'SQL'
ALTER SCHEMA public OWNER TO argon_owner;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO argon_app, argon_backup;
SQL

ok "roles and database '$ARGON_DB' ready"
