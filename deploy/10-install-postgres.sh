#!/usr/bin/env bash
# HOST ONLY. PostgreSQL 16 from PGDG (Ubuntu 26.04 ships only 18), bound to
# loopback, scram-sha-256 over TCP, peer for the postgres OS user. Idempotent.
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
got=$(gpg --show-keys --with-colons "$KEY.tmp" | awk -F: '/^fpr/{print $10; exit}')
[[ $got == "$PGDG_FPR" ]] || { rm -f "$KEY.tmp"; die "PGDG key fingerprint $got != $PGDG_FPR"; }
mv "$KEY.tmp" "$KEY"
echo "deb [signed-by=$KEY] https://apt.postgresql.org/pub/repos/apt $CODENAME-pgdg main" \
  > /etc/apt/sources.list.d/pgdg.list
apt-get update -q

info "installing postgresql-16"
DEBIAN_FRONTEND=noninteractive apt-get install -y -q postgresql-16 postgresql-client-16
pg_lsclusters -h | grep -q '^16 main' || pg_createcluster 16 main --start

CONF=/etc/postgresql/16/main
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

systemctl enable --now postgresql@16-main
systemctl restart postgresql@16-main
for _ in $(seq 20); do pg_isready -q -h 127.0.0.1 && break; sleep 1; done
pg_isready -h 127.0.0.1 || die "postgres did not come up"

info "asserting 5432 is loopback-only"
bad=$(ss -Htln "sport = :5432" | awk '{print $4}' | grep -vE '^(127\.0\.0\.1|\[::1\]):5432$' || true)
[[ -z $bad ]] || die "postgres is listening on: $bad"
ss -Htln "sport = :5432" | awk '{print "    " $4}' >&2
v=$(sudo -u postgres psql -XAtc 'SHOW server_version')
ok "postgres $v on loopback only"

# Optional: 2 GiB swap, so a memory spike is slow rather than an OOM kill of
# postgres mid-commit. Opt in with ARGON_SWAP=1.
if [[ ${ARGON_SWAP:-0} == 1 && ! -f /swapfile ]]; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  sysctl -w vm.swappiness=10 >/dev/null
  ok "2 GiB swap enabled"
fi
