# deploy/

The same scripts run against every target. Only the env file differs:
`deploy/env/local.env` or `/etc/argon/deploy.env`, selected with `ARGON_ENV`.
`TARGET` says where Postgres is: `local` (a server already running on this
machine), `docker` (a compose container: a developer machine, or the EC2
server with `ARGON_HOST=1`), or `host` (a native server install).

| Script | Target | Purpose |
|---|---|---|
| `00-preflight.sh` | all | Read-only. Aborts if the host carries anything not on the allowlist. |
| `05-install-release.sh` | server | The checkout becomes the release at `/opt/argon/src`, which scripts, timers and the image build run from. |
| `10-start-postgres.sh` | server | The postgres:18 (Debian) container on Argon's own network, published on loopback. Asserts major and loopback. |
| `15-install-tools.sh` | server | Go (pinned + sha256) for the drill's test binary, the migrate CLI; the AWS CLI once backups go to S3. |
| `20-roles.sh` | all | argon_owner / argon_migrator / argon_app / argon_backup / argon_auth, the database. |
| `25-deploy-user.sh` | server | The CI user argon-deploy: one key, one forced command (`argon-deploy`), no docker group. |
| `argon-deploy` | server | The forced command: a release on stdin → 05, 30, 40, 70, 75. Never starts a stopped API. |
| `30-install-api.sh` | server | The API image, the drill's test binary, the container's env file. Does not start the API. |
| `40-migrate.sh` | all | Migrate as argon_migrator, then `sql/grants.sql`. Host: backup first, `--confirm`. |
| `50-backup.sh` | all | pg_dump + same-snapshot manifest → `BACKUP_STORE`, read back and checksummed. |
| `60-restore.sh` | all | `--drill`: restore to scratch, compare manifest, run Tier 1 probe, drop. `--into DB --confirm`: real recovery. |
| `70-verify-tier1.sh` | all | Tries to break the Tier 1 constraints in a scratch DB; exit 1 if any holds only on paper. |
| `65-check-freshness.sh` | server | Newest dump, newest passing drill, timers enabled: raises an alert if any is stale. Daily via `argon-freshness.timer`. |
| `alert.sh` | server | Raise / list / acknowledge alerts: journal, SSH login banner, and `ALERT_CHANNEL`. |
| `80-install-timers.sh` | host | Backup, drill and freshness timers, the alert unit and login banner. Refuses until a drill has passed. |
| `75-assert-loopback.sh` | server | Every listener and every published container port on loopback, except `PUBLIC_PORTS`. Run after anything that can open a port. |
| `85-install-nginx.sh` | server | Nginx on loopback only: healthz proxied, everything else refused until TLS exists. |

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

## EC2 (Ubuntu 26.04, arm64): containers

PostgreSQL 18 (Debian image) and the API run as containers on Argon's own
Docker network (`deploy/compose/docker-compose.prod.yml`). Nginx runs on the
host. **Everything listens on loopback only**, except sshd; Docker's published
ports bypass ufw, so `75-assert-loopback.sh` checks this by script after every
step that can open a port. Public exposure waits for a domain and TLS.

