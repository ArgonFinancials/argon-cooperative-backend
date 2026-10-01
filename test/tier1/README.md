# Tier 1 acceptance tests

Written **before** the code they cover, and reviewed. The detail below was
written for 0002 and applies to every suite.

| File | What it is |
|---|---|
| `tier1_0002_test.go` | 53 tests (build tag `tier1`), each asserting a specific SQLSTATE + constraint name |
| `0002_interface.sql` | 0002's tables and columns **with no rules**: "M0", the maximally broken schema |
| `mutants/*.sql` | 38 deliberately wrong implementations. Each states what is wrong with it |
| `mutate.sh` | Proves both halves (below) |

```bash
make up db-setup
make tier1-mutate
```

## Every suite

Each was written and mutation-tested before the code it covers.
`ARGON_ENV=deploy/env/local.env bash test/tier1/mutate.sh <suite> [--svc]`

| Suite | Tests | Broken by | GREEN against |
|---|---|---|---|
| `0002` | `tier1_0002_test.go` (53) | `0002_interface.sql` (M0) + `mutants/` (38) | `migrations/0002_tier1.up.sql` |
| `0003` | `tier1_0003_test.go`, `tier1_0003_service_test.go` (84) | `mutants_0003/` (49) + the auth service fake's breaks (`--svc`) | `migrations/0003_auth.up.sql`, `internal/auth` |
| `0004` | `tier1_0004_test.go` (11) | M0 + `mutants_0004/` (8) | `migrations/0004_roles.up.sql` |
| `api` | `tier1_api_statement_test.go` (19) | the breaks in `apifake_test.go` (21, `--svc`) | `internal/api/statement.go` |
| `batch` | `tier1_api_batch_test.go` (22) | the breaks in `apifake_batch_test.go` (32, `--svc`) | `internal/api/batch.go` |

The restore drill (`deploy/60-restore.sh --drill`) runs every suite above (GREEN half)
against the restored copy.

## What `make tier1-mutate` proves

1. **GREEN:** every test passes against the real `migrations/*.up.sql`.
2. **RED where it must be:** every refusal test fails against M0, and against at least one *plausible partial fix*.
3. **Controls are sound:** every control passes on M0 (the statement shape is valid) and fails against an *over-strict* mutant (it can catch an implementation that refuses too much).
4. **Red for the right reason:** a failure while building a test's own fixtures is reported as BROKEN, never as red.
5. **Nothing vacuous:** every test is targeted by some mutant. An empty test list is refused outright.

Tests will not run without `ARGON_TEST_*` set, and they exit 2 rather than
skip. A skipped Tier 1 suite reads as a green one.

## Deliberately not tested

**Trial balance at period close (§5).** Every entry is forced to balance at
commit, so an unbalanced trial balance cannot be constructed. A test for it
could never fail. See the note above `TestP4_*` before adding one.

## Constraint names are part of the interface

`ledger_lines_entry_open`, `ledger_entries_posted_at_is_now`,
`signatory_approvals_one_per_user`, `signatory_user_is_member_user`,
`signatory_has_user`, `signatory_not_auditor`, `signatory_holds_signing_post`,
`signatory_approvals_signed_at_is_now`, `auditor_holds_no_signing_post`,
`ledger_entries_actor_user_id_fkey`, `value_date_known_unless_opening_balance`,
`ledger_lines_member_id_fkey`, `ledger_line_member_in_society`,
`entry_not_in_closed_period`, `period_close_is_final`,
`accounting_periods_no_overlap`, `adjustment_states_reason`,
`ledger_entries_adjusts_id_fkey`, `correction_is_reversal_or_adjustment`,
`correction_same_society`.

A trigger reports its name with
`RAISE … USING ERRCODE = 'check_violation', CONSTRAINT = '<name>'`.
