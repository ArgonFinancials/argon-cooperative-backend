#!/usr/bin/env bash
# THE SERVER. Installs the nightly backup and weekly restore-drill timers —
# but only after a drill has passed by hand, so the first scheduled drill is
# not also the first time the restore path has ever run.
source "$(dirname "$0")/lib.sh"
on_server || die "80-install-timers.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"

case "$BACKUP_STORE" in
  s3://*) last=$(aws s3 ls --recursive "$BACKUP_STORE/drills/" | grep '\.ok$' | sort | tail -1) ;;
  file://*) last=$(find "${BACKUP_STORE#file://}/drills" -name '*.ok' 2>/dev/null | sort | tail -1) ;;
esac
[[ -n $last ]] || die "no passing drill recorded in $BACKUP_STORE/drills — run: sudo ARGON_ENV=$ARGON_ENV bash $DEPLOY_DIR/60-restore.sh --drill"
ok "last passing drill: $last"

for u in argon-backup.service argon-backup.timer argon-restore-drill.service argon-restore-drill.timer; do
  install -m 0644 "$DEPLOY_DIR/systemd/$u" "/etc/systemd/system/$u"
done
systemctl daemon-reload
systemctl enable --now argon-backup.timer argon-restore-drill.timer
systemctl list-timers 'argon-*' --no-pager
echo
echo "A failed drill shows as a failed unit:  systemctl --failed ; journalctl -u argon-restore-drill"
echo "Nothing pages anyone yet — wire OnFailure= to an alert channel (SNS/email) before relying on it."
