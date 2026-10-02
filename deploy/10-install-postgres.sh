#!/usr/bin/env bash
# HOST ONLY. PostgreSQL $PG_MAJOR (18) from PGDG, bound to loopback,
# scram-sha-256 over TCP, peer for the postgres OS user. Idempotent.
#
# 18 is a REQUIREMENT, not whatever the distribution offers: local
# development runs 18, and one major version on both ends is what lets dumps
# move either way (an 18 pg_dump cannot restore into 16), keeps backup
# manifests comparable, and makes local test results speak for production.
# Ubuntu 26.04's own archive also carries postgresql-18; PGDG is pinned so
# minor releases follow PGDG's patches, and the source is asserted below.
# Fails — rather than warns — if 5432 ends up reachable from anywhere but
# loopback.
source "$(dirname "$0")/lib.sh"
[[ $TARGET == host ]] || die "10-install-postgres.sh is host-only (docker target uses docker-compose.yml)"
[[ $EUID -eq 0 ]] || die "run with sudo"

PGDG_FPR=B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8
KEY=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
CODENAME=$(. /etc/os-release; echo "$VERSION_CODENAME")

info "PGDG repository ($CODENAME-pgdg)"
apt-get update -q
apt-get install -y -q ca-certificates curl gnupg
install -d -m 0755 "$(dirname "$KEY")"
curl -fsSL -o "$KEY.tmp" https://www.postgresql.org/media/keys/ACCC4CF8.asc
# Never stop reading early in a pipe: under pipefail the writer's SIGPIPE (141)
# kills the script silently (found on the first EC2 run, at the source check).
got=$(gpg --show-keys --with-colons "$KEY.tmp" | awk -F: '/^fpr/ && !f {print $10; f=1}')
[[ $got == "$PGDG_FPR" ]] || { rm -f "$KEY.tmp"; die "PGDG key fingerprint $got != $PGDG_FPR"; }
mv "$KEY.tmp" "$KEY"
echo "deb [signed-by=$KEY] https://apt.postgresql.org/pub/repos/apt $CODENAME-pgdg main" \
  > /etc/apt/sources.list.d/pgdg.list
apt-get update -q

# Prefer PGDG over the distribution's build of the same packages.
cat > /etc/apt/preferences.d/pgdg.pref <<'EOF'
Package: *
Pin: release o=apt.postgresql.org
Pin-Priority: 900
EOF

info "installing postgresql-$PG_MAJOR"
DEBIAN_FRONTEND=noninteractive apt-get install -y -q "postgresql-$PG_MAJOR" "postgresql-client-$PG_MAJOR"
src=$(apt-cache policy "postgresql-$PG_MAJOR" | awk '/\*\*\*/ && !f {getline; print; f=1}')
[[ $src == *apt.postgresql.org* ]] || die "postgresql-$PG_MAJOR came from '$src', not PGDG"
pg_lsclusters -h | grep -q "^$PG_MAJOR main" || pg_createcluster "$PG_MAJOR" main --start

CONF=/etc/postgresql/$PG_MAJOR/main
info "configuring $CONF"
# Sized for a t4g.medium (3.7 GiB) shared with the API.
cat > "$CONF/conf.d/argon.conf" <<'EOF'
# Managed by deploy/10-install-postgres.sh — edits here are overwritten.
listen_addresses = 'localhost'
port = 5432
password_encryption = 'scram-sha-256'
max_connections = 50
shared_buffers = 1GB
effective_cache_size = 2560MB
work_mem = 16MB
maintenance_work_mem = 256MB
# Durability is not tuned away. These are the defaults, stated so nobody
# "optimises" them.
fsync = on
synchronous_commit = on
full_page_writes = on
wal_level = replica
log_min_duration_statement = 500ms
log_lock_waits = on
log_connections = on
log_disconnections = on
EOF

cat > "$CONF/pg_hba.conf" <<'EOF'
# Managed by deploy/10-install-postgres.sh — edits here are overwritten.
# TYPE  DATABASE  USER      ADDRESS        METHOD
local   all       postgres                 peer
local   all       all                      reject
host    all       all       127.0.0.1/32   scram-sha-256
host    all       all       ::1/128        scram-sha-256
# Nothing else. Remote access is an SSH tunnel to 127.0.0.1:5432.
EOF
chown postgres:postgres "$CONF/pg_hba.conf" "$CONF/conf.d/argon.conf"
chmod 0640 "$CONF/pg_hba.conf"

systemctl enable --now "postgresql@$PG_MAJOR-main"
systemctl restart "postgresql@$PG_MAJOR-main"
for _ in $(seq 20); do pg_isready -q -h 127.0.0.1 && break; sleep 1; done
pg_isready -h 127.0.0.1 || die "postgres did not come up"

info "asserting 5432 is loopback-only"
bad=$(ss -Htln "sport = :5432" | awk '{print $4}' | grep -vE '^(127\.0\.0\.1|\[::1\]):5432$' || true)
[[ -z $bad ]] || die "postgres is listening on: $bad"
ss -Htln "sport = :5432" | awk '{print "    " $4}' >&2
v=$(sudo -u postgres psql -XAtc 'SHOW server_version')
[[ $v == "$PG_MAJOR".* ]] || die "the running server is $v, not PostgreSQL $PG_MAJOR"
ok "postgres $v on loopback only"

# Optional: 2 GiB swap, so a memory spike is slow rather than an OOM kill of
# postgres mid-commit. Opt in with ARGON_SWAP=1.
if [[ ${ARGON_SWAP:-0} == 1 && ! -f /swapfile ]]; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  sysctl -w vm.swappiness=10 >/dev/null
  ok "2 GiB swap enabled"
fi
