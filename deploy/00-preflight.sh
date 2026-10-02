#!/usr/bin/env bash
# Read-only. Refuses to let provisioning proceed on a host that is carrying
# something it does not recognise. On a shared host a migration or a runaway
# process can take down a service holding customer funds, so the rule is an
# ALLOWLIST: anything listening or running that is not on it stops the run.
source "$(dirname "$0")/lib.sh"

fail=0
flag() { printf '\033[31m  !! %s\033[0m\n' "$*" >&2; fail=1; }

if [[ $TARGET == docker ]] && ! on_server; then
  info "docker target (a developer machine)"
  dc ps --status running --services | grep -qx postgres || die "compose postgres is not running (make up)"
  binding=$(dc port postgres 5432 || true)
  [[ $binding == "127.0.0.1:$PGPORT" ]] || flag "postgres published on '$binding' — must be 127.0.0.1:$PGPORT"
  v=$(pg_super -d postgres -At -c 'SHOW server_version_num' </dev/null)
  [[ $v == "$PG_MAJOR"* ]] || flag "server_version_num=$v, want $PG_MAJOR.x"
  ((fail == 0)) && ok "local docker postgres $v on $binding" || die "preflight failed"
  exit 0
fi

if [[ $TARGET == local ]]; then
  # A developer's own server: report what differs from EC2 rather than demand
  # it matches. It is someone else's server; this changes nothing on it.
  info "local target: an existing server on 127.0.0.1:$PGPORT"
  q() { pg_super -d postgres -At -c "$1" </dev/null; }
  v=$(q 'SHOW server_version_num') || die "cannot reach the superuser on 127.0.0.1:$PGPORT (POSTGRES_SUPERUSER / POSTGRES_PASSWORD in $ARGON_ENV)"
  info "server $(q 'SHOW server_version'), listen_addresses=$(q 'SHOW listen_addresses'), template1 encoding $(q "SELECT pg_encoding_to_char(encoding) FROM pg_database WHERE datname = 'template1'")"
  [[ $v == "$PG_MAJOR"* ]] || flag "server is $(q 'SHOW server_version'): every target runs PostgreSQL $PG_MAJOR, so results here would not speak for EC2"
  [[ $(q 'SHOW listen_addresses') =~ ^(localhost|127\.0\.0\.1)$ ]] ||
    printf '\033[33m  ?? this server listens on %s, not loopback only: reachable from the network if the firewall allows it\033[0m\n' "$(q 'SHOW listen_addresses')" >&2
  [[ $(q "SELECT count(*) FROM pg_available_extensions WHERE name = 'btree_gist'") == 1 ]] || flag "btree_gist is not available (0002 needs it)"
  # The drill's pg_dump/pg_restore come from PATH here, not from inside a
  # container: they must be the server's major version.
  for tool in psql pg_dump pg_restore; do
    tv=$("$tool" --version 2>/dev/null || true)   # no early-exit pipe under pipefail
    [[ $tv =~ ([0-9]+) ]] && tv=${BASH_REMATCH[1]} || tv=""
    [[ $tv == "${v:0:2}" ]] || flag "$tool on PATH is major ${tv:-missing}; the server is ${v:0:2}"
  done
  ours="'argon_owner','argon_migrator','argon_app','argon_backup','argon_auth'"
  mine=$(q "SELECT string_agg(rolname, ', ' ORDER BY rolname) FROM pg_roles WHERE rolname IN ($ours)")
  others=$(q "SELECT string_agg(rolname, ', ' ORDER BY rolname) FROM pg_roles WHERE rolname LIKE 'argon\_%' AND rolname NOT IN ($ours)")
  [[ -z $mine ]] && info "none of the five argon roles exist yet" || info "argon roles already present: $mine (20-roles.sh is idempotent and resets THESE passwords from $ARGON_ENV)"
  [[ -z $others ]] || info "other argon_* roles on this server, not ours and never touched: $others"
  ((fail == 0)) && ok "local postgres $v reachable as $POSTGRES_SUPERUSER" || die "preflight failed"
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
    # Docker's port forwarding for Argon's containers, and nginx: loopback only
    # (75-assert-loopback.sh asserts the same after every install).
    *'"docker-proxy"'*|*'"nginx"'*)
      [[ $addr =~ ^127\. || $addr == '[::1]' ]] && continue
      flag "${proc%%,*} listening on $local — must be loopback only"; continue ;;
  esac
  flag "unexpected listener $proto $local $proc"
done < <(sudo ss -Htulpn | awk '{print $1, $5, $7}')

# ---- services ----------------------------------------------------------------
info "running services"
allowed='^(acpid|chrony|containerd|cron|dbus|docker|getty@.*|serial-getty@.*|irqbalance|ModemManager|multipathd|networkd-dispatcher|polkit|rsyslog|snap\.amazon-ssm-agent\..*|snapd|ssh|systemd-.*|udisks2|unattended-upgrades|user@[0-9]+|postgresql@'"$PG_MAJOR"'-main|argon-api|nginx|fwupd|packagekit|apt-daily.*|update-notifier.*|check-new-release)\.service$'
while read -r unit _; do
  [[ $unit =~ $allowed ]] || flag "unrecognised running service: $unit"
done < <(systemctl list-units --type=service --state=running --no-legend --plain)

# Containers: Argon's own compose project ("argon") is expected; anyone else's is not.
if command -v docker >/dev/null; then
  others=$(sudo docker ps --format '{{.Names}} {{.Label "com.docker.compose.project"}}' | awk '$2 != "argon" {print $1}' | tr '\n' ' ')
  [[ -z ${others// /} ]] || flag "containers that are not Argon's are running: $others"
fi

# Postgres clusters that are not ours.
# On the server Postgres runs in a container (TARGET=docker): ANY native
# cluster is unexpected, a second Postgres to collide on 5432 and to patch.
if command -v pg_lsclusters >/dev/null && [[ $TARGET == docker ]] && [[ -n $(pg_lsclusters -h) ]]; then
  flag "a native postgres cluster exists, though the server runs Postgres in a container: $(pg_lsclusters -h | awk '{print $1"/"$2}' | tr '\n' ' ')"
elif command -v pg_lsclusters >/dev/null; then
  pg_lsclusters -h | while read -r ver name _; do
    [[ "$ver/$name" == "$PG_MAJOR/main" ]] || echo "$ver/$name"
  done | grep -q . && flag "unexpected postgres cluster(s): $(pg_lsclusters -h | awk '{print $1"/"$2}' | tr '\n' ' ')"
fi

((fail == 0)) || die "preflight found things this host was not expected to carry. Stop and find out what they are before provisioning."
ok "host carries nothing unexpected"
