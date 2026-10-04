#!/usr/bin/env bash
# THE SERVER. Raise, list and acknowledge alerts. Channel-agnostic: every alert
# is ALWAYS written to the journal (priority crit) and kept as an OPEN alert
# under /var/lib/argon/alerts/open, shown at every SSH login until someone
# acknowledges it. It is ALSO sent to ALERT_CHANNEL, once there is one:
#
#   none     (default) nobody is notified; the alert says so, in the alert
#   sns      aws sns publish to ALERT_SNS_TOPIC_ARN (email/SMS subscribers)
#   webhook  POST JSON to ALERT_WEBHOOK_URL (Slack, Teams, a pager)
#
# A send that fails is not silent: the alert is marked UNDELIVERED, in the
# journal and in its open file.
#
#   alert.sh raise SUBJECT [BODY_FILE|-]     (exit 0; the raise is the point)
#   alert.sh unit-failed UNIT                 (argon-alert@.service: OnFailure=)
#   alert.sh list
#   alert.sh ack ID|all
source "$(dirname "$0")/lib.sh"
: "${ALERT_CHANNEL:=none}"
: "${ALERT_DIR:=/var/lib/argon/alerts}"
host=$(hostname)

raise() {
  local subject=$1 body=${2:--}
  local id; id=$(date -u +%Y%m%dT%H%M%SZ)-$(tr -c 'a-z0-9' '-' <<<"${subject,,}" | cut -c1-40 | sed 's/-*$//')
  install -d -m 0750 "$ALERT_DIR/open" "$ALERT_DIR/acked"
  local f="$ALERT_DIR/open/$id"
  { echo "subject: $subject"; echo "host: $host"; echo "raised: $(date -u +%FT%TZ)"; echo "channel: $ALERT_CHANNEL"; echo
    if [[ $body == - ]]; then [[ -t 0 ]] || cat; else cat "$body"; fi; } > "$f"
  logger -p user.crit -t argon-alert "ALERT $id: $subject"

  local text; text="[argon $host] $subject"$'\n\n'"$(sed -n '6,60p' "$f")"
  local sent=0 why=""
  case $ALERT_CHANNEL in
    none)    why="no alert channel configured (ALERT_CHANNEL=none): NOBODY WAS NOTIFIED" ;;
    sns)     [[ -n ${ALERT_SNS_TOPIC_ARN:-} ]] || why="ALERT_SNS_TOPIC_ARN is empty"
             [[ -z $why ]] && { aws sns publish --topic-arn "$ALERT_SNS_TOPIC_ARN" --subject "[argon] ${subject:0:90}" \
                                  --message "$text" >/dev/null 2>"$f.err" && sent=1 || why="sns publish failed: $(head -c 300 "$f.err")"; } ;;
    webhook) [[ -n ${ALERT_WEBHOOK_URL:-} ]] || why="ALERT_WEBHOOK_URL is empty"
             [[ -z $why ]] && { python3 -c 'import json,sys; print(json.dumps({"text": sys.stdin.read()}))' <<<"$text" \
                                  | curl -fsS -m 20 -H 'Content-Type: application/json' --data-binary @- "$ALERT_WEBHOOK_URL" >/dev/null 2>"$f.err" \
                                  && sent=1 || why="webhook POST failed: $(head -c 300 "$f.err")"; } ;;
    *)       why="unknown ALERT_CHANNEL=$ALERT_CHANNEL" ;;
  esac
  rm -f "$f.err"
  if ((sent)); then
    echo "delivered: $ALERT_CHANNEL" >> "$f"
  else
    printf '\nUNDELIVERED: %s\n' "$why" >> "$f"
    logger -p user.crit -t argon-alert "ALERT $id UNDELIVERED: $why"
  fi
  printf '%s %s%s\n' "$id" "$subject" "$( ((sent)) || echo "  [UNDELIVERED: $why]")" >&2
}

case ${1:-} in
  raise) shift; [[ -n ${1:-} ]] || die "usage: alert.sh raise SUBJECT [BODY_FILE|-]"; raise "$@" ;;
  unit-failed)
         u=${2:?usage: alert.sh unit-failed UNIT}
         # Exit 3 means "failed, and raised its own alert" (65-check-freshness.sh).
         [[ ${MONITOR_EXIT_STATUS:-} == 3 ]] && { info "$u exited 3: it raised its own alert"; exit 0; }
         { echo "systemd unit $u FAILED (result ${MONITOR_SERVICE_RESULT:-?}, exit ${MONITOR_EXIT_STATUS:-?})."
           echo "If this is the backup or the drill: assume the newest backup is NOT known to be good."
           echo; echo "Last journal lines:"; journalctl -u "$u" -n 30 --no-pager -o cat 2>&1 | sed 's/\x1b\[[0-9;]*m//g'
         } | raise "$u failed" - ;;
  list)  for f in "$ALERT_DIR"/open/*; do [[ -f $f ]] || continue
           printf '%s  %s%s\n' "$(basename "$f")" "$(sed -n 's/^subject: //p' "$f")" "$(grep -q '^UNDELIVERED' "$f" && echo '  [UNDELIVERED]')"; done ;;
  ack)   [[ -n ${2:-} ]] || die "usage: alert.sh ack ID|all"; install -d -m 0750 "$ALERT_DIR/acked"
         if [[ $2 == all ]]; then n=0; for f in "$ALERT_DIR"/open/*; do [[ -f $f ]] && mv "$f" "$ALERT_DIR/acked/" && n=$((n+1)); done; ok "acknowledged $n"
         else [[ -f $ALERT_DIR/open/$2 ]] || die "no open alert $2"; mv "$ALERT_DIR/open/$2" "$ALERT_DIR/acked/"; ok "acknowledged $2"; fi
         logger -p user.notice -t argon-alert "acknowledged: $2 by ${SUDO_USER:-$USER}" ;;
  *)     die "usage: alert.sh raise|list|ack" ;;
esac
