# deploy/

The same scripts run against local Docker and the EC2 host. Only the env file
differs: `deploy/env/local.env` or `/etc/argon/deploy.env`, selected with
`ARGON_ENV`.

| Script | Target | Purpose |
|---|---|---|
| `00-preflight.sh` | both | Read-only. Aborts if the host carries anything not on the allowlist. |
| `10-install-postgres.sh` | host | PG 16 from PGDG, loopback-only, scram. Asserts the binding. |
| `15-install-tools.sh` | host | Go (pinned + sha256), migrate CLI, AWS CLI. |
| `20-roles.sh` | both | argon_owner / argon_migrator / argon_app / argon_backup, the database. |
| `30-install-api.sh` | host | Build and install the API, `/etc/argon/argon.env`, systemd unit (not started). |
| `40-migrate.sh` | both | Migrate as argon_migrator, then `sql/grants.sql`. Host: backup first, `--confirm`. |
| `50-backup.sh` | both | pg_dump + same-snapshot manifest → `BACKUP_STORE`, read back and checksummed. |
| `60-restore.sh` | both | `--drill`: restore to scratch, compare manifest, run Tier 1 probe, drop. `--into DB --confirm`: real recovery. |
| `70-verify-tier1.sh` | both | Tries to break the Tier 1 constraints in a scratch DB; exit 1 if any holds only on paper. |
| `80-install-timers.sh` | host | Nightly backup and weekly drill timers. Refuses until a drill has passed. |

## Local

```bash
cp deploy/env/local.env.example deploy/env/local.env   # fill passwords
cp .env.example .env                                    # ARGON_APP_PASSWORD into DATABASE_URL
go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1
make up db-setup migrate-up verify-tier1 backup restore-drill
make run                                                 # :8081
```

Local Postgres is on **127.0.0.1:5433** (`PGPORT`). Port 5432 is left for any
native install. Client tools run inside the container, so their version always
matches the server.

## EC2 (Ubuntu 26.04, arm64)

Prerequisites, all yours to arrange:
1. An S3 bucket with versioning and Block Public Access on, and a lifecycle rule for old dumps.
2. An instance role with `iam/argon-backup-policy.json`. It has no Delete, so a compromised host cannot erase its own backups.
3. Security group: 22 from your IP only. Never 5432. 8080 stays closed until a proxy/TLS decision is made.

```bash
# on the instance, from a checkout of this repo
sudo install -d -m 0750 /etc/argon
sudo install -m 0600 deploy/env/ec2.env.example /etc/argon/deploy.env && sudo nano /etc/argon/deploy.env
export ARGON_ENV=/etc/argon/deploy.env
sudo -E bash deploy/00-preflight.sh
sudo -E bash deploy/10-install-postgres.sh
sudo -E bash deploy/15-install-tools.sh
sudo -E bash deploy/20-roles.sh
sudo -E bash deploy/30-install-api.sh
sudo -E bash deploy/40-migrate.sh up --confirm
sudo -E bash deploy/70-verify-tier1.sh
sudo -E bash deploy/50-backup.sh
sudo -E bash deploy/60-restore.sh --drill
sudo -E bash deploy/80-install-timers.sh
sudo systemctl start argon-api && curl -s 127.0.0.1:8080/healthz
```

From a laptop, the database is reached only through a tunnel:
`ssh -L 5433:127.0.0.1:5432 ubuntu@<host>`.

## Trust boundary

The ledger constraints bind **argon_app**, which is everything the API can do.
They do **not** fully bind **argon_owner**, and cannot. As owner, it can:
- `TRUNCATE` ledger tables (row triggers do not fire on TRUNCATE),
- `ALTER TABLE … DISABLE TRIGGER`, then UPDATE/DELETE posted rows,
- edit `signatory_approvals`, which has no immutability trigger.

The probe shows all three succeeding (G5–G7). This is accepted, not
overlooked: something has to be able to run migrations and restores. So the
boundary is **who can act as argon_owner**:
- only `argon_migrator` can, and only migrations (`40`) and restores (`60`) use it,
- its password lives in `/etc/argon/deploy.env` (root, 0600) and never in the API's environment,
- the API's `DATABASE_URL` is argon_app, and the probe's P1–P8 rows prove what that role cannot do,
- the postgres superuser is reachable only as the `postgres` OS user (peer auth), i.e. by root on the host.

Anyone with root on the host or the migrator password can rewrite the ledger.
Nothing in the schema changes that. Access control on the host and backups the
host cannot delete (`iam/`: no `s3:DeleteObject`) are what stand behind it.

## Known limits
- Nightly `pg_dump`, so RPO is up to 24 h. WAL archiving (pgBackRest/WAL-G) is the next step for a funds ledger.
- A failed timer shows in `systemctl --failed` and nowhere else until an alert channel is wired.
- The API binds `:PORT` on all interfaces (`cmd/api/main.go`). The security group is the only thing keeping 8080 private.
