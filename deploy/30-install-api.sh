#!/usr/bin/env bash
# THE SERVER. Prepares the API to run as a container, from the installed
# release (/opt/argon/src): builds the image, builds the restore drill's test
# binary, writes the container's environment. Does NOT start the API:
# starting it is its own deliberate step, after the drill.
#
#   cd /opt/argon/src && sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/30-install-api.sh
#
# The container's environment (/etc/argon/argon.env): the handlers connect as
# argon_app, the auth service on its own connection as argon_auth, both to the
# postgres service on Argon's network.
source "$(dirname "$0")/lib.sh"
on_server || die "30-install-api.sh is for the server (ARGON_HOST=1)"
[[ $TARGET == docker ]] || die "on the server the API runs in a container: TARGET=docker"
[[ $EUID -eq 0 ]] || die "run with sudo"
[[ $REPO_DIR == /opt/argon/src ]] || die "run from the installed release (/opt/argon/src), not from $REPO_DIR: see 05-install-release.sh"
require_env ARGON_APP_PASSWORD ARGON_AUTH_PASSWORD ARGON_CODE_KEY ARGON_CSRF_KEY ARGON_DB API_PORT
[[ $ARGON_CODE_KEY =~ ^[0-9a-f]{64,}$ ]] || die "ARGON_CODE_KEY must be at least 32 bytes of lowercase hex (openssl rand -hex 32)"
[[ $ARGON_CSRF_KEY =~ ^[0-9a-f]{64,}$ ]] || die "ARGON_CSRF_KEY must be at least 32 bytes of lowercase hex, generated on the instance straight into the deploy env file (openssl rand -hex 32; deploy/README.md, first install)"
command -v go >/dev/null || die "go not found: run 15-install-tools.sh"

export ARGON_RELEASE=$(cat "$REPO_DIR/RELEASE")
info "building the restore drill's test binary"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
(cd "$REPO_DIR" && GOPATH=/root/go GOCACHE=/root/.cache/go-build CGO_ENABLED=0 GOMAXPROCS=1 \
  go test -c -tags tier1 -o "$tmp/tier1.test" ./test/tier1/) || die "cannot build the tier1 test binary"
install -m 0755 "$tmp/tier1.test" /opt/argon/bin/tier1.test

info "building the API image argon-api:$ARGON_RELEASE"
dc build api || die "image build failed"

umask 077
cat > /etc/argon/argon.env <<EOF
DATABASE_URL=postgres://argon_app:${ARGON_APP_PASSWORD}@postgres:5432/${ARGON_DB}?sslmode=disable
AUTH_DATABASE_URL=postgres://argon_auth:${ARGON_AUTH_PASSWORD}@postgres:5432/${ARGON_DB}?sslmode=disable
AUTH_CODE_KEY=${ARGON_CODE_KEY}
ARGON_CSRF_KEY=${ARGON_CSRF_KEY}
PORT=8080
ENV=production
ARGON_ALLOWED_ORIGINS=${ARGON_ALLOWED_ORIGINS:-}
EOF
chmod 0600 /etc/argon/argon.env "$ARGON_ENV"
ok "image argon-api:$ARGON_RELEASE and /opt/argon/bin/tier1.test ready; the API is NOT started"
info "start it deliberately:  cd /opt/argon/src && sudo docker compose --env-file $ARGON_ENV -f $COMPOSE_FILE --profile api up -d api"
