#!/usr/bin/env bash
# INSTALLS THE VFD RELAY ON THE WALLET'S SERVER (README.md beside this file).
# Run as root, from a copy of this folder with the built binary beside it:
#
#   sudo bash install.sh --domain vfd-relay.example.com            # install / update
#   sudo bash install.sh --set VFD_ACCESS_TOKEN                     # a secret, typed unseen
#
# It touches only what is the relay's: its user, /opt/argon-vfd-proxy,
# /etc/argon-vfd-proxy, its unit, its logrotate file, its nginx site and its
# certificate. It reads nothing of the wallet's and changes none of it.
#
# Every step is read back after it is applied: a unit "started" is checked
# active, a site "reloaded" is checked answering, a refusal is checked refused.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
DOMAIN='' COOP_ADDR=54.85.168.60 PORT=8443 SET=''
while (($#)); do case $1 in
  --domain) DOMAIN=$2; shift 2 ;;
  --coop-addr) COOP_ADDR=$2; shift 2 ;;
  --port) PORT=$2; shift 2 ;;
  --set) SET=$2; shift 2 ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac; done
die() { echo "install.sh: $*" >&2; exit 1; }
ok() { echo "  ok  $*"; }
[[ $(id -u) == 0 ]] || die "run as root (sudo)"

ENVF=/etc/argon-vfd-proxy/proxy.env
install -d -m 0700 -o root -g root /etc/argon-vfd-proxy
if [[ ! -f $ENVF ]]; then
  install -m 0600 -o root -g root "$HERE/proxy.env.example" "$ENVF"
  ok "$ENVF created from the example; its secrets are empty"
fi

# ---- a secret, typed unseen: not echoed, not in history, not in ps ---------
if [[ -n $SET ]]; then
  [[ $SET =~ ^(VFD_ACCESS_TOKEN|PROXY_KEYS|VFD_BASE_URL)$ ]] || die "--set takes VFD_ACCESS_TOKEN, PROXY_KEYS or VFD_BASE_URL"
  IFS= read -rs -p "$SET (not shown): " value; echo
  [[ -n $value && $value != *$'\n'* ]] || die "empty; nothing changed"
  tmp=$(mktemp /etc/argon-vfd-proxy/.proxy.env.XXXXXX)
  chmod 0600 "$tmp"
  grep -v "^$SET=" "$ENVF" > "$tmp" || true
  printf '%s=%s\n' "$SET" "$value" >> "$tmp"
  unset value
  mv "$tmp" "$ENVF"
  ok "$SET set in $ENVF (0600 root); restart: systemctl restart argon-vfd-proxy"
  exit 0
fi

[[ -n $DOMAIN ]] || die "--domain is required: the relay's own DNS name, pointing at this server"
[[ -x $HERE/vfd-proxy ]] || die "no vfd-proxy binary beside install.sh (go build -o vfd-proxy ./cmd/vfd-proxy, GOOS=linux)"
command -v nginx >/dev/null || die "nginx is not installed"
command -v certbot >/dev/null || die "certbot is not installed (apt-get install certbot)"
for v in VFD_BASE_URL VFD_ACCESS_TOKEN PROXY_KEYS; do
  grep -Eq "^$v=.+" "$ENVF" || die "$v is empty in $ENVF: set it first with: bash install.sh --set $v"
done

# ---- the user and the binary ------------------------------------------------
id argon-vfd-proxy >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin argon-vfd-proxy
install -d -m 0755 -o root -g root /opt/argon-vfd-proxy
install -m 0755 -o root -g root "$HERE/vfd-proxy" /opt/argon-vfd-proxy/vfd-proxy
install -m 0644 -o root -g root "$HERE/argon-vfd-proxy.service" /etc/systemd/system/argon-vfd-proxy.service
install -m 0644 -o root -g root "$HERE/argon-vfd-proxy.logrotate" /etc/logrotate.d/argon-vfd-proxy
systemctl daemon-reload
systemctl enable argon-vfd-proxy >/dev/null 2>&1
systemctl restart argon-vfd-proxy
sleep 2
systemctl is-active --quiet argon-vfd-proxy || die "argon-vfd-proxy did not stay up: journalctl -u argon-vfd-proxy -n 30"
# Up means answering, and answering as the relay: an unsigned call is refused
# by it, marked as its own, before VFD is asked.
hdr=$(curl -s -o /dev/null -D - http://127.0.0.1:8095/account/enquiry | tr -d '\r')
grep -qi '^x-argon-proxy: bad_signature' <<<"$hdr" || die "the relay is not answering on 127.0.0.1:8095 as itself"
ok "argon-vfd-proxy active, refusing an unsigned call as its own"

# ---- nginx and the certificate ------------------------------------------------
render() { sed -e "s/@RELAY_DOMAIN@/$DOMAIN/g" -e "s/@RELAY_PORT@/$PORT/g" -e "s/@COOP_ADDR@/$COOP_ADDR/g" "$HERE/argon-vfd-proxy.conf.in"; }
SITE=/etc/nginx/sites-available/argon-vfd-proxy
install -d -m 0755 /var/www/argon-vfd-proxy-acme
if [[ ! -f /etc/letsencrypt/live/$DOMAIN/fullchain.pem ]]; then
  # First, port 80 alone, for Let's Encrypt's check.
  render | awk '/^server \{/{n++} n<=1' > "$SITE"
  ln -sf "$SITE" /etc/nginx/sites-enabled/argon-vfd-proxy
  nginx -t -q && systemctl reload nginx
  certbot certonly --webroot -w /var/www/argon-vfd-proxy-acme -d "$DOMAIN" --cert-name "$DOMAIN" \
    --non-interactive --agree-tos --register-unsafely-without-email --keep-until-expiring
fi
[[ -f /etc/letsencrypt/live/$DOMAIN/fullchain.pem ]] || die "no certificate for $DOMAIN"
render > "$SITE"
ln -sf "$SITE" /etc/nginx/sites-enabled/argon-vfd-proxy
nginx -t -q || die "nginx -t failed; the site is written but nginx was not reloaded"
systemctl reload nginx
sleep 1
# Applied means confirmed: this server is not the cooperative, so nginx must
# refuse it, with a certificate that verifies for the name.
code=$(curl -s -o /dev/null -w '%{http_code}' --resolve "$DOMAIN:$PORT:127.0.0.1" "https://$DOMAIN:$PORT/account/enquiry" || true)
[[ $code == 403 ]] || die "https://$DOMAIN:$PORT from this server answered $code; want 403 (only $COOP_ADDR is let in, and the certificate must verify)"
ok "nginx serves $DOMAIN:$PORT with a valid certificate and refuses every address but $COOP_ADDR"
echo
echo "Installed. Still to do outside this server: the security group opens TCP $PORT to $COOP_ADDR/32 only."
echo "Then, from the cooperative's server: deploy/README.md, \"The VFD relay\", step 4 (the first signed call)."
