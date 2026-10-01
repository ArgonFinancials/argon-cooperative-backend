# Argon backend

Go + PostgreSQL. Serves the member, approver, cooperative-admin and
platform-admin surfaces from one place. The frontend is a separate repository.

**Read `LEDGER_CONTRACT.md` before touching `internal/ledger` or `migrations/`.**
It states obligations, not intentions, and the frontend is already written
against it.

## Run

```bash
cp .env.example .env
make up            # postgres in docker
make migrate-up
make run           # :8080
```

`curl -i localhost:8080/healthz` should return `200` (`503` if the database is unreachable).

## The rules this service exists to hold

**Money is `int64` kobo. ₦1 = 100 kobo. Never a float, at any layer.**
On the wire money is a **string**, not a JSON number — a JSON number is an
IEEE-754 double, so a number would silently break the rule at the one layer
nobody inspects. `internal/money` rejects a bare JSON number rather than
coercing it.

**A posted ledger entry is immutable.** There is no update operation on the
port and no update path in the database — `migrations/0001_init.up.sql` installs
a trigger that refuses `UPDATE` and `DELETE` on every ledger table. A correction
is a reversal and a repost, or an adjusting entry.

**The trial balance sums to zero by construction.** A deferred constraint
trigger rejects any entry whose lines do not sum to zero, at commit. Not
flagged, not queued for review — rejected.

**Attribution is point-in-time.** Every entry snapshots the actor's post at
posting. An officer handover must never rewrite the past: a March signature
still reads as March's treasurer after a June handover.

**A member sits on exactly one collection channel per cycle.** A member
collected twice is a money defect of the same class as an unbalanced entry.

**`postAll` is one transactional call**, idempotent on a client-minted
reference. If this API can only accept rows individually, all-or-nothing is not
achievable at the client and the requirement is ours — see `LEDGER_CONTRACT.md`
§1, and raise it before designing the endpoint.

**The partner charge and the Argon margin are separate lines.** Summed into one
total the distinction is unrecoverable from history, and transaction charges are
the only revenue line in the product.

**VFD is the record of account; this ledger is the book of account.** They will
periodically disagree — in-flight transfers, provider fees, settlement timing.
A difference is a reconciliation item to be explained, never a correction
applied to the books.

## Tier 1

These need a **human-written failing acceptance test before implementation**.
A test written by whatever wrote the code encodes the same misunderstanding and
puts a green tick next to it.

- Ledger posting: immutability and balance
- Exactly-once / idempotency on `postAll`
- Permission enforcement, server-side
- Exactly one collection channel per member per cycle
- Quorum evaluation: no user counts as more than one signatory

## The contract

`api/openapi.yaml` is authored here and vendored by the frontend as a pinned,
committed copy. Generate Go server interfaces from it so divergence is a compile
error rather than a runtime surprise. Every enum's description names the
decision it came from and the one-line reason, because the semantics originated
in the design package and this file is where they get read.

## Layout

```
cmd/api/           entrypoint
internal/money/    Kobo — the one money type, string on the wire
internal/ledger/   the port: LEDGER_CONTRACT.md's server-side shape
internal/config/   environment
internal/db/       pool
migrations/        schema, with the Tier 1 constraints as DB constraints
api/               openapi.yaml
```
