#!/usr/bin/env bash
# HOST ONLY. Builds the API from this checkout, installs it and the deploy
# scripts under /opt/argon, writes /etc/argon/argon.env and the systemd unit.
# Does NOT start the API — that happens after migrations (40) have run.
#
# Run from the checkout:  sudo ARGON_ENV=/etc/argon/deploy.env deploy/30-install-api.sh
source "$(dirname "$0")/lib.sh"
[[ $TARGET == host ]] || die "30-install-api.sh is host-only"
[[ $EUID -eq 0 ]] || die "run with sudo"
require_env ARGON_APP_PASSWORD API_PORT
builder=${SUDO_USER:-ubuntu}

id argon &>/dev/null || useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin argon
install -d -m 0755 /opt/argon /opt/argon/bin
install -d -m 0750 -o root -g argon /etc/argon

info "building as $builder"
out=$(sudo -u "$builder" mktemp -d)
sudo -u "$builder" env -C "$REPO_DIR" PATH="/usr/local/go/bin:$PATH" CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o "$out/argon-api" ./cmd/api
install -m 0755 "$out/argon-api" /opt/argon/bin/argon-api
rm -rf "$out"
ok "/opt/argon/bin/argon-api"

info "installing deploy scripts and migrations"
rm -rf /opt/argon/deploy /opt/argon/migrations
cp -r "$REPO_DIR/deploy" /opt/argon/deploy
cp -r "$REPO_DIR/migrations" /opt/argon/migrations
rm -f /opt/argon/deploy/env/*.env        # examples only; secrets live in /etc/argon
chmod -R go-w /opt/argon

umask 027
cat > /etc/argon/argon.env <<EOF
DATABASE_URL=postgres://argon_app:${ARGON_APP_PASSWORD}@127.0.0.1:${PGPORT}/${ARGON_DB}?sslmode=disable
PORT=${API_PORT}
ENV=production
EOF
chown root:argon /etc/argon/argon.env; chmod 0640 /etc/argon/argon.env
chmod 0600 "$ARGON_ENV"

install -m 0644 "$DEPLOY_DIR/systemd/argon-api.service" /etc/systemd/system/argon-api.service
systemctl daemon-reload
systemctl enable argon-api.service
ok "argon-api.service installed and enabled (not started). Start after 40-migrate.sh:  sudo systemctl start argon-api"
