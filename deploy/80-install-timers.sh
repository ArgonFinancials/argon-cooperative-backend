#!/usr/bin/env bash
# THE SERVER. Installs the nightly backup and weekly restore-drill timers —
# but only after a drill has passed by hand, so the first scheduled drill is
# not also the first time the restore path has ever run.
source "$(dirname "$0")/lib.sh"
on_server || die "80-install-timers.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"

case "$BACKUP_STORE" in
  # || true: with no drill yet, grep fails and set -e would exit SILENTLY here,
  # before the message below that says what to do.
  s3://*) last=$(aws s3 ls --recursive "$BACKUP_STORE/drills/" | grep '\.ok$' | sort | tail -1 || true) ;;
  file://*) last=$(find "${BACKUP_STORE#file://}/drills" -name '*.ok' 2>/dev/null | sort | tail -1) ;;
esac
[[ -n $last ]] || die "no passing drill recorded in $BACKUP_STORE/drills — run: sudo ARGON_ENV=$ARGON_ENV bash $DEPLOY_DIR/60-restore.sh --drill"
ok "last passing drill: $last"

for u in argon-backup.service argon-backup.timer argon-restore-drill.service argon-restore-drill.timer \
         argon-freshness.service argon-freshness.timer argon-alert@.service; do
  install -m 0644 "$DEPLOY_DIR/systemd/$u" "/etc/systemd/system/$u"
done
install -m 0755 "$DEPLOY_DIR/motd/60-argon-alerts" /etc/update-motd.d/60-argon-alerts
install -d -m 0750 /var/lib/argon/alerts/open /var/lib/argon/alerts/acked
systemctl daemon-reload
systemctl enable --now argon-backup.timer argon-restore-drill.timer argon-freshness.timer
systemctl list-timers 'argon-*' --no-pager
echo
info "a failed job raises an alert (OnFailure=argon-alert@); a job that stops running is caught by argon-freshness"
case ${ALERT_CHANNEL:-none} in
  none) printf '\033[33m  ?? ALERT_CHANNEL=none: alerts go to the journal and the SSH login banner only. NOBODY IS NOTIFIED.\033[0m\n' >&2 ;;
  *)    ok "alerts also go to ALERT_CHANNEL=$ALERT_CHANNEL" ;;
esac
[[ -n ${ALERT_HEARTBEAT_URL:-} ]] || printf '\033[33m  ?? no ALERT_HEARTBEAT_URL: if this server dies, nothing outside it notices.\033[0m\n' >&2
