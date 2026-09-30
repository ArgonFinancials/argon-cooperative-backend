#!/usr/bin/env bash
# Read-only. Refuses to let provisioning proceed on a host that is carrying
# something it does not recognise. On a shared host a migration or a runaway
# process can take down a service holding customer funds, so the rule is an
# ALLOWLIST: anything listening or running that is not on it stops the run.
source "$(dirname "$0")/lib.sh"

fail=0
flag() { printf '\033[31m  !! %s\033[0m\n' "$*" >&2; fail=1; }

if [[ $TARGET == docker ]]; then
  info "docker target"
  dc ps --status running --services | grep -qx postgres || die "compose postgres is not running (make up)"
  binding=$(dc port postgres 5432 || true)
  [[ $binding == "127.0.0.1:$PGPORT" ]] || flag "postgres published on '$binding' — must be 127.0.0.1:$PGPORT"
  v=$(pg_super -d postgres -At -c 'SHOW server_version_num' </dev/null)
  [[ $v == 16* ]] || flag "server_version_num=$v, want 16.x"
  ((fail == 0)) && ok "local docker postgres $v on $binding" || die "preflight failed"
  exit 0
fi

info "host: $(. /etc/os-release; echo "$PRETTY_NAME") $(dpkg --print-architecture), $(nproc) cpu, $(free -h | awk '/Mem:/{print $2}') ram"
df -h / | tail -1 | awk '{print "    disk /: "$4" free of "$2}' >&2

# ---- listeners ---------------------------------------------------------------
# Allowed: sshd, systemd-resolved, chronyd, DHCP, containerd on loopback, and
# — once we have installed them — postgres on loopback and argon-api.
info "listening sockets"
while read -r proto local proc; do
  port=${local##*:}; addr=${local%:*}
  case "$proc" in
    *'"sshd"'*|*'"systemd"'*|*'"systemd-resolve"'*|*'"chronyd"'*|*'"systemd-network"'*) continue ;;
    *'"containerd"'*) [[ $addr == 127.0.0.1 ]] && continue ;;
    *'"postgres"'*)
      if [[ $port == "$PGPORT" && ( $addr == 127.0.0.1 || $addr == '[::1]' ) ]]; then continue; fi
      flag "postgres listening on $local — must be loopback only" ; continue ;;
    *'"argon-api"'*) continue ;;
  esac
  flag "unexpected listener $proto $local $proc"
done < <(sudo ss -Htulpn | awk '{print $1, $5, $7}')

# ---- services ----------------------------------------------------------------
info "running services"
allowed='^(acpid|chrony|containerd|cron|dbus|docker|getty@.*|serial-getty@.*|irqbalance|ModemManager|multipathd|networkd-dispatcher|polkit|rsyslog|snap\.amazon-ssm-agent\..*|snapd|ssh|systemd-.*|udisks2|unattended-upgrades|user@[0-9]+|postgresql@16-main|argon-api|fwupd|packagekit|apt-daily.*|update-notifier.*|check-new-release)\.service$'
while read -r unit _; do
  [[ $unit =~ $allowed ]] || flag "unrecognised running service: $unit"
done < <(systemctl list-units --type=service --state=running --no-legend --plain)

if command -v docker >/dev/null && sudo docker ps -q | grep -q .; then
  flag "docker containers are running: $(sudo docker ps --format '{{.Names}}' | tr '\n' ' ')"
fi

# Postgres clusters that are not ours.
if command -v pg_lsclusters >/dev/null; then
  pg_lsclusters -h | while read -r ver name _; do
    [[ "$ver/$name" == 16/main ]] || echo "$ver/$name"
  done | grep -q . && flag "unexpected postgres cluster(s): $(pg_lsclusters -h | awk '{print $1"/"$2}' | tr '\n' ' ')"
fi

((fail == 0)) || die "preflight found things this host was not expected to carry. Stop and find out what they are before provisioning."
ok "host carries nothing unexpected"