```bash
# 1. The code, on the instance (CI does this later): a checkout at ~/argon-src.
# 2. The secrets, generated ON THE INSTANCE straight into the file (never typed or pasted):
sudo install -d -m 0750 /etc/argon
sudo install -m 0600 ~/argon-src/deploy/env/ec2.env.example /etc/argon/deploy.env
for v in POSTGRES_PASSWORD ARGON_MIGRATOR_PASSWORD ARGON_APP_PASSWORD ARGON_BACKUP_PASSWORD ARGON_AUTH_PASSWORD; do
  sudo sed -i "s|^$v=.*|$v=$(openssl rand -base64 24 | tr -d '/+=')|" /etc/argon/deploy.env
done
sudo sed -i "s|^ARGON_CODE_KEY=.*|ARGON_CODE_KEY=$(openssl rand -hex 32)|" /etc/argon/deploy.env

# 3. Ubuntu 26.04's sudo ignores -E, so ARGON_ENV goes on each sudo line.
E=ARGON_ENV=/etc/argon/deploy.env
cd ~/argon-src
sudo $E bash deploy/00-preflight.sh          # read-only: nothing unexpected on this host
sudo $E bash deploy/05-install-release.sh    # the checkout becomes the release at /opt/argon/src
cd /opt/argon/src                            # every later step runs from the release
sudo $E bash deploy/15-install-tools.sh      # Go (the drill's test binary) and the migrate CLI
sudo $E bash deploy/10-start-postgres.sh     # the postgres container; asserts loopback
sudo $E bash deploy/20-roles.sh              # roles and the argon database
sudo $E bash deploy/30-install-api.sh        # API image, drill binary, /etc/argon/argon.env; NOT started
sudo $E bash deploy/40-migrate.sh up --confirm
sudo $E bash deploy/70-verify-tier1.sh       # the probe, in a scratch database
sudo $E bash deploy/50-backup.sh
sudo $E bash deploy/60-restore.sh --drill    # restore, manifest, probe and every suite on the copy
sudo $E bash deploy/80-install-timers.sh     # refuses until a drill has passed
sudo $E bash deploy/85-install-nginx.sh      # loopback only; asserts loopback
sudo $E bash deploy/75-assert-loopback.sh

# 4. DELIBERATELY, after the drill result has been read: start the API.
#    ARGON_RELEASE names the image 30 built and the probe ran against. Without it
#    compose looks for argon-api:local and BUILDS a new, untested image.
#    --no-build makes that a refusal instead.
cd /opt/argon/src
sudo ARGON_RELEASE=$(cat RELEASE) docker compose --env-file /etc/argon/deploy.env -f deploy/compose/docker-compose.prod.yml --profile api up -d --no-build api
curl -s -w '%{http_code}\n' 127.0.0.1:8080/healthz; curl -s -w '%{http_code}\n' 127.0.0.1/healthz   # direct, and through nginx
# healthz pings Postgres as argon_app: 200 through nginx is nginx -> API -> Postgres; 503 if the database is unreachable.
```

From a laptop, everything is reached through an SSH tunnel, e.g.
`ssh -L 8080:127.0.0.1:8080 ubuntu@<host>`.

**Never run `docker compose down -v` or a volume prune on this host.** The
database lives in the named volume `argon-pgdata`, and its only other copy is
the backups.

`/etc/argon/deploy.env` carries the container's superuser password, four role
passwords and the code key, all generated on the instance; `30-install-api.sh`
writes the API container's `/etc/argon/argon.env` from them (the handlers as
argon_app, the auth service as argon_auth, both to the `postgres` service).
None is an AWS resource; nothing in `AWS_REQUEST.md` changes.

**Stated gap until the S3 bucket exists: backups are on the same disk as the
database.** With `BACKUP_STORE=file:///var/backups/argon`, every dump lands on
the instance's own volume. Losing the instance, or its volume, loses the
database and every backup at once. A passing drill then proves only that a
dump restores and still enforces its rules; it does **not** mean the data is
safe anywhere else. This is correct for now and is not a backup. It closes when
Part B (`AWS_REQUEST.md`) gives `BACKUP_STORE=s3://…`.

**These host scripts are on their first runs.** Report each failure as it comes
(the step, the command, the output), rather than fixing it in place, because
what fails says more about the scripts than what passes.

### Findings from the first runs (2026-10-02/03)

