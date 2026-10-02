# deploy/

The same scripts run against every target. Only the env file differs:
`deploy/env/local.env` or `/etc/argon/deploy.env`, selected with `ARGON_ENV`.
`TARGET` says where Postgres is: `local` (a server already running on this
machine), `docker` (the compose container), or `host` (EC2).

| Script | Target | Purpose |
|---|---|---|
| `00-preflight.sh` | all | Read-only. Aborts if the host carries anything not on the allowlist. |
| `10-install-postgres.sh` | host | PostgreSQL 18 from PGDG (asserted: major and source), loopback-only, scram. Asserts the binding. |
| `15-install-tools.sh` | host | Go (pinned + sha256), migrate CLI, AWS CLI. |
| `20-roles.sh` | all | argon_owner / argon_migrator / argon_app / argon_backup / argon_auth, the database. |
| `30-install-api.sh` | host | Build and install the API, `/etc/argon/argon.env`, systemd unit (not started). |
| `40-migrate.sh` | all | Migrate as argon_migrator, then `sql/grants.sql`. Host: backup first, `--confirm`. |
| `50-backup.sh` | all | pg_dump + same-snapshot manifest → `BACKUP_STORE`, read back and checksummed. |
| `60-restore.sh` | all | `--drill`: restore to scratch, compare manifest, run Tier 1 probe, drop. `--into DB --confirm`: real recovery. |
| `70-verify-tier1.sh` | all | Tries to break the Tier 1 constraints in a scratch DB; exit 1 if any holds only on paper. |
| `80-install-timers.sh` | host | Nightly backup and weekly drill timers. Refuses until a drill has passed. |

## Local

Against a PostgreSQL already installed on this machine (`TARGET=local`):

```bash
cp deploy/env/local.env.example deploy/env/local.env   # generate the argon_* passwords;
                                                        # POSTGRES_PASSWORD = that server's superuser
go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1
export ARGON_ENV=deploy/env/local.env
bash deploy/00-preflight.sh     # reports version, encoding, listen address, existing argon_* roles
bash deploy/20-roles.sh         # creates the argon_* roles and ONE database: ARGON_DB=argon_test
bash deploy/40-migrate.sh up
bash deploy/70-verify-tier1.sh
bash deploy/50-backup.sh && bash deploy/60-restore.sh --drill
```

What this touches on that server: five roles (`argon_owner`, `argon_migrator`,
`argon_app`, `argon_backup`, `argon_auth`, cluster-wide, so visible to its other
databases), the `argon_test` database, and short-lived scratch databases
(`argon_verify_*`, `argon_drill_*`) that are dropped after use. Nothing else.
Client tools are that server's own, so their version matches it. Preflight
refuses a server that is not PostgreSQL 18: see "One major version" below.

