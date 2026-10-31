#!/usr/bin/env bash
# THE SERVER. Puts the API on its public name over HTTPS (going live,
# 2026-10-30). Run after 85-install-nginx.sh, once BOTH are true:
#   - ARGON_API_DOMAIN's A record points at this instance's Elastic IP;
#   - the security group lets 80 and 443 in from anywhere.
#
#   1. refuses unless the name resolves to this instance's public address, the
#      app's origin passes the origin rule, and ACME_EMAIL is set;
#   2. port 80, public: the certificate check, and a redirect (argon-acme.conf.in);
#   3. a Let's Encrypt certificate for ARGON_API_DOMAIN (certbot, webroot), and
#      a deploy hook that reloads nginx after every renewal;
#   4. 443: the API, TLS, HSTS, the sign-in rate limit (argon-api-tls.conf.in);
#   5. checks the result through nginx, and that only nginx holds 80 and 443.
#
# Only the API's name: the app (ARGON_ALLOWED_ORIGINS) is hosted elsewhere,
# with its own certificate. Then prove renewal: 87-prove-renewal.sh.
# Safe to run again: an existing certificate is kept, the configs re-rendered.
source "$(dirname "$0")/lib.sh"
on_server || die "86-install-tls.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"
: "${ARGON_API_DOMAIN:?set ARGON_API_DOMAIN (the API's public name) in $ARGON_ENV}"
: "${ACME_EMAIL:?set ACME_EMAIL in $ARGON_ENV: Let's Encrypt writes there if a certificate is about to expire}"
D=$ARGON_API_DOMAIN
bash "$DEPLOY_DIR/nginx/check-origin.sh" "${ARGON_ALLOWED_ORIGINS:-}" "$D" || die "fix ARGON_ALLOWED_ORIGINS in $ARGON_ENV"
[[ -f /etc/nginx/sites-enabled/argon ]] || die "nginx is not installed: run 85-install-nginx.sh first"

# 1. The name must already point HERE. Otherwise the certificate check goes
#    somewhere else, fails, and counts against Let's Encrypt's limits.
tok=$(curl -sf -m 5 -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60') ||
  die "cannot reach the instance metadata service"
pub=$(curl -sf -m 5 -H "X-aws-ec2-metadata-token: $tok" http://169.254.169.254/latest/meta-data/public-ipv4) ||
  die "this instance has no public IPv4 address"
dns=$(getent ahostsv4 "$D" | awk '{print $1}' | sort -u | tr '\n' ' ')
[[ $dns == "$pub " ]] || die "$D resolves to '${dns:-nothing}', and this instance's public address is $pub: fix the A record (and wait for it) first"
ok "$D resolves to this instance ($pub)"

if ! command -v certbot >/dev/null; then
  info "installing certbot (Ubuntu's package: its systemd timer does the renewals)"
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q certbot >/dev/null
fi
install -d -m 0755 /var/www/acme

render() {  # render TEMPLATE TARGET-NAME, with the server's values
  NGX_API_DOMAIN=$D NGX_APP_ORIGIN=$ARGON_ALLOWED_ORIGINS NGX_UPSTREAM="127.0.0.1:${API_PORT:-8080}" \
  NGX_HTTP_LISTEN=0.0.0.0:80 NGX_HTTPS_LISTEN=0.0.0.0:443 \
  NGX_CERT="/etc/letsencrypt/live/$D/fullchain.pem" NGX_KEY="/etc/letsencrypt/live/$D/privkey.pem" \
  NGX_ACME_ROOT=/var/www/acme \
    bash "$DEPLOY_DIR/nginx/render-public.sh" "$DEPLOY_DIR/nginx/$1" > "/etc/nginx/sites-available/$2.new" ||
    die "cannot render $1"
  mv "/etc/nginx/sites-available/$2.new" "/etc/nginx/sites-available/$2"
  ln -sf "/etc/nginx/sites-available/$2" "/etc/nginx/sites-enabled/$2"
}
reload() {
  nginx -t 2>&1 | sed 's/^/    /' >&2
  nginx -t >/dev/null 2>&1 || die "nginx configuration test failed"
  systemctl reload nginx
}

# 2. Port 80 first: the certificate check needs it, and 443 needs the certificate.
render argon-acme.conf.in argon-acme
[[ -f /etc/letsencrypt/live/$D/fullchain.pem ]] || rm -f /etc/nginx/sites-enabled/argon-api-tls
reload
ok "port 80 serves the certificate check for $D"

# 3. The certificate.
if [[ -f /etc/letsencrypt/live/$D/fullchain.pem ]]; then
  ok "a certificate for $D exists; kept (renewal is certbot.timer's, proven by 87-prove-renewal.sh)"
else
  info "asking Let's Encrypt for a certificate for $D"
  certbot certonly --webroot -w /var/www/acme -d "$D" --cert-name "$D" \
    --email "$ACME_EMAIL" --agree-tos --no-eff-email --non-interactive --key-type ecdsa ||
    die "no certificate: read the reason above (most often: port 80 not open in the security group, or DNS not yet pointing here)"
fi
# A renewed certificate is only SERVED after nginx reloads: without this hook
# a renewal succeeds and nginx keeps serving the old one until it expires.
install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
cat > /etc/letsencrypt/renewal-hooks/deploy/argon-nginx-reload <<'EOF'
#!/bin/sh
# Installed by deploy/86-install-tls.sh: a renewed certificate is served only after a reload.
exec systemctl reload nginx
EOF
chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/argon-nginx-reload

# 4. 443.
render argon-api-tls.conf.in argon-api-tls
reload

# 5. Through nginx, on this host (independent of the security group).
r=(--resolve "$D:443:127.0.0.1" --resolve "$D:80:127.0.0.1" -s -o /dev/null)
c=$(curl "${r[@]}" -w '%{http_code}' -X POST "http://$D/auth/sign-in")
[[ $c == 403 ]] || die "plain-HTTP sign-in on port 80 answered $c, not 403 tls_required"
c=$(curl "${r[@]}" -w '%{http_code}' "http://$D/healthz")
[[ $c == 301 ]] || die "plain-HTTP GET on port 80 answered $c, not a redirect"
hsts=$(curl "${r[@]}" -D - "https://$D/healthz" | tr -d '\r' | awk 'tolower($0) ~ /^strict-transport-security:/')
[[ -n $hsts ]] || die "https://$D/healthz carries no Strict-Transport-Security (or the certificate does not verify)"
c=$(curl "${r[@]}" -w '%{http_code}' "https://$D/healthz")
info "https://$D/healthz -> $c (200 with the API running; 503 service_unavailable while it is stopped)"
bash "$DEPLOY_DIR/75-assert-loopback.sh"
ok "the API's public name is served over HTTPS: https://$D"
info "now prove renewal:  sudo ARGON_ENV=$ARGON_ENV bash $DEPLOY_DIR/87-prove-renewal.sh"