| # | What failed | Lesson |
|---|---|---|
| 1 | `sudo -E` is ignored on Ubuntu 26.04; scripts ran without `ARGON_ENV` | Pass variables on the sudo line itself. |
| 2 | Exit 141: `apt-cache policy \| awk '…exit'` under pipefail (SIGPIPE) | No early-exit readers in pipes under pipefail. |
| 3 | 15 exited 1 on a trailing `command -v aws &&`; migrate's "dev" version proved nothing | A false last test is the script's exit status; assert versions from the build info. |
| 4 | **My `apt-get autoremove` during the Postgres purge also removed `pollinate`**, an Ubuntu image default (boot-time RNG seeding), with six packages that had only come with native Postgres | **A cleanup removes more than it meant to.** On a host where we answer for not breaking anything else, read what autoremove will take before it takes it, and check the apt history afterwards. pollinate was reinstalled; the other six were ours and stay gone. |
| 5 | `docker compose` missing ("unknown flag: --env-file") | Install and assert every tool a script calls. |
| 6 | The forced command arrived with CRLF (`env: $'bash\r'`): `git archive` applies `core.autocrlf` and only `*.sh` was pinned | Pin every file the server reads, extension or not; 05 now refuses a release with CRLF. |
| 7 | **The first CI run failed all nine suites with exit 1 and NO OUTPUT.** `BIN="…$( [[ $OSTYPE == msys* ]] && echo .exe )"`: on Linux the test is false, the substitution returns 1, the assignment takes that status, and `set -e` exits silently. Invisible until then because every earlier run was on Windows. | **The silence is the dangerous part.** Never assign from `$( test && echo … )` under `set -e`; use `if`. A check that has only ever run on one OS has not run. |
| 8 | The runbook's "start the API" line omitted `ARGON_RELEASE`: compose would have built and run an untested `argon-api:local`. Caught before it ran. | Start the image that was tested, by name, with `--no-build`. The pinned release, the tested image and the CI gate are all defeated by one missing argument in a README: the documented command is part of the system. |
| 9 | The production environment's required-reviewer rule did not exist: the org is on GitHub Free and the repo is private, where that rule needs Enterprise. Caught by reading the rule back through the API before setting the deploy secrets. | A control you configured is not a control until it is read back from the system that enforces it. |
| 10 | A ledger line could post to ANOTHER society's account; both trial balances broke. Not infrastructure: recorded in `test/tier1/README.md` ("Finding 10"), closed by 0006. Found by writing the trial-balance read, not by testing the write path. | A read that must sum to zero is a check on every rule that produced the data. |
| 11 | Adding the tenth and eleventh suites to the drill: `run_tier1: more than 9 suites against one database would overlap fixture ranges`. The fixture-counter ranges were 10^7 apart under an 8-digit phone, so only 9 fit. Refused loudly, as designed. | Ranges are now 10^6 apart (99 fit), and the assumption behind that is asserted: a test process that uses more than its range FAILS (shown by shrinking the range to 10: "used 30 fixture numbers, more than its range of 10"). run_tier1 now prints why a binary exited non-zero with no failing test; before, the drill would have said only "FAILED". |
| 12 | An approved test (statement 13) passed on either date basis, so its approval held no decision. Recorded in `test/tier1/README.md` ("Finding 12"). | An approval does not make a test discriminating: ask what it would do had the decision gone the other way. |
| 13 | A deploy was reported as through, twice, when it had not run. Both times caught by reading the system (the run list, the server's `RELEASE`, the deploy user's last login), not by taking the report. | The same rule as finding 9, applied to people as well as settings: a deploy is done when the run says so and the server's `RELEASE` names the commit, not when someone remembers starting it. Confirm from the run. |
| 18 | My deploy watcher reported a run that was not new: it treated any manual run whose id differed from the last one it had seen as the new deploy, and the run listing briefly returned yesterday's refused gate test. The server (RELEASE, schema) showed nothing had happened. | Finding 13 applied to the instrument: "different from the last one" is not "new". A watcher accepts only a run created after it started, with a higher id, and still confirms on the server. |
| 22 | The frontend had NO pinned copy of the API spec, not a stale one. A 2026-10-02 note said "re-vendor", and both sides believed a pinned copy existed because the instruction to put it there had been given. Found 2026-10-07 when handing over the spec. | Same family as finding 9 (a rule clicked and never saved): a control assumed to exist because someone was told to create it. Read it back from where it should be. The copy is now in the frontend repo, byte-identical (sha256 `043bf89c…`). |
| 23 | The workstation ran out of memory (38 MB of virtual memory free) and a `git push` hung on the credential manager with no output. Only my own push processes were stopped; the editor was restarted by its owner, and the push succeeded on retry. | A death with no output looks like waiting. Check free memory before a long run, and give a hung command a deadline instead of a longer wait. |
| 24 | The auth service's list of act kinds (`internal/auth` `ActKinds`) lagged the schema: 0008 added `guarantee` and `guarantee_withdrawal`, and the service could mint no proof for either. Found writing the approvals API, which needs `approval_signature` and `approval_rejection`. | A list maintained in two places drifted, and drifted SILENTLY: the guarantee flow would have refused every real attempt while nothing in the suites noticed, because the suites mint proofs in SQL, not through the service. A finding is a class: every enum the schema owns has copies (the service, the OpenAPI spec, the frontend). When a migration adds a value, read every copy back. |
| 25 | **Finding 16 recurring**: the finding is the recurrence, not the grant. Moving the fixtures onto step-up acts, the Go tests passed on the act path while the probe, which runs as `argon_app`, was refused: `permission denied for table signatory_approvals`. The app may insert only the columns grants.sql lists, and `act_id` is not among them (it cannot be until 0010 adds the column). Under a mutant that read `SELECT *` from `credential_acts`, it was refused again, on `new_pin_hash`, which argon_app is deliberately barred from reading. | Building 0010 must (a) add `act_id` to argon_app's INSERT lists on `signatory_approvals` and `approval_rejections`, and (b) make its rule functions SECURITY DEFINER or name their columns. Otherwise every real signature is refused in production while every test passes. The probe is the test that sees this; its act path was proved with a temporary grant (fixture, C5, C5b, K5 as expected). The habit recorded after finding 16 did not hold, so it is now a STEP: `mutate.sh` runs the probe as argon_app after GREEN for every migration suite, and fails the run if it fails (proved with a removed grant: 18/18 GREEN, run FAILED). |
| 26 | Tracing the probe with `bash -x` printed the local `ARGON_AUTH_PASSWORD` into a session transcript: `require_env` tests `[[ -n "${!v}" ]]`, and the trace shows the expanded value. Local development database only. | The trace was the right diagnostic move; the exposure came from tracing WITH THE ENVIRONMENT LOADED. That is the specific trap: never trace a script that has loaded an env file. Use the script's own error output, or capture stderr of the failing statement. The local password was rotated 2026-10-07 (new accepted, old refused, neither printed). |
| 27 | Moving the fixtures onto step-up acts changed what TestSignStep6 sent: `w.reject` now supplies the rejecter's act, so "a rejection with no step-up" carried one. The mutation run still reported the test red on its mutant, because red means "the refusal did not come", and an accepted rejection is red for any implementation, the correct one included. Only GREEN, which could not run until 0010 existed, showed it (11/12). | A refusal test whose fixture satisfies the rule is red on every mutant and looks proved. When a fixture helper changes, re-read every refusal test that goes through it and ask what it now sends. Test 6 now writes its rejection out with no act, as test 1 does. |
| 28 | The step added for finding 25 failed in CI on every migration suite, and said nothing. The runner had no `migrate` CLI (the suites apply SQL directly; the probe builds its database with migrate), and mutate.sh showed only the probe lines matching its filter. The probe's own death message matched none of them. My local proof had been run where the tool exists. | Finding 23's class, in the instrument I had just built: a failure with no reason shown. mutate.sh now always shows the probe's last lines on a failure, proved by making migrate unavailable (it shows "migrate not found"). CI installs the server's pinned migrate version and reads the version back from the binary. |

