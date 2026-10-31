#!/usr/bin/env bash
# THE SERVER. PROVES that the certificate renews, rather than assuming it
# (going live, 2026-10-30). Run after 86-install-tls.sh.
#
#   1. certbot.timer is enabled and active: something will try.
#   2. certbot renew --dry-run: the whole path, today, against Let's Encrypt's
#      staging service (DNS, the security group, port 80, the webroot). It
#      changes nothing; it says a renewal WOULD work.
#   3. ONE REAL RENEWAL, forced: the certificate nginx serves must change
#      serial without anyone reloading nginx. That proves the renewal AND the
#      deploy hook; a renewal nginx never picks up is the failure a dry run
#      cannot see. Skipped if a real renewal was proven in the last 7 days
#      (Let's Encrypt allows 5 certificates per week for one name).
#   4. What nginx serves now, and for how long.
#
# Staying proven is 65-check-freshness.sh's job: it alerts when the served
# certificate has fewer than CERT_MIN_DAYS (21) left. Certbot renews at 30, so
# that alert means renewals have been failing for 9 days.
source "$(dirname "$0")/lib.sh"
on_server || die "87-prove-renewal.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"
: "${ARGON_API_DOMAIN:?set ARGON_API_DOMAIN in $ARGON_ENV}"
D=$ARGON_API_DOMAIN
[[ -f /etc/letsencrypt/live/$D/cert.pem ]] || die "no certificate for $D: run 86-install-tls.sh first"
[[ -x /etc/letsencrypt/renewal-hooks/deploy/argon-nginx-reload ]] || die "the reload hook is missing: run 86-install-tls.sh again"

served() {  # served FIELD: serial | enddate, of the certificate nginx serves for D
  echo | openssl s_client -connect 127.0.0.1:443 -servername "$D" 2>/dev/null |
    openssl x509 -noout "-$1" 2>/dev/null | cut -d= -f2
}
ondisk() { openssl x509 -noout "-$1" -in "/etc/letsencrypt/live/$D/cert.pem" | cut -d= -f2; }

# 1.
for p in is-enabled is-active; do
  s=$(systemctl "$p" certbot.timer 2>/dev/null || true)
  [[ $s == enabled || $s == active ]] || die "certbot.timer $p: ${s:-missing}. Nothing will renew."
done
ok "certbot.timer enabled and active; next run: $(systemctl show certbot.timer -p NextElapseUSecRealtime --value)"

# 2.
hook=(); certbot --help renew 2>/dev/null | grep -q -- '--run-deploy-hooks' && hook=(--run-deploy-hooks)
info "dry run against Let's Encrypt staging${hook:+ (deploy hooks included)}"
certbot renew --dry-run --cert-name "$D" "${hook[@]}" || die "the dry run failed: a renewal would fail today. Read the reason above."
ok "dry run passed: a renewal would succeed today"

# 3.
proof=/var/lib/argon/tls/renewal-proven
install -d -m 0750 /var/lib/argon/tls
if [[ -f $proof ]] && (( $(date +%s) - $(stat -c %Y "$proof") < 7 * 86400 )); then
  info "a real renewal was proven within 7 days; not forcing another:"; sed 's/^/    /' "$proof" >&2
else
  before=$(served serial)
  [[ -n $before ]] || die "cannot read the certificate nginx serves for $D on 127.0.0.1:443"
  info "forcing ONE real renewal; nginx serves serial $before"
  certbot renew --force-renewal --cert-name "$D" || die "the forced renewal failed. Read the reason above."
  after=""
  for _ in $(seq 10); do after=$(served serial); [[ -n $after && $after != "$before" ]] && break; sleep 1; done
  [[ $(ondisk serial) != "$before" ]] || die "certbot reported success, but the certificate on disk is unchanged"
  [[ $after != "$before" ]] || die "renewed on disk (serial $(ondisk serial)), but nginx still serves $before: the reload hook did not run"
  { echo "proven: $(date -u +%FT%TZ)"; echo "served before: $before"; echo "served after:  $after"; echo "valid until:   $(served enddate)"; } > "$proof"
  ok "renewal proven: nginx now serves serial $after, with nobody reloading it by hand"
fi

# 4.
end=$(served enddate)
days=$(( ($(date -d "$end" +%s) - $(date +%s)) / 86400 ))
((days >= 60)) || die "nginx serves a certificate with only $days days left ($end)"
ok "nginx serves a certificate for $D valid until $end ($days days)"
