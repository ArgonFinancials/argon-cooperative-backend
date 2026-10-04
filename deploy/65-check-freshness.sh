#!/usr/bin/env bash
# THE SERVER. Catches the failures OnFailure= cannot: a job that never runs.
# A disabled timer, a deleted unit or a stopped clock fails nothing; it just
# stops producing backups. This reads the RESULTS, not the jobs:
#
#   - the newest dump in BACKUP_STORE is at most BACKUP_MAX_AGE_H hours old
#   - the newest PASSING drill is at most DRILL_MAX_AGE_D days old
#   - both timers are enabled and active, and neither job's last run failed
#   - the backup store's disk has room (file:// only)
#
# Anything wrong: one alert listing all of it (alert.sh), exit 3 ("failed, and
# already alerted": argon-alert@ does not raise it twice). All fresh:
# a heartbeat to ALERT_HEARTBEAT_URL when set — an outside service that alerts
# when the heartbeat STOPS, which is the only way a dead server is noticed.
source "$(dirname "$0")/lib.sh"
on_server || die "65-check-freshness.sh is for the server (ARGON_HOST=1)"
: "${BACKUP_MAX_AGE_H:=26}" "${DRILL_MAX_AGE_D:=8}" "${BACKUP_MIN_FREE_GB:=5}"
: "${ARGON_TIMERS:=argon-backup.timer argon-restore-drill.timer}"

problems=()
now=$(date -u +%s)
# Keys carry their UTC time: dumps/…/argon-20261003T053515Z[-label].dump,
# drills/…/drill-20261003T053548Z.ok. Same on S3 and on disk.
# A key with no readable time counts as infinitely old (stale), never as an
# error: under set -e a failing assignment would end this check SILENTLY.
age_of() { local ts; ts=$(grep -oE '[0-9]{8}T[0-9]{6}Z' <<<"$1" | head -1 || true)
  if [[ -z $ts ]]; then echo 999999999; return; fi
  echo $(( now - $(date -u -d "${ts:0:4}-${ts:4:2}-${ts:6:2} ${ts:9:2}:${ts:11:2}:${ts:13:2}" +%s) )); }

dump=$(store_latest_dump || true)
if [[ -z $dump ]]; then problems+=("no dump at all in $BACKUP_STORE/dumps")
else a=$(age_of "$dump"); h=$((a / 3600))
  ((a <= BACKUP_MAX_AGE_H * 3600)) && ok "newest dump ${h}h old: $dump" || problems+=("newest dump is ${h}h old (limit ${BACKUP_MAX_AGE_H}h): $dump")
fi

case "$BACKUP_STORE" in
  s3://*)   drill=$(aws s3 ls --recursive "$BACKUP_STORE/drills/" | awk '{print $4}' | grep '\.ok$' | sort | tail -1 || true) ;;
  file://*) drill=$(cd "${BACKUP_STORE#file://}" 2>/dev/null && find drills -name '*.ok' | sort | tail -1 || true) ;;
esac
if [[ -z $drill ]]; then problems+=("no passing drill recorded in $BACKUP_STORE/drills")
else a=$(age_of "$drill"); d=$((a / 86400))
  ((a <= DRILL_MAX_AGE_D * 86400)) && ok "newest passing drill ${d}d old: $drill" || problems+=("newest PASSING drill is ${d}d old (limit ${DRILL_MAX_AGE_D}d): $drill")
fi

for t in $ARGON_TIMERS; do
  en=$(systemctl is-enabled "$t" 2>/dev/null || true); ac=$(systemctl is-active "$t" 2>/dev/null || true)
  [[ $en == enabled && $ac == active ]] && ok "$t enabled and active" || problems+=("$t is ${en:-missing}/${ac:-missing}, not enabled/active")
  svc=${t%.timer}.service
  [[ $(systemctl show -p Result --value "$svc" 2>/dev/null) =~ ^(success|)$ ]] || problems+=("$svc last run: $(systemctl show -p Result --value "$svc")")
done

if [[ $BACKUP_STORE == file://* ]]; then
  free=$(df -BG --output=avail "${BACKUP_STORE#file://}" | tail -1 | tr -dc 0-9)
  ((free >= BACKUP_MIN_FREE_GB)) && ok "backup disk: ${free}G free" || problems+=("backup disk has ${free}G free (minimum ${BACKUP_MIN_FREE_GB}G)")
fi

if ((${#problems[@]})); then
  printf '%s\n' "${problems[@]}" | sed 's/^/  !! /' >&2
  { echo "The backups are not known to be good. Until this is fixed, assume there is no usable backup."
    echo; printf -- '- %s\n' "${problems[@]}"
    echo; echo "Check:  systemctl list-timers 'argon-*' ; journalctl -u argon-backup -u argon-restore-drill --since -2d"
  } | bash "$DEPLOY_DIR/alert.sh" raise "backups are stale or failing (${#problems[@]} problem(s))" -
  exit 3
fi
ok "backups fresh and drills passing"
if [[ -n ${ALERT_HEARTBEAT_URL:-} ]]; then
  curl -fsS -m 20 -o /dev/null "$ALERT_HEARTBEAT_URL" && ok "heartbeat sent" \
    || { bash "$DEPLOY_DIR/alert.sh" raise "heartbeat could not be sent" </dev/null; exit 3; }
else
  info "no ALERT_HEARTBEAT_URL: if this server dies, nothing outside it will notice"
fi
