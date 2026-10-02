#!/usr/bin/env bash
# THE SERVER. PostgreSQL 18 as a container (deploy/compose/docker-compose.prod.yml):
# Debian-based image, Argon's own network, published on loopback only.
# Idempotent. Replaces the native install (10-install-postgres.sh, retired
# 2026-10-02 when the server moved to the container route).
source "$(dirname "$0")/lib.sh"
on_server || die "10-start-postgres.sh is for the server (ARGON_HOST=1)"
[[ $TARGET == docker ]] || die "on the server Postgres runs in a container: TARGET=docker"
[[ $EUID -eq 0 ]] || die "run with sudo"
require_env POSTGRES_PASSWORD
[[ -f $COMPOSE_FILE ]] || die "COMPOSE_FILE $COMPOSE_FILE not found"
[[ $COMPOSE_FILE == */docker-compose.prod.yml ]] || die "COMPOSE_FILE must be the production compose file, not $COMPOSE_FILE"

info "pulling the postgres image"
dc pull -q postgres
info "starting postgres"
dc up -d postgres
cid=$(dc ps -q postgres)
[[ -n $cid ]] || die "the postgres container did not start"
for _ in $(seq 60); do
  [[ $(docker inspect -f '{{.State.Health.Status}}' "$cid") == healthy ]] && break
  sleep 2
done
[[ $(docker inspect -f '{{.State.Health.Status}}' "$cid") == healthy ]] || die "postgres is not healthy after 2 minutes: docker logs $cid"

v=$(pg_super -d postgres -At -c 'SHOW server_version_num' </dev/null)
[[ $v == "$PG_MAJOR"* ]] || die "the container runs server_version_num=$v, not PostgreSQL $PG_MAJOR"
info "$(pg_super -d postgres -At -c "SELECT 'server ' || current_setting('server_version') || ', template1 ' || pg_encoding_to_char(encoding) || ' collate ' || datcollate FROM pg_database WHERE datname = 'template1'" </dev/null)"
info "image $(docker inspect -f '{{.Config.Image}}' "$cid") ($(docker inspect -f '{{.Image}}' "$cid" | cut -c1-19)…), network $(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$cid")"

bash "$DEPLOY_DIR/75-assert-loopback.sh"
ok "postgres $v in a container, on loopback only"
