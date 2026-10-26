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
F=/etc/argon/deploy.env   # the CSRF key: the same line as on an existing instance (below)
sudo grep -qE '^ARGON_CSRF_KEY=[0-9a-f]{64,}$' "$F" || { sudo sed -i '/^ARGON_CSRF_KEY=/d' "$F"; echo "ARGON_CSRF_KEY=$(openssl rand -hex 32)" | sudo tee -a "$F" >/dev/null; }

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

### A new key on an existing instance

A release can need a variable the instance's `/etc/argon/deploy.env` never had: that file was copied from the example once, at first install, and is never copied again. `ARGON_CSRF_KEY` (2026-10-26, the browser session) is the first. Without it, `30-install-api.sh` refuses to install; the running API is untouched (the release checkout from `05` is already replaced by then, but nothing restarts the container).

Run this ON THE INSTANCE before the deploy that first needs it. It writes a key only when no valid one is there, so running it twice changes nothing; a line that exists but is empty or malformed is replaced, not kept. The key goes from `openssl` into the file through `tee`, never on a command line.

```bash
F=/etc/argon/deploy.env
sudo grep -qE '^ARGON_CSRF_KEY=[0-9a-f]{64,}$' "$F" || { sudo sed -i '/^ARGON_CSRF_KEY=/d' "$F"; echo "ARGON_CSRF_KEY=$(openssl rand -hex 32)" | sudo tee -a "$F" >/dev/null; }
sudo grep -c '^ARGON_CSRF_KEY=' "$F"   # must print 1
```