**Habit: a control is not a control until you have read it back from the
system that enforces it.** The click succeeding is not the control existing.
Finding 9: a required-reviewer rule that GitHub's page accepted and never saved,
caught one step before every push to main would have deployed unapproved.
This applies beyond GitHub: grants (read `pg_roles` and `has_table_privilege`,
not the script that ran), listeners (`ss`, not the config), timers
(`systemctl is-enabled`, not the install log).

**Habit: a finding is a class, not a line.** After fixing the instance, find
the rest: name the shape, search every script for it, and prove the search
finds a planted example before trusting its silence. Finding 7's shape
(an assignment from a failing substitution under `set -e`) turned up twice
more that way: `65-check-freshness.sh`, `80-install-timers.sh`.

## Alerts

A nightly backup that silently stops working is a backup nobody has, and a
drill that passes weekly is worth nothing if nobody reads the result. So alerts
are designed around the four ways that happens, and the channel is a setting
(`ALERT_CHANNEL` in `/etc/argon/deploy.env`) that can come later without
changing any of this:

| Failure | Caught by |
|---|---|
| A job runs and fails (backup, drill) | `OnFailure=argon-alert@%n.service` on each unit → `alert.sh unit-failed`, with the unit's last journal lines |
| A job stops running at all (timer disabled, unit deleted, clock wrong): nothing *fails* | `argon-freshness.timer` (daily 07:00 UTC and after boot) → `65-check-freshness.sh` reads the RESULTS: newest dump ≤ 26 h, newest passing drill ≤ 8 d, both timers enabled, neither last run failed, backup disk ≥ 5 GB free |
| The server itself dies: nothing on it can say so | the freshness check pings `ALERT_HEARTBEAT_URL` only when all is fresh; an outside dead-man's switch (healthchecks.io, or a CloudWatch alarm on missing data) alerts when the pings stop |
| An alert is sent and nobody reads it, or the channel is broken | every alert is ALSO kept open in `/var/lib/argon/alerts/open` and printed at every SSH login until acknowledged; a failed send is marked **UNDELIVERED** in the journal and the alert |