`TARGET=docker` still works with `docker-compose.yml` (`PGPORT=5433`, the
container's own superuser password, `make up`).

## EC2 (Ubuntu 26.04, arm64)

Prerequisites, all yours to arrange:
1. An S3 bucket with versioning and Block Public Access on, and a lifecycle rule for old dumps.
2. An instance role with `iam/argon-backup-policy.json`. It has no Delete, so a compromised host cannot erase its own backups.
3. Security group: 22 from your IP only. Never 5432. 8080 stays closed until a proxy/TLS decision is made.

```bash
# on the instance, from a checkout of this repo
sudo install -d -m 0750 /etc/argon
sudo install -m 0600 deploy/env/ec2.env.example /etc/argon/deploy.env
# Generate the secrets ON THE INSTANCE, straight into the file (never typed, never pasted):
for v in ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_BACKUP_PASSWORD ARGON_AUTH_PASSWORD; do
  sudo sed -i "s|^$v=.*|$v=$(openssl rand -base64 24 | tr -d '/+=')|" /etc/argon/deploy.env
done
sudo sed -i "s|^ARGON_CODE_KEY=.*|ARGON_CODE_KEY=$(openssl rand -hex 32)|" /etc/argon/deploy.env
# Before Part B (the S3 bucket) exists, back up to a local directory instead
# (see "Stated gap" below: that is NOT a backup):
#   sudo sed -i 's|^BACKUP_STORE=.*|BACKUP_STORE=file:///var/backups/argon|' /etc/argon/deploy.env
sudo nano /etc/argon/deploy.env          # check the rest: ARGON_DB, PGPORT, API_PORT, BACKUP_STORE
# Ubuntu 26.04's sudo ignores -E ("preserving the entire environment is not
# supported"), so ARGON_ENV is passed on each sudo line, not exported.
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/00-preflight.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/10-install-postgres.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/15-install-tools.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/20-roles.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/30-install-api.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/40-migrate.sh up --confirm
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/70-verify-tier1.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/50-backup.sh
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/60-restore.sh --drill
sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/80-install-timers.sh
sudo systemctl start argon-api && curl -s 127.0.0.1:8080/healthz
```

From a laptop, the database is reached only through a tunnel:
`ssh -L 5433:127.0.0.1:5432 ubuntu@<host>`.

`/etc/argon/deploy.env` carries four role passwords and one key, all generated
on the instance: migrator, app, backup, **`ARGON_AUTH_PASSWORD`** (the auth
service's role, 0005) and **`ARGON_CODE_KEY`** (the HMAC key for sign-in code
digests). None is an AWS resource; nothing in `AWS_REQUEST.md` changes.
`30-install-api.sh` writes the API's own `/etc/argon/argon.env` from them: the
app's connection as argon_app, the auth service's as argon_auth, and the key.

**Stated gap until the S3 bucket exists: backups are on the same disk as the
database.** With `BACKUP_STORE=file:///var/backups/argon`, every dump lands on
the instance's own volume. Losing the instance, or its volume, loses the
database and every backup at once. A passing drill then proves only that a
dump restores and still enforces its rules; it does **not** mean the data is
safe anywhere else. This is correct for now and is not a backup. It closes when
Part B (`AWS_REQUEST.md`) gives `BACKUP_STORE=s3://…`.

**These host scripts have never run anywhere.** Their first run is their first
test: report each failure as it comes (the step, the command, the output),
rather than fixing it in place, because what fails says more about the scripts
than what passes.

## One major version: 18, everywhere

Local, docker and EC2 all run PostgreSQL 18 (`PG_MAJOR` in `lib.sh`;
preflight and `10-install-postgres.sh` refuse anything else). One major
version removes a whole class of differences: an 18 `pg_dump` cannot restore
into 16, backup manifests from different majors do not match (18 lists NOT
NULL constraints in `pg_constraint`; 16 does not), and error wording and
trigger behaviour that cannot be verified by reading are then the same
behaviour. Minor versions may differ (local 18.4, PGDG 18.6 on EC2).

**Known difference, not a proven equivalence: collation.** A Windows server
sorts text by Windows locale rules; EC2 by Linux (glibc/ICU) rules. Nothing in
the schema or the tests *appears to* depend on text sort order: ordering is
by time, id or number, and uniqueness is exact. "Appears to" is the accurate
claim; no test establishes it. A result that depends on how two strings sort
should be checked on EC2, not trusted from a Windows run.

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

## Incident: migrate says "dirty"

**The version migrate records after a failure is where it was going, not where
the schema is.** migrate writes the target version, marked dirty, *before* it
runs the file:

| What failed | migrate records | The schema is actually at |
|---|---|---|
| `up` to N (e.g. 0004's guard: accounts already exist) | N, dirty | N−1 |
| `down` from N (e.g. 0004's down: ledger lines exist) | N−1, dirty | N |

Every migration here is one transaction (`BEGIN…COMMIT`), so a failed one has
changed nothing. Recovery is always to force the version the schema was at
**before the command**. `40-migrate.sh` prints that number when a run fails:

```bash
migrate -path migrations -database "$URL" force <version before the command>
```

Never force to the number migrate recorded. After a refused down, that would
tell the next `up` that 0004 is not applied, and it would try to add a column
that already exists. If in any doubt, check the schema, not the tool. For
example, 0004 is applied if `accounts.role` exists:
`SELECT 1 FROM information_schema.columns WHERE table_name='accounts' AND column_name='role';`

Both rows were reproduced on a scratch database on 2026-10-01.

## Known limits
- Nightly `pg_dump`, so RPO is up to 24 h. WAL archiving (pgBackRest/WAL-G) is the next step for a funds ledger.
- A failed timer shows in `systemctl --failed` and nowhere else until an alert channel is wired.
- The API binds `:PORT` on all interfaces (`cmd/api/main.go`). The security group is the only thing keeping 8080 private.