Why not a plain append: run twice, it leaves two keys; the later one wins, and every open page's CSRF token is refused until it reloads, which would look like the cookie work was broken rather than the install. Why not the first-install `sed s|^…=.*|…|` alone: on a file without the line it changes nothing and says nothing.

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
| 29 | **UNEXPLAINED, NOT RESOLVED.** On 2026-10-07 one local run of suite 0002 (`mutate.sh 0002 --svc`, inside a loop over every suite) printed GREEN 53/53 and "probe passes", then no verdict line at all: neither the all-clear nor "mutation run FAILED". Every other suite in the same loop printed one. A rerun printed the full all-clear. The cause is not known. | An unexplained quiet run in a verification suite is the shape that has bitten repeatedly (findings 11, 23, 28). "A rerun was fine" is not a diagnosis. Open until the cause is found. If it recurs, keep the whole output, not a filtered summary: the loop that hit it kept only grep matches, which is why there is nothing to read. |
| 30 | **OPEN, fix named, not yet done.** On the server, the drill's `run_tier1` starts the test binary through `sudo -u postgres env VAR=… bin`, and the database URLs it passes carry passwords: argon_migrator's (since the drill was written), and since 2026-10-08 argon_app's and argon_auth's (the app-role step followed the existing pattern, because a different mechanism on the server path cannot be tested from here). Command arguments are readable by any local user in the process list while the job runs. | **The fix:** pass the URLs through an environment file (root-owned, 0600, read by a wrapper) or on stdin, never argv. Then, during a drill, confirm with `ps` that no password appears. **The size:** it is exploitable only by someone who already has a shell on that host. That is the trust boundary already documented under "Who can act as argon_owner" (keep the docker group empty; SSH access to this host is access to every member's money), and such a person can already read `/etc/argon/deploy.env` if root, or reach the database through docker if in that group. So it is a real gap of known size, not an alarm: it widens what an unprivileged local user could see, and closing it is worth doing on the server path, tested there. Whoever fixes it should know both halves. |
| 31 | The test harness stopped applying `ARGON_TEST_SCHEMA` at an empty entry, silently: `if f == "" { break }`. A hand-built list with a stray comma (mine, 2026-10-09, validating suite 0011's fixture moves) applied 0001–0010 and nothing after, and 406 tests "passed" against a schema nobody asked for. Caught only because suite 0011's own 43 tests failed with "relation charge_schedules does not exist". No committed suite definition contains an empty entry. | Finding 23's class, in the oldest instrument: an input that ends the work without a word. An empty entry now refuses (exit 2, naming the list), proved with the same stray comma. A validation is only as good as the schema it actually ran against: read back what was applied, not what was asked for. **How far back it reaches (established 2026-10-11, not assumed):** "the current list has no empty entry" and "no previous run was affected" are different claims. The checks that support the second: (1) every `SUITE_BASE` ever committed, across all of git history (14 distinct values): none has an empty entry; (2) every version of mutate.sh since its first (8967ed2): the GREEN list has always stripped its trailing comma; the base was a literal list, then `abs()` mapped each entry to a path (an empty one would have become a directory and failed loudly); a mutant run appends one file after a comma; (3) the Makefile has never set the variable; CI only runs mutate.sh; the drill names an existing database and applies no schema; (4) every hand-built list in this project's session transcripts (32 commands since 2026-09-30), read by eye: two had the empty entry. One was the run this finding records; the other was the deliberate proof that the refusal works. **Limit:** runs outside these transcripts (another session, someone at a terminal) cannot be checked from here. Nothing in the repository ever built such a list. |
| 32 | **There was no B-04 spike.** LEDGER_CONTRACT carried a "Step 0 B-04 finding as summarised", and D-04 sat DISPUTED for eleven days waiting on "whoever ran B-04". A read-only review of all four Argon repositories (2026-10-13) found "B-04" and "spike" nowhere in code, docs or history. The summary described work nobody did. D-04 was settled instead by what the running wallet platform does. | A summary of work is not evidence that the work happened. When a decision rests on a cited study, find the study. Where there is none, the cited study is itself the finding, and the answer comes from the system that exists. |
| 33 | **Suite 0011 as approved, and its migration (71c8f49), refused to record reality.** An officer pays a withdrawal, then records it, and the database checks withdrawable at that moment. If the check fails, the money has left the bank and the ledger refuses to say so. The wallet platform's open ₦20,000 case and its refund-turned-double-payout are this shape. Caught by reading that platform before any withdrawal could run here (no endpoint posts one, and no society has the configuration a withdrawal needs). | LEDGER_CONTRACT §11: **the ledger must be able to record anything that actually happened.** Validation belongs before the money moves: hold at authorisation. After the money moves, record the confirmed outcome and flag any difference for a person. A rule that prevents recording reality is a blindfold, not a control. 0011 is revised, tests first, for re-approval. |
| 34 | **A capability check that skipped itself.** The probe's 0014 cases (the app cannot call the operator's functions or write their log) decided whether 0014 existed by asking `information_schema.tables` *as argon_app*. information_schema lists only what the asking role holds privileges on; the app holds none on the settings log, correctly, so the probe read "absent" and skipped all four cases without a word. A sabotage that granted the app EXECUTE on the quorum function then "passed". Caught because the new rows never appeared. Separately and UNEXPLAINED: the first direct probe run that day exited 1, where the same code then exited 0; its log was deleted before being read. | Detect from the catalog (`to_regclass`), never from a view filtered by the asker's privileges, and **say when a block is skipped** (the probe now prints it). The other era checks ask about tables the app does hold privileges on, so they were not affected; that is luck, not design, and the rule now is the catalog. Proved: the four cases run and refuse; with EXECUTE granted to the app, only P36 goes red. A log is read before it is deleted (finding 29's lesson, missed again). |
| 35 | **Three ways the operator script's tests could have passed without testing anything** (2026-10-14, all caught before the script was trusted). (a) The fixture that seeds a society per test ran as `c=$(fresh)`: in a subshell the counter never advanced and its `die` stopped only the subshell, so every test after the first ran against **no society at all**, and went on to its checks. (b) Git Bash's `grep -qiF` aborted on the script's output, so every text check failed whatever was printed. (c) Three refusal tests looked for the database's reason by a phrase the script's own preview also prints ("never backdated", "unilateral", "debt will not be recovered"): S14 passed while the script's refusal formatter matched nothing and printed every refusal as a raw psql dump. S09 and S10 were saved only by also checking the rule's name. | (a) the fixture runs in the test's own shell and sets a variable; (b) the comparison is in bash; (c) every refusal test now requires `REFUSED by the database (<rule>)` and a phrase only the database says, and the formatter reads psql's `psql:<stdin>:N:` prefix. Proved: twenty sabotaged copies of the script (`test/ops/sabotage_settings.sh`), each caught by the test it should fail, for the reason it should. Same lesson as the 0011–0013 neighbours: **ask of every passing test what actually made it pass**, including text the code under test prints itself. |
| 36 | **Finding 24 recurring, again: the money acts could not be stepped up for.** 0011 and 0012 added `contribution_record`, `withdrawal_payout` and `withdrawal_authorisation`; the auth service's `ActKinds` and the API's `ActKind` enum did not, so no client could mint a proof to record or authorise money. Suites 0011 and 0012 never noticed: their `stepUp` fixture writes `auth_proofs` straight into the database, past the service. Found writing the money API's tests (2026-10-14). | Fixed in the list and the spec. **The class, not the line:** TestApiMoney01 reads every act the schema's `proof_act_kind_known` allows and mints each THROUGH THE API, so the next migration that adds an act and not the service fails a test. Proved: with `guarantee` (an old act) removed from the list, TestApiMoney01 goes red naming it. A fixture that bypasses a service cannot find that service's gaps. |
| 37 | **A test that proved step-up reuse and looked like it proved idempotency** (2026-10-14, TestApiMoney29). The break that ignores the Idempotency-Key went red only because the retry carried the first call's proof, already spent, so the step-up refused it: the right test, red, for a reason that had nothing to do with the key. A client that steps up again on retry would have been recorded twice under that break, and the test would never have seen it. The sixth time a covering rule hid the one under test, and the second where the covering rule was one of ours (the step-up) rather than a neighbour's. Caught by reading the red's reason. | The test now retries first with a FRESH proof, so only the key stands between it and a second record, and asserts `RECORDED TWICE` on a 201; then resends as it was. Order fixed in a slice (a map's order is random, and the fresh retry must come first). **And the predictable half:** every rule since 0011 has made existing tests unreachable from upstream (0015: 23 fixture failures, four neighbour-covered). Since 0014, every new rule's reference is run against EVERY suite before review (`all_vs_ref`), and each failure's reason is read; that run is part of the sequence for a new rule, not an occasional check. |
| 38 | **Two runs made the same scratch database name in one clock tick** (2026-10-15). The test binary named its database `argon_verify_t<UnixNano>`; Windows' clock ticks in 100 ns, and two mutation runs started side by side created the same name, so one break's `CREATE DATABASE` failed and that break never ran (`SCHEMA DID NOT APPLY`, which mutate.sh counted as a failure, so it was loud). Found reading the failure, not guessed. The same class as `randHex` (a token unique per process, not across processes) and HANDOVER row 20: an identifier unique only by time. | **And the collision showed a worse defect:** the losing process's deferred `DROP DATABASE … WITH (FORCE)` was armed before its `CREATE`, so it dropped the WINNER's database (suite 0004's run failed with `database … does not exist`). "Only a name this process generated is ever dropped" held by luck. The drop is now armed only after this process's `CREATE` succeeds. The process id joins the time in all four clock-built scratch names: the test binary's, the probe's, the drill's, and the operator script tests'. Searched for the class: the society codes and import keys carry per-process sequence ranges (`ARGON_TEST_SEQ_BASE`) and the release id is one-at-a-time, so they stand. **A name is unique across processes or it is not unique.** **Two rules, from now on.** (1) **A cleanup is armed only after the thing it cleans up exists**: armed earlier, it can destroy something it did not create; the same ordering pointed at anything other than a scratch database destroys whatever it finds. (2) **A timestamp is not an identifier**: every scratch name (a database, a directory, a file another process could also make) includes something per-process (the process id) as well as the time. **What held:** the scratch-name guards (`assert_scratch`, the binary's `^argon_verify_t[0-9]+$`) were the second line, and they are why this cost a scratch database rather than something worse. |
| 39 | **A verification output read partially** (2026-10-15). Proving the 0015 grant, I removed it and ran the probe, then filtered the result for `✗`. Only P34 showed, so the grant looked barely tested. The probe marks a wrong error or a failed control `?`, not `✗`: read in full, every case that writes the column had failed (CTL FAIL, WRONG ERR) and P42, which writes none, still passed, the strongest proof of the lot. The run was right; my reading of it was not. | **A verification output read partially is a verification not done**, the same class as every silent failure on this list. Read every line that is not a plain pass, with the markers the tool actually uses (`✗` and `?` for the probe; `PASSED ON BROKEN`, `BROKEN FIXTURE ?`, `NOT RUN ?`, `SCHEMA DID NOT APPLY` for mutate.sh), not the marker one expects. The same day a `SCHEMA DID NOT APPLY` line was found the same way (finding 38). |
| 40 | **No endpoint said who the caller is** (2026-10-16). Sign-in returned a session token and nothing else, so a client could learn no user, member, society or post, and every other endpoint needs one of those ids. Sign-in, statements, the ledger reads, loans, approvals, imports and money were each built and verified in isolation, each GREEN against its own tests; none could be reached from a screen without hard-coded ids. Found when asked what the frontend could consume today, not by any test. | **The gap was in what joins them.** Each endpoint's suite proves the endpoint; nothing proved a client can get from sign-in to it. `GET /me` first (tests first), and from now on a new endpoint is checked for how a client obtains every id it takes. |
| 41 | **A withdrawal request had no author** (2026-10-18). `approval_requests` recorded whom a request was for, never who raised it, and raising needed no step-up; the client also chose the request's expiry. It survived because nothing had needed to ask who raised a request until a member could raise one. The same shape as the approval status nobody wrote (finding 14): a field never wrong because nothing read it. | 0016 (tests first): `raised_by` and the raiser's own step-up, refused without either; the expiry is the server's, from a society setting (seven days shipped). A table is checked for authorship when the first path that writes it is designed, not when the first reader arrives. |
| 42 | **A push landed a stub** (2026-10-18). In one chained command a `git add` failed (the stub's deletion was already staged), the commit never happened, and the `git push` after it still ran and reported success: main carried `/me` as a stub for a minute, until the next push. Nothing caught it; I saw it reading the output. An action that partly failed and reported success. | `scripts/push-main.sh`: refuses a working tree that is not clean (staged, unstaged or untracked), lists the commits about to go, and reads the remote's main back afterwards, failing unless it IS the intended commit. Proved: an untracked file and a modified tracked file are each refused. Every push goes through it; a push is never chained after a commit in one line again. |
| 43 | **Neighbours covering, again, and now a step** (2026-10-19, suites 0016 and requests_api). Writing 0016, my rules covered for others three ways: the raiser check for the step-up check (TestRaise01 was red on `withdrawal_request_act_is_raisers`); 0016's triggers, named to fire first, for 0008's loan rule, 0011's member and quorum rules (TestLoan23, TestMoney41) and 0011's requester rule (TestMoney44); and in the API fake, its own check of who may raise for the database's (test 04 passed on the broken service). mutate.sh had scored the first as `red ✓`: only reading the reasons caught it, the seventh time. Also: three breaks borrowed from the approvals suite broke code the new handlers never call, and a test (the client's expiry) no API break could reach. | **mutate.sh now fails a targeted red whose reason is `WRONG REFUSAL`** (`RED FOR ANOTHER RULE ✗`), proved on the restored-to-wrong mutant. 0016's triggers are named to fire AFTER the older rules on the same tables, so each older rule refuses its own case first. The fake keeps only the API's own rule (the duty: treasurer and secretary) and maps the database's. Each test got a break of its own; the unreachable test was removed before review rather than kept as decoration. |
| 44 | **Our own fixture met the quorum ceiling first, and it was passing** (2026-10-19). TestMoney44 tested a treasurer's own withdrawal in a world of two signatories at a quorum of two: a request that could never have been approved, since the treasurer never signs their own and only the chairman was left. It passed, because it tested only that the treasurer's signature is refused. The same arithmetic, with three signatories at a quorum of three, holds for every deployed society today. | 0016 refuses such a request when raised, with the arithmetic and the remedy; the fixture got a third signatory so it tests what it says. **A test can pass in a world the system could never reach.** "Would a society actually be in this state?" is a question no suite asks and no mutation run can answer: a mutant breaks the code, never the fixture's world. The SECOND time our fixtures described an impossible world (the first: a member record with no person behind it, which two rules refused). Fixtures drift from reality more easily than code does, because nothing checks them against it. |
| 45 | **`range_sane` is unreachable for signatories and auditors** (2026-10-19, found proving a fixture for requests test 21). An appointment whose term ends before it begins is refused, but not by `range_sane` (0001): 0002's BEFORE trigger `appointments_auditor_holds_no_signing_post` runs first, builds `daterange(held_from, held_until)`, and PostgreSQL raises its own error, "range lower bound must be less than or equal to range upper bound" (SQLSTATE 22000), naming no rule. The world stays impossible, so nothing is unsafe; but the refusal is one an operator cannot act on, and `range_sane` has never been tested (no test names it). Neighbours covering (finding 43), inside the schema. | **Open, not fixed**: a Tier 1 table, so a reviewed failing test first (an end before the start refused AS `range_sane`, for a signatory, an auditor and a non-signing post), then the trigger checks the range before building one. Requests test 21's fixture now reads the ended appointment back (held since 2020-01-01, ended yesterday), so a missing appointment stops the fixture instead of building another world. |
| 46 | **A suite approved without being read** (2026-10-20). Suite 0016 and the requests API tests were sent for review as plain text in the chat, twice, whole files of about 360 lines each; both times the commentary arrived and the code did not. The reviewer approved them WITHOUT reading them, because the delivery channel failed repeatedly, and said so: a real reduction in the review standard, recorded rather than glossed. What stood in for reading: every test proven to fail against a mutant for its own reason; mutate.sh failing any mutant caught by the wrong rule (the gap reading mainly caught, findings 43–44); the three changes the reviewer asked for, each proven to fail; and the author's own unprompted finding of test 21's second weakness. More evidence than a reading usually adds; not the same thing. | **Review goes in pieces small enough to survive**: one test at a time if necessary, or only the parts where a decision is encoded (assertions, world-building fixtures, refusal texts), numbered so a missing piece is noticed. Six assertions that matter read beat forty not received. |
| 47 | **mutate.sh died silently on the failure it was written to explain** (2026-10-20, the first GREEN of 0016). The argon_app probe step failed, mutate.sh printed "PROBE FAILED as argon_app ✗ — …:" and stopped: no reason, no summary, and its trap deleted the probe's log. Under lib.sh's `set -Eeuo pipefail`, the `grep` that picks the probe's error lines matched NOTHING, so the pipeline returned 1 and errexit ended the script, one line before the "last lines of the probe" fallback whose own comment says it exists because "a failure with no reason shown is the dangerous kind". The fallback had never run: no failure until now had lacked a matching line. WHY the probe failed is lost with its log; rerun, it passed. The likeliest cause, unproven: I edited 70-verify-tier1.sh while mutate.sh was running it, and bash reads a script as it executes. | The picking pipeline is `{ …; } \|\| true`. Proved on the exact code under lib.sh's options with a log that matches nothing: the old code exits 1 printing nothing; the new prints the last lines. **Rule: a failure path must be proven by making it fire, not by reading it.** It is the only code that runs exactly when nothing else is working; here the explanation was unreachable precisely when it was needed, and it took the evidence with it. The lost probe failure stays UNPROVEN: "edited while running" is a plausible cause, not a diagnosis. |
| 48 | **A local commit that would not have built** (2026-10-20). Committing finding 47's fix, two deletions already staged for the next commit (the request stubs, the reference rules) went in with it: a commit deleting `requests_notbuilt.go` without adding `requests.go`, which does not compile. Caught reading the commit's file list ("staged files: 4", expecting 2), undone with a soft reset before any push, and recommitted with its own two files. Nothing lost. But push-main.sh guards the REMOTE (what landed is what was meant); nothing guards a local commit that does not build, and a commit that does not build on main breaks bisecting and any deploy of it. | Open question for the reviewer: whether, and where, to guard it. **Habit meanwhile: read a commit's file list before trusting its message.** |
| 49 | **A charge that moved between asking and being paid, told to nobody** (2026-10-21, found writing the balance tests). The quote is indicative and the hold's charge applies (decided): a member quoted ₦10.75 can have ₦26.88 taken if the society's series changes in between, and nothing says the figure moved. Worse, when the higher charge no longer fits their withdrawable, the hold refuses with the figures and the request stops, for a reason the member was never told and did not cause. The undisclosed gap on a contribution, by another route. Recomputing "what was quoted" from the series in force on the raise date does NOT work: a series taking effect the same day, after the request, changes that day's figure (test 10 builds exactly that world). | **Open, being built (the full version, decided 2026-10-21)**: 0017 stores the quoted charge on the request at raise (server-set, as the expiry); the request shows BOTH the quoted and the authorised charge, and says so in words when they differ; the hold's refusal names the change as well as the arithmetic. Tests first. **A member is never left with a stopped request and an unexplained figure.** |
| 50 | **A member cannot see who read their savings** (2026-10-21). Reading a member's figure is tied to the duty (the treasurer and secretary, who raise for a member; the auditor), but nothing records or shows a member which officer looked. The platform-admin design already promises a society can see what Argon staff looked at; a member seeing nothing about their own officers is the weaker position. | **Open, not built: a noted gap, not an absence.** Not now and probably not soon. |
| 51 | **Four red builds, two days, nobody told** (19–21 October; the reviewer found out from an email). From a886476 on, every push's CI run failed in about 2 seconds: GitHub refused to start any job ("recent account payments have failed or your spending limit needs to be increased"); the jobs had no steps at all. The repo, the workflow and the runners were not the cause, and the gate and deploy were skipped (it failed closed: the server stayed on 7cfb7dc, the nightly backups ran). Nothing was reported because nothing read CI: my only watcher had been for e5501ce's run, and after each later push I read push-main.sh's "the remote's main is what was intended", which proves the push LANDED, not that CI RAN. I treated the first as covering the second. | `scripts/ci-result.sh`, run by push-main.sh after the readback: waits for the push's run and passes only on success; otherwise names each failed job with GitHub's own annotation, grouped (one refusal shared by 26 jobs prints once), and the log of any job that ran a step; fails too when no run appears or none finishes. Made to fire: the green run passes; the billing refusal fails in 43 s with GitHub's words; a commit with no run fails; and push-main.sh, its push landed on a scratch remote whose CI cannot be read, says "PUSHED BUT NOT VERIFIED" and exits 1. First version retried a log that a never-started job does not have, for 20+ minutes: caught by running it, not reading it. **A push that landed is not a push that was verified.** |
| 52 | **A suite on main that CI never ran** (found 2026-10-22). The CI matrix is a list written by hand; `balance_api` was never added, so it reached main and never ran, while the run reported 26 green and nobody asked which 26. The same shape as the auth act kinds falling behind the schema, twice: a list someone maintains always drifts. mutate.sh's list of accepted suites is a third copy. | `scripts/check-suite-list.sh`: the suite files are the truth; the matrix and mutate.sh's list are compared with them both ways (a suite never run; a name with no suite). A CI job of its own that the deploy needs, and run by push-main.sh before any push. Made to fire: on the real gap (0017, balance_api), a ghost matrix entry, a suite mutate.sh does not accept; push-main.sh refused, the remote untouched. **Widened the same day, after the first push with the CI wait came back red (6e1e534): "PUSHED BUT NOT VERIFIED".** 28 of 29 jobs passed; suite 0002, the catch-all, failed 0017's tests as unproven, because its exclusion list (two copies in the suite, a third in the restore drill) was a fourth hand-written list I had not updated, and I had not run suite 0002 after adding 0017. The check now also requires every test to belong to EXACTLY ONE suite, chosen as mutate.sh chooses; made to fire on the real case (0017's nine tests claimed by 0002 and 0017) and on tests claimed by none. The CI wait did its job on its first real push. **Then every other hand list was looked for, not waited for:** the restore drill named 26 suites by hand (it now runs every suite file whose prerequisite exists, selected as mutate.sh's GREEN selects, and SAYS which are skipped as tests-first; compared entry by entry with the 26 it replaced, and run in a full local drill); suite 0002 kept its exclusion list twice (`SUITE_SKIP` is now `$SUITE_EXCLUDE`). The Go list of step-up act kinds was already guarded (TestApiMoney01 mints every kind the schema allows). |
| 53 | **The repository went public** (2026-10-22, a workaround while billing is sorted). Every object ever pushed is now readable. Scanned, every blob of all 99 commits: NO SECRET, by value (the local env's values; on the server, its env files' values compared there against a clone of the public history, only key names returned; the operator's SSH key), by pattern and by path. One key's private half exists only in GitHub's secrets and could not be checked by value; no private-key header and no key-shaped base64 is in the history. BUT THE REPO IS A MAP: infrastructure identifiers and access arrangements, written for colleagues, are now readable by anyone. Among them the server's PUBLIC HOST KEY in the workflow (it pins the server's identity for CI): not a secret, but internet scanners index servers by host key, so it can lead to the server's address though no hostname or IP is published. The details are with the reviewer, deliberately NOT restated here: this file is published too. | **History is not rewritten** (decided: destructive, and clones keep it anyway). The map is made out of date instead, by AWS-side actions the reviewer has raised as a security item. **`deploy/AWS_REQUEST.md` is to be REMOVED from the tree once those are done.** The repo returns to private as soon as billing allows. **Rule: a record of an exposure must not repeat it** (this entry's first draft restated the map; caught before the push). |
| 54 | **A gate that never gated, proven by a copy of itself** (2026-10-22, the derived restore drill). The drill was given a gate: a suite runs only where the restored copy's schema has its migrations. A printout of the selection said a copy at 15 would skip 0016; the real drill of a copy at 15 ran 0016 and failed. Two defects, both mine: (1) the fields were read with a TAB separator, which `read` treats as whitespace, so an empty skip pattern collapsed and the next field moved into its place; (2) the script edits, made through Python strings, wrote CONTROL BYTES where text was meant (0x01 for `\1` in the sed replacement, so the migration number was never a number and `((want > schema))` never held; 0x1F raw). The printout used my own copy of the logic, not the script's code, so it proved the idea and not the file: the same mistake as the comparison an hour before. | The separator is `\x1f`, the bytes repaired, and every tracked text file scanned for control bytes (only this script had any). Proved by drilling the real backups: at schema 16 every live suite ran and passed and the two tests-first suites were named NOT RUN; at schema 15, re-run by name after the fix. **Rule: verify the code in the file, never a copy of its logic.** A check of a transcription proves the transcription. **The cause of (2), found writing this entry, which it damaged the same way:** the command transport I edit through collapses a doubled backslash to one before the shell sees it, so an escape written into a Python string inside a command arrives one level lower and becomes a control byte. Edits carrying backslashes are now made with the editor or from a script file, never inline, and a byte scan follows every scripted edit. **And a second gap the repaired gate exposed:** at schema 15 it ran approvals_api, whose base lists only what its fixtures need, against this release's handlers, which read 0016's column. A suite of the release's own CODE needs the release's whole schema (a deploy migrates before that code runs, so code on an older schema is a world production is never in); the drill now names such suites NOT RUN on an older copy, with the reason. |
| 55 | **The member's statement dropped VAT and the levy, and the spec could not say they existed** (2026-10-22, found moving the statement tests to D-07). Since 0011 and 0013, VAT on Argon's fee and the transfer levy are their own ledger lines. The statement read only the partner's and Argon's lines: a withdrawal charged ₦60.75 showed a ₦10 charge, VAT and the levy left the member's savings and appeared nowhere on their statement, and the row's gross came out wrong (the savings line less only two of the four charges). A read that hid money from the member it belongs to. As a member sees it (the frontend's live screen, 2026-10-23, the real server against a seeded dev history): the ₦20,000 withdrawal reads "Moved −₦20,050.75 · Charges Argon −₦10", an amount that is neither what was withdrawn (₦20,000) nor what left the savings (₦20,060.75), the VAT and the levy folded into it and gone as charges; a member reading their own statement could reconcile it against nothing. Underneath, the spec's lists had drifted from the schema in four places (MovementKind lacked loan_write_off and loan_recovery since 0009; AccountRole four roles; the journal's ChargeRole and the statement's ChargeLine role lacked vat and statutory_levy), so a generated client could receive values it had no type for, and nothing failed. Also found: `ledger_discrepancies.kind` is unconstrained TEXT, so there is no schema list for the spec's Discrepancy.kind to be checked against. | Tests first: TestApiSpec01 compares every spec list beside a schema type or constraint with what the schema allows, read from the catalog (the TestApiMoney01 model), deliberate differences named in the test (`referred`); shown failing on the old spec with every drift named, then the spec corrected. The statement tests moved to the D-07 world (every component distinct, so any swap or omission shows); shown failing on the real handler (only Argon's line; no VAT or levy in the period) before the handler is changed; break `charges_two_roles_only` is the defect. One layer along (2026-10-23): the spec test catches a role the spec does not declare, never one the handler does not READ, so TestApiStatement20 reads every charge role from the schema's own constraint, posts one line of each (power-of-two amounts, so a shortfall names the roles skipped) and checks the GROSS first, then each line; proven on the real handler by a fifth-role schema (mutants_api/fifth_charge_role.sql, red on the gross by exactly its amount) and by break `gross_skips_levy` (every line shown, the levy left out of the gross). To run that mutant on the real handler, mutate.sh gained `SUITE_MUTANTS_REAL=1`: a suite's SQL mutants run as GREEN does (the fake's variable unset, the production roles' grants); before it, a SQL mutant in an API suite could only have run against the fake's setup. The limit, named so it is not assumed closed: only the api suite opts in, and it holds the ONLY SQL mutant of any API suite (the other twelve have none). So every other API suite meets the real handler in GREEN alone, against the schema as it is; every break they prove is in the fake. No API suite but this one is proven against a schema its handler has not caught up with. A gap, not a defect. The discrepancy kind's missing constraint: open, and worse than the six drifts, not better: a list with no schema cannot drift from one, so no test can compare it with anything, and a kind any code can invent is a kind nobody can report on. Whether to constrain it is a question of its own (raised 2026-10-23). |

**Rule: when a hand-written list becomes a derived one, the derivation must select the same things the list did, or the difference is explained** (2026-10-22, finding 52). Compare the two entry by entry before the list is deleted. Deriving the restore drill from each suite's plain run pattern would have silently lost suite 0003's ten service tests; the comparison against the 26 lines being replaced caught it before it was written.

**Habit: when a shared fixture changes, re-read every test that depends on it
before trusting the run (finding 27).** A mutation run proves a test fails
against a BROKEN implementation. It cannot prove the test still tests what it
says, because a test asserting nothing also fails against a broken one. Only
the GREEN half catches that, and GREEN runs last; for a migration not yet
written, it does not run at all. Moving the fixtures onto step-up acts disarmed
TestSignStep6, the mutation run counted it as proved, and only GREEN caught it.

**Rule: when a test asserts on output, it requires text that only the system
under test can produce** (finding 35, S14). A test cannot tell where text came
from unless it is written to care: S14 looked for "never backdated", the
database never printed it, the script's own preview did, and the test passed
on the right string from the wrong source. So a refusal test asks for the
rule's name and a phrase only the database says, never a phrase the code in
front of it also prints. This is a new trap, not a variant of the old ones:
the mutation run cannot catch it, because the covering text survives every
mutant that does not touch the preview.

**This environment: Git Bash on Windows.** The development machine is
Windows; the server and CI are Linux. Failures that came from that difference
so far: finding 6 (CRLF in the forced command, loud), finding 7 (a line
written for Windows exited SILENTLY on Linux), finding 35(b) (Git Bash's
`grep -qiF` aborted, SILENTLY failing every text check), and on 2026-10-14 two
loud ones in the operator script (`rev` missing; Python's `\r\n` made an enum
value invalid). It cuts both ways: whoever runs these tests on Linux will not
reproduce the Windows failures, and a run on Windows proves nothing about
Linux-only paths (finding 7). CI runs on Linux; a check written here is not
proven until CI has run it.

**Rule: an API test fake implements only what the API itself decides, never a rule the database owns** (2026-10-19, the third time: 0015's pre-checks, then requests_api test 04). A fake that re-checks a database rule gives that rule one real guard and one decorative one, and only the decorative one is under test: breaking it changes nothing, because the database still refuses. The fake MAPS the database's refusals, as the real handler does; its breaks remove a mapping (and the refusal answers 500), never a duplicate check. Until a migration exists, the suite's base carries the reviewed reference rules (guarded), not a re-implementation in Go.

**Rule: a test that can never fail at its layer is removed, not kept.** A test no break at its layer can make red is documentation, and leaving it implies coverage that is not real. The layer that owns the rule tests it (finding 43: the client's expiry, the database's rule, TestRaise05).

**A habit is a check nobody has written yet.** The end state for each habit on this list is a tool that fails without anyone reading: mutate.sh's `RED FOR ANOTHER RULE` (finding 43), the push check (42), the act-kind test (36), the money suite's base guard.

**Rule: a new check is not trusted until it has been made to fail on purpose,
in the same commit that adds it.** Every new instrument in this project has
had a defect, and each looked like success:
- the deploy watcher, which took an old run for a new one (finding 18), and a
  second watcher on 2026-10-07 whose lookup never matched, so it would have
  polled silently for an hour;
- the crawler;
- the test lister, which found zero tests;
- probe case P21, which passed on an UPDATE of zero rows;
- the drill, which said FAILED without a reason (finding 11);
- the app-role probe step, which failed in CI and said nothing (finding 28).

So the commit that adds a check also shows it failing: a planted fault, the
failure it produces, and the reason it gives.

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

## A society's rules: the operator (`45-society-settings.sh`)

A society's quorums, floor and guard threshold are bye-law decisions, its
lifecycle is Argon's state for it, and its charges are configuration the app
never writes. Until request kinds exist to change them, the operator changes
them with this script, which calls 0014's functions as argon_migrator. Nothing
else should write these columns (see the trust boundary below: the owner's
role can, and that is the documented gap).

```bash
cd /opt/argon/src; E=ARGON_ENV=/etc/argon/deploy.env
# Show first: what the society has now, and who changed what, on whose decision.
sudo $E bash deploy/45-society-settings.sh show --society LAG001
# A society's setting: the operator, the society's resolution, the reason.
sudo $E bash deploy/45-society-settings.sh withdrawal-quorum --society LAG001 --value 3 \
  --operator "Your Name" --resolution "Minutes of the AGM, item 7" --resolution-date 2026-09-30 \
  --reason "Carrying out the AGM's resolution"
# ...prints the change and NOT APPLIED (exit 3). Read it, then the same line with --confirm.
```

- Settings: `withdrawal-quorum`, `write-off-quorum`, `signature-count`
  (each at least 2, refused below with the reason), `account-floor` and
  `guard-threshold` (in kobo; the preview shows naira), `lifecycle`,
  `charge-series --file SERIES.json` (`{"kind", "component", "from", "source",
  "bands": [{"from", "to", "flat", "bp", "cap"}]}`; never backdated).
- **The guard threshold has no minimum.** It is interface friction, not
  authorisation: it decides what a person is asked to confirm, never who must
  sign. The quorum rule does not apply to it.
- **The lifecycle and charge series take no resolution**, and the script
  refuses one rather than drop it: the lifecycle is Argon's state for the
  society, and charges are not the society's decision.
- The server login is recorded by the script, never by the caller (there is no
  flag for it), and **labelled for what it is**:
  `user@host [login, verified by id -un]`, and under sudo
  `; sudo by NAME [claimed: SUDO_USER from the environment, not verified]`.
  The sudo name is accurate on this server only because reading the env file
  needs root, and that is how it happens to be run, not a control. The fix:
  read the audit subsystem's login uid (`/proc/self/loginuid`, set by
  pam_loginuid at login and not changeable by the user) instead of the
  environment.
- A refusal is the database's, printed as written, with the rule's name.
  Nothing is changed by a refusal.
- Every change is in `society_settings_log`, which is never edited, and which
  0014's down migration refuses to drop once it holds a row.

Tests: `test/ops/society_settings_test.sh` (a scratch database; CI runs it with
suite 0014).

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
- only `argon_migrator` can, and only migrations (`40`), the operator's settings (`45`, through 0014's functions) and restores (`60`) use it,
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