Every alert, whatever the channel, is logged at priority `crit`
(`journalctl -t argon-alert`). With `ALERT_CHANNEL=none` (today) the alert
itself says **NOBODY WAS NOTIFIED**, and 80 warns at install.

```bash
sudo $E bash deploy/alert.sh list          # open alerts
sudo $E bash deploy/alert.sh ack all       # after reading them; logged with who acknowledged
```

**Decided: Slack** (`ALERT_CHANNEL=webhook`), because it needs no AWS and the
team is already there. An alert that lands where nobody looks is worse than
none: it reads as covered. The options were:
- `sns`: an SNS topic with email/SMS subscribers. Needs `sns:Publish` on that topic in the instance role, the same IAM request as the S3 bucket.
- `webhook`: a Slack/Teams incoming webhook URL. No AWS needed.
- `ALERT_HEARTBEAT_URL`: needed with either one. Without it, a dead server is noticed only when someone looks.

## CI/CD (`.github/workflows/deploy.yml`)

**tier1** runs on every push and pull request: for each of the fifteen suites,
`mutate.sh <suite> --svc` against `postgres:18-trixie` in a service container
on the runner (TARGET=local, passwords generated per run). A suite passes only
if its tests pass on the real migrations AND turn red on every deliberately
broken schema and service.

**deploy never runs on a push.** A person deploys: GitHub → Actions →
tier1-and-deploy → **Run workflow** on `main`, typing the first 7+ characters
of the commit they mean to deploy. The gate refuses anyone not on
`ARGON_DEPLOYERS`, and a commit that is not main's latest; tier1 then runs
again in that same run, and only if every suite is green does the deploy go,
one at a time, never cancelled mid-run.

Why not required reviewers (finding 9): the ArgonFinancials org is on GitHub
Free and the repo is private, where environment reviewers need Enterprise.
**The limit, stated:** anyone with write access can edit the workflow to drop
the gate, and Free has no branch protection for private repos. Write access is
the real boundary: keep it to the people on `ARGON_DEPLOYERS`.

The deploy is `git archive` of the commit streamed over SSH to the user
**argon-deploy**:
- its key is its own ed25519 key, **never `prod-loan-wallet.pem`**, which opens other hosts;
- `authorized_keys` pins it to `restrict,command="sudo /opt/argon/bin/argon-deploy"`, so there is no shell, no forwarding and no pty, whatever the client asks for;
- sudoers allows exactly that path with no arguments; the user has no password and is not in the docker group (`25-deploy-user.sh` refuses if it is);
- the workflow pins the server's host key, so a server answering with any other key is refused.

