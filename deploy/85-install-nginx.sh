#!/usr/bin/env bash
# THE SERVER. Nginx in front of the API, LOOPBACK ONLY until a domain and TLS
# exist (deploy/nginx/argon.conf): /healthz is proxied, everything else is
# refused with tls_required, so sign-in is never served over plain HTTP.
#
# Ubuntu's package starts nginx at install with a default site on EVERY
# interface. Its auto-start is blocked for the install, and the default site is
# removed before nginx first runs; 75-assert-loopback.sh then proves the result.
source "$(dirname "$0")/lib.sh"
on_server || die "85-install-nginx.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"

if ! command -v nginx >/dev/null; then
  info "installing nginx (auto-start blocked during the install)"
  printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d; chmod 0755 /usr/sbin/policy-rc.d
  trap 'rm -f /usr/sbin/policy-rc.d' EXIT
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q nginx >/dev/null
  rm -f /usr/sbin/policy-rc.d; trap - EXIT
fi
rm -f /etc/nginx/sites-enabled/default
install -m 0644 "$DEPLOY_DIR/nginx/argon.conf" /etc/nginx/sites-available/argon
ln -sf /etc/nginx/sites-available/argon /etc/nginx/sites-enabled/argon
nginx -t 2>&1 | sed 's/^/    /' >&2
nginx -t >/dev/null 2>&1 || die "nginx configuration test failed"
systemctl enable -q nginx
systemctl restart nginx

code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1/auth/sign-in -X POST)
[[ $code == 403 ]] || die "plain-HTTP sign-in through nginx answered $code, not 403 tls_required"
info "through nginx: /auth/sign-in -> 403 tls_required; /healthz -> $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1/healthz) (502 until the API runs)"
bash "$DEPLOY_DIR/75-assert-loopback.sh"
ok "nginx on loopback only"
