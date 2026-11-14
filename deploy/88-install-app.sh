#!/usr/bin/env bash
# THE SERVER. Puts the app (the frontend's static export) on its public name,
# beside the API (2026-11-14: no hosting elsewhere; the app's files are served
# by nginx from this instance, and no process runs them). Two steps, because
# the name moves between them:
#
#   --preview  BEFORE DNS points here. Port 80 serves the app on its name
#              (argon-app-preview.conf.in), so the release can be checked with
#              `curl --resolve` from anywhere, and so Let's Encrypt's check
#              can be answered once DNS moves. Nobody else reaches it: the
#              name still points elsewhere.
#   --tls      AFTER the app's A record points at this instance. A Let's
#              Encrypt certificate for the app's name (webroot, as the API's),
#              then port 80 redirects (argon-app-acme.conf.in) and 443 serves
#              the app (argon-app-tls.conf.in).
#
# The app's name is ARGON_ALLOWED_ORIGINS' host: one setting, so the name
# served and the origin the API allows cannot differ. The files are what the
# deploy installed (36-install-frontend.sh): /var/www/argon-frontend/current.
# Run after 86-install-tls.sh (the API's sites hold the default servers).
# Safe to run again. Tests of the sites: test/ops/app_site_test.sh.
#
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/88-install-app.sh --preview
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/88-install-app.sh --tls
source "$(dirname "$0")/lib.sh"
on_server || die "88-install-app.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"
mode=${1:-}
[[ $mode == --preview || $mode == --tls ]] || die "say which step: --preview (before DNS moves) or --tls (after)"
: "${ARGON_API_DOMAIN:?set ARGON_API_DOMAIN in $ARGON_ENV}"
: "${ACME_EMAIL:?set ACME_EMAIL in $ARGON_ENV}"
bash "$DEPLOY_DIR/nginx/check-origin.sh" "${ARGON_ALLOWED_ORIGINS:-}" "$ARGON_API_DOMAIN" || die "fix ARGON_ALLOWED_ORIGINS in $ARGON_ENV"
A=${ARGON_ALLOWED_ORIGINS#https://}
ROOT=/var/www/argon-frontend/current
[[ -f /etc/nginx/sites-enabled/argon-api-tls ]] || die "the API's public sites are not installed: run 86-install-tls.sh first"
[[ -f $ROOT/index.html ]] || die "no frontend installed at $ROOT: deploy a release that carries one (deploy/frontend.ref) first"

render() {  # render TEMPLATE TARGET-NAME
  NGX_API_DOMAIN=$ARGON_API_DOMAIN NGX_APP_ORIGIN=$ARGON_ALLOWED_ORIGINS NGX_UPSTREAM="127.0.0.1:${API_PORT:-8080}" \
  NGX_HTTP_LISTEN=0.0.0.0:80 NGX_HTTPS_LISTEN=0.0.0.0:443 \
  NGX_CERT="/etc/letsencrypt/live/$ARGON_API_DOMAIN/fullchain.pem" NGX_KEY="/etc/letsencrypt/live/$ARGON_API_DOMAIN/privkey.pem" \
  NGX_ACME_ROOT=/var/www/acme NGX_APP_ROOT=$ROOT \
  NGX_APP_CERT="/etc/letsencrypt/live/$A/fullchain.pem" NGX_APP_KEY="/etc/letsencrypt/live/$A/privkey.pem" \
    bash "$DEPLOY_DIR/nginx/render-public.sh" "$DEPLOY_DIR/nginx/$1" > "/etc/nginx/sites-available/$2.new" ||
    die "cannot render $1"
  mv "/etc/nginx/sites-available/$2.new" "/etc/nginx/sites-available/$2"
  ln -sf "/etc/nginx/sites-available/$2" "/etc/nginx/sites-enabled/$2"
}
apply() { bash "$DEPLOY_DIR/nginx/apply.sh" "$@" || die "nginx did not take the change: read its errors above"; }

# Checked at the instance's PRIVATE address: on 127.0.0.1 nginx answers with
# 85's loopback site before it looks at the name (finding 68).
tok=$(curl -sf -m 5 -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60') ||
  die "cannot reach the instance metadata service"
priv=$(curl -sf -m 5 -H "X-aws-ec2-metadata-token: $tok" http://169.254.169.254/latest/meta-data/local-ipv4) ||
  die "this instance has no private IPv4 address"
pub=$(curl -sf -m 5 -H "X-aws-ec2-metadata-token: $tok" http://169.254.169.254/latest/meta-data/public-ipv4) ||
  die "this instance has no public IPv4 address"
want=$(cat "$ROOT/index.html")

if [[ $mode == --preview ]]; then
  [[ ! -f /etc/letsencrypt/live/$A/fullchain.pem ]] ||
    die "$A already has a certificate: the preview is for before it; run --tls (it is safe to run again)"
  rm -f /etc/nginx/sites-enabled/argon-app-tls
  render argon-app-preview.conf.in argon-app
  apply 0.0.0.0:80 0.0.0.0:443
  got=$(curl -s -m 10 --resolve "$A:80:$priv" "http://$A/")
  [[ $got == "$want" ]] || die "http://$A/ at $priv did not serve the installed index.html: '${got:0:160}'"
  bash "$DEPLOY_DIR/75-assert-loopback.sh"
  ok "the app is served on port 80 for $A, from release $(readlink /var/www/argon-frontend/current)"
  info "check it from anywhere:  curl -s --resolve $A:80:$pub http://$A/ | head -c 300"
  info "then point $A's A record at $pub, wait for it to resolve, and run:  sudo ARGON_ENV=$ARGON_ENV bash $0 --tls"
  exit 0
fi

# --tls: the name must point HERE, or the certificate check goes elsewhere,
# fails, and counts against Let's Encrypt's limits.
dns=$(getent ahostsv4 "$A" | awk '{print $1}' | sort -u | tr '\n' ' ')
[[ $dns == "$pub " ]] || die "$A resolves to '${dns:-nothing}', and this instance's public address is $pub: change the A record (and wait for it) first"
ok "$A resolves to this instance ($pub)"
if [[ -f /etc/letsencrypt/live/$A/fullchain.pem ]]; then
  ok "a certificate for $A exists; kept (renewal is certbot.timer's, with 86's reload hook)"
else
  # Port 80 must answer the check: the preview's site does, so keep it until the certificate exists.
  [[ -f /etc/nginx/sites-enabled/argon-app ]] || { render argon-app-preview.conf.in argon-app; apply 0.0.0.0:80 0.0.0.0:443; }
  info "asking Let's Encrypt for a certificate for $A"
  certbot certonly --webroot -w /var/www/acme -d "$A" --cert-name "$A" \
    --email "$ACME_EMAIL" --agree-tos --no-eff-email --non-interactive --key-type ecdsa ||
    die "no certificate: read the reason above (most often: DNS not yet pointing here everywhere)"
fi
[[ -x /etc/letsencrypt/renewal-hooks/deploy/argon-nginx-reload ]] ||
  die "the renewal reload hook is missing (86-install-tls.sh installs it): a renewed certificate would not be served"

render argon-app-acme.conf.in argon-app
render argon-app-tls.conf.in argon-app-tls
apply 0.0.0.0:80 0.0.0.0:443

r=(-s -m 10 --resolve "$A:80:$priv" --resolve "$A:443:$priv")
c=$(curl "${r[@]}" -o /dev/null -w '%{http_code} %{redirect_url}' "http://$A/")
[[ $c == "301 https://$A/" ]] || die "http://$A/ answered '$c', not a redirect to https://$A/"
hdrs=$(curl "${r[@]}" -o /dev/null -D - "https://$A/" | tr -d '\r')
grep -qi '^strict-transport-security: max-age=31536000' <<<"$hdrs" || die "https://$A/ carries no HSTS (or its certificate does not verify)"
got=$(curl "${r[@]}" "https://$A/")
[[ $got == "$want" ]] || die "https://$A/ did not serve the installed index.html: '${got:0:160}'"
bash "$DEPLOY_DIR/75-assert-loopback.sh"
ok "the app's public name is served over HTTPS: https://$A"
info "renewal: certbot.timer renews both certificates; prove it with  sudo ARGON_ENV=$ARGON_ENV bash $DEPLOY_DIR/87-prove-renewal.sh"