`argon-deploy` installs the release (05), builds the image (30), migrates with
a backup first (40 `--confirm`), runs the probe (70) and asserts loopback (75).
**It does not start a stopped API**: starting it stays a deliberate act. A
running API is recreated on the new image and must answer `/healthz`. The
release id is `<UTC time>-<commit>`.

One-time setup:
```bash
# On the laptop: a key for this purpose only. The private half goes ONLY into the GitHub secret.
ssh-keygen -t ed25519 -N '' -C argon-deploy -f argon-deploy
scp argon-deploy.pub ubuntu@<host>:
# On the server, from the release:
cd /opt/argon/src && sudo $E bash deploy/25-deploy-user.sh ~/argon-deploy.pub
# GitHub, Settings:
#   Secrets  ARGON_DEPLOY_KEY  = contents of argon-deploy (the private key); then delete the local copy
#   Variables ARGON_DEPLOY_HOST = the server's hostname
#   Variables ARGON_DEPLOYERS   = GitHub logins allowed to deploy, comma-separated (admin to change)
```
If the server is rebuilt, its host key changes: update `ARGON_HOST_KEY` in the
workflow from a fingerprint checked out of band, never from whatever the new
server presents.

Stated limits: the tier1 runner is amd64 and the server is arm64, so the
suites prove the rules on the same major and image family, not the same CPU.
The deploy streams source and builds on the server; it does not ship a
CI-built image.

## One major version: 18, everywhere

Local, docker and EC2 all run PostgreSQL 18 (`PG_MAJOR` in `lib.sh`;
preflight and `10-start-postgres.sh` refuse anything else). One major
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

**On the server, Docker access is superuser.** The postgres container trusts
local connections, so anyone who can run `docker` on the host (root, or any
member of the `docker` group) is the database superuser through
`docker exec`, and can read or rewrite anything, the ledger included. The role
split proven by the probe (P11–P17: argon_app cannot mint a proof, record an
attempt or read a PIN hash; argon_auth cannot spend a proof or post) protects
against the **application**: a compromised or buggy handler. It does **not**
protect against anyone with shell access to the server. Keep the `docker`
group empty, and treat SSH access to this host as access to every member's
money.

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
- the postgres superuser is reachable through `docker exec` on the host (root or the docker group, see above) or with its password from `/etc/argon/deploy.env`.

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
- Alerts reach nobody until the Slack webhook and heartbeat URLs are set on the server (`ALERT_CHANNEL=none` says so in every alert).
- Inside its container the API binds `:PORT` on all interfaces (`cmd/api/main.go`); compose publishes it on `127.0.0.1` only, and `75-assert-loopback.sh` asserts that after every step that can open a port.

### Known gap: write access can remove the deploy gate

**Stated, understood, not fixable on the current plan.** The deploy gate (the
`ARGON_DEPLOYERS` list and the typed commit) lives in
`.github/workflows/deploy.yml`, on `main`. Anyone with write access can change
that file on `main` and remove the gate; nothing stops them, because the
ArgonFinancials org is on **GitHub Free**, and Free has no branch protection or
rulesets for private repositories. Today **three accounts** have that access
(`samuelmohel`, `dayowole`, `Argon-Financials`, all admin), and **only one**
(`samuelmohel`) is on `ARGON_DEPLOYERS`. So the gate stops a mistaken or
unlisted click; it does not stop a writer who means to deploy.

**Remedy: a paid org plan.**
- **GitHub Team** adds branch protection for private repos. Protect `main` so
  every change needs a reviewed pull request, with a CODEOWNERS rule making a
  deployer's review mandatory for `.github/`. The gate can then only be removed
  with a deployer's approval.
- **GitHub Enterprise** adds required reviewers on environments: the original
  design, a named approver for every deploy (finding 9).

Until then, keep write access to the people on `ARGON_DEPLOYERS`. That leaves
the remaining risk with the deployers themselves.
