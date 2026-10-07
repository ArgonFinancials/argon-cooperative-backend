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
| `0005` | `tier1_0005_test.go` (12) | `0005_interface.sql` (M0) + `mutants_0005/` (12) | `migrations/0005_step_up.up.sql` |
| `0006` | `tier1_0006_test.go` (4) | M0 (the real 0001–0005) + `mutants_0006/` (4) | `migrations/0006_line_society.up.sql` |
| `0007` | `tier1_0007_test.go` (22, approvals) | `0007_interface.sql` (M0: the old writable status) + `mutants_0007/` (22) | `migrations/0007_approvals.up.sql` |
| `0008` | `tier1_0008_test.go` (36, loans) | `0008_interface.sql` (M0, on the real 0007) + `mutants_0008/` (36) | `migrations/0008_loans.up.sql` |
| `stepup` | `tier1_stepup_service_test.go` (9) | the step-up service fake's breaks (`--svc`) | `internal/auth/stepup.go` |
| `api` | `tier1_api_statement_test.go` (19) | the breaks in `apifake_test.go` (23, `--svc`) | `internal/api/statement.go` |
| `batch` | `tier1_api_batch_test.go` (22) | the breaks in `apifake_batch_test.go` (32, `--svc`) | `internal/api/batch.go` |
| `stepup_api` | `tier1_api_stepup_test.go` (15) | the breaks in `apifake_stepup_test.go` (`--svc`) | `internal/api/stepup.go` |
| `signin_api` | `tier1_api_signin_test.go` (14) | the breaks in `apifake_signin_test.go` (`--svc`) | `internal/api/signin.go` |
| `books` | `tier1_api_books_test.go` (31) | the breaks in `apifake_books_test.go` (32, `--svc`) | `internal/api/books.go` |
| `0009` | `tier1_0009_test.go` (18, write-offs) | `0009_interface.sql` (M0) + `mutants_0009/` (18) | `migrations/0009_write_offs.up.sql` |
| `loans_api` | `tier1_api_loans_test.go` (23), `tier1_api_loans_wo_test.go` (3, written off) | the breaks in `apifake_loans_test.go` (28, `--svc`) | `internal/api/loans.go` |

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

**A trial balance that does not sum to zero (§5: period close; the books'
`500 ledger_does_not_balance`).** Every entry balances at commit, and since
**0006** every line posts to an account of its own entry's society, so a
society's books that do not sum to zero cannot be constructed, and a test
for them could never fail. See the note above `TestP4_*` before adding one.
The refusals stay in the code: a check that cannot fire is still what stands
between a future rule change and a silent breakage.

**This section was wrong until 0006** (finding 10, below). It said "every
entry balances, so it cannot be constructed", and that held per ENTRY, not per
SOCIETY: a balanced entry with a line on another society's account broke two
trial balances at once.

## Finding 10: a line could post to another society's account (2026-10-05)

0002 checked a line's **member** against the entry's society and never its
**account**. A cross-society line passed every rule, its entry summed to zero,
and **both** societies' trial balances broke: one missing a line, one carrying
a foreign one. §3's "the trial balance sums to zero by construction" was not
true, and it survived five migrations and 239 tests. Closed by 0006, suite
`0006`.

**What found it: writing the trial balance READ, not testing the write path.**
A read that must sum to zero is a check on every rule that produced the data;
the write-path tests each checked one rule at a time and could all pass with
the hole between them. Habit: for every invariant a read promises (sums to
zero, two reads agree), ask which write rule makes it true, and test THAT rule
across the boundary the read crosses (here: per society, not per entry).

## Finding 12: an approved test that could not hold its decision (2026-10-05)

Statement test 13 was written for "a period bounds the rows and carries the
balance", and **approved**. It asked for a period starting in 2999. That passes
whether the statement filters by value date or by posting date, because every
entry is before 2999 on both. So when the decision changed (posting date to
value date), the approved test could not tell the two apart. Only implementing
the change exposed it. Test 10 had the same flaw for ordering: every dated
entry shared one value date, so posting order and value-date order were the
same list. Both now fail on the other basis (`period_by_posting`,
`posting_order`).

Same shape as the fixtures that could not distinguish, except that this one
had been reviewed.

**Habit: an approval does not make a test discriminating.** For every test,
ask what it would do if the decision had gone the other way. If the answer is
"pass", it is documentation, not a test. Make the other way a named break and
watch it go red.

## Findings 14 and 15: approvals were a record, not a control (2026-10-05)

Found while writing the loan tests.

- **14.** `approval_requests.state` was a free column. The API role held
  `UPDATE (state)` (grants.sql) and no rule tied it to signatures: a request
  could read `approved` with none. PRD 8.4's multi-signatory control rested on
  a column anything could write.
- **15.** Signing checked neither the request's expiry nor its state.

**Decided: fix the column, not each reader.** Loans first routed around it;
that is a workaround every future consumer would have to repeat, and one
eventually would not. Suite **0007** (tests first, for review, ahead of loans):
the status is DERIVED (`approval_status(request, at)`) from signatures,
rejection rows and expiry, and nothing stores or writes it; a rejection is its
own row (author, post, server time, one of Stage 5's four reasons); signing
refuses an expired or rejected request. Loans (now suite 0008) trust
`approval_status`.

## Finding 16: a rule that worked as the owner and broke the app (2026-10-06)

0007's signing trigger locks the request row (`SELECT … FOR UPDATE`) so a
signature and a rejection cannot race. A row lock needs UPDATE privilege; the
trigger ran with the caller's rights; and 0007 had just removed the app's only
UPDATE grant on that table. **After 0007 the API could not sign at all.**
Suite 0007 was green, because the suites run as the owner. The probe, which
seeds and runs as `argon_app`, refused it on its first line. Fixed as 0005's
rules are: `SECURITY DEFINER` with a pinned `search_path`, so the lock is the
rule's need and the app still holds no UPDATE. The probe gained P18–P20 (the
app cannot move a request or rewrite a rejection) and K18, and P18 was shown
to fail when the grant is put back.

**Habit: a suite run as the owner proves the rule, not that the app can live
with it.** Whenever a migration changes grants, or a rule takes a lock or
reads a table the app cannot, run the probe as the app before calling it green.

**Also from 0007 (a test of mine):** a control that cannot pass against M0 is
not a control; it is a rule test in the wrong column. "Two of three
approves" first went in as a control; on M0 nothing ever reads `approved`, so
it failed there, and it became rule test 16. Same family as controls that go
red because a rule broke.

## Finding 17: a sabotage that removed its own precondition (2026-10-06)

To prove the new loan probe cases could fail, two permissions were broken in
one run: the app lost INSERT on `loan_instalments` (K19 must fail) and gained
UPDATE on `loans` (P21 must fail). K19 failed, so no loan was created, so
P21's `UPDATE loans …` matched zero rows and "succeeded". P21 reported a pass
that proved nothing: the same sabotage had removed the row it needed. Run alone,
the P21 sabotage failed the probe as it should (and the immutability rule
still refused underneath).

Same family as the test lister that found zero tests and the suite that ran
against a broken fixture: a check that passes because its precondition is
gone. P21 now carries a note that it needs K19's loan.

**Habit: when sabotaging, break ONE thing.** With two broken at once, the
result is about their interaction, not about the rule under test.

**Kept deliberately: P21 holds at two layers.** The app has no UPDATE on
`loans` (the grant), and `loan_agreement_immutable` refuses an UPDATE from
anyone (the rule). Either alone is enough; both mean a future grant change
does not silently open it.

## Stated gap: 0007's rollback has never carried real approvals

`0007_approvals.down.sql` rebuilds the old `state` column from the derived
status, so a rollback does not reset every request to `awaiting`. It was run
once, on a scratch database with almost nothing in it (2026-10-06): it
applied, and 0007 re-applied after it. The step that rebuilds `state` from
real signatures and rejections has never had any to carry. A rollback is run
exactly once, under pressure, on a full database, which is the one condition
it has not been tested in. Before relying on it, run it against a restored
copy of the live database (the drill's restore gives one) and compare each
request's rebuilt `state` with `approval_status` taken before going down.

## Finding 19: a borrower could sign out their own loan (2026-10-06)

0008 barred a loan's guarantors from signing any request for it, and never
its borrower. A treasurer who borrowed could sign the disbursement of their
own loan: the clearest possible failure of the multi-signatory control. It
survived until the write-off rules (which bar the borrower) made someone look
at the disbursement next to them. **Decided:** the borrower never signs ANY
request for their own loan (suite 0009, test 6).

## Finding 20: a broken version caught in setup reads as a broken fixture

Writing suite 0009, an over-strict mutant (only loans past their schedule may
be written off) refused the write-off REQUEST, which a control raised through
a fixture helper. The runner reported "BROKEN FIXTURE", not a control that
caught an over-strict rule. Fixed by raising the request inside the control's
own `accepted(...)`. The same reading problem as a test red for the wrong
reason, one phase earlier. **Habit:** a statement a control exists to prove
must be asserted, not run as setup; anything an over-strict rule could refuse
belongs in the assertion.

## Finding 21: a rollback that left residue its own re-apply tripped on (2026-10-06)

0009 adds two enum values. Its down cannot remove them (PostgreSQL cannot drop
an enum value), and says so. But nobody had re-applied 0009 after its down:
the round trip (up, down, up) failed at the first statement, `enum label
"loan_write_off" already exists`. A rollback followed by a fix-forward, the
usual sequence under pressure, would have died there. Fixed with
`ADD VALUE IF NOT EXISTS`. Round trip now tested 9 → down → up → down, then
8 and 7 down, then 7, 8, 9 up again, on scratch.

**Habit: a down-migration is tested by re-applying the up after it, not by
the down succeeding.** Whatever a down cannot undo is exactly what the next
up meets.

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
`correction_same_society`, `ledger_line_account_in_society`, `signature_before_expiry`,
`signature_on_open_request`, `rejection_reason_known`, `rejection_note_for_looks_wrong`,
`rejection_rejected_at_is_now`, `rejecter_user_is_member_user`, `rejecter_holds_signing_post`,
`rejection_on_open_request`, `rejecter_has_not_signed`, `approval_rejections_immutable`,
`resubmits_a_rejected_request`, and for loans (0008) `interest_method_undecided`,
`loan_terms_are_products`, `loan_term_within_product`, `loan_within_savings_multiple`,
`loan_borrower_in_society`, `loan_product_in_society`, `loan_agreement_immutable`,
`loan_schedule_sums_to_principal`, `loan_schedule_matches_term`, `loan_schedule_in_order`,
`loan_schedule_immutable`, `loan_schedule_closed`, `guarantee_is_own_act`,
`guarantee_act_is_guarantee`, `guarantor_user_is_member_user`, `guarantor_not_borrower`,
`loan_guarantees_one_per_person`, `guarantor_member_of_society`, `signatory_not_guarantor`,
`guarantee_withdrawal_is_own_act`, `guarantee_withdrawn_before_disbursement`,
`loan_guarantees_immutable`, `loan_request_matches_loan`, `loan_guarantors_met`,
`loan_movement_names_loan`, `loan_id_only_on_loan_movements`, `loan_same_society`,
`loan_disbursement_approved`, `loan_disbursed_once`, `loan_disbursement_is_principal`,
`loan_line_is_borrowers`, `loan_repaid_after_disbursement`, and for write-offs (0009)
`write_off_quorum_unset`, `write_off_quorum_is_societys`, `write_off_needs_money_owed`,
`write_off_request_is_outstanding`, `write_off_states_reason`, `borrower_never_signs_own_loan`,
`write_off_approved`, `write_off_once`, `write_off_is_outstanding`, `write_off_to_loss_account`,
`loan_written_off`, `recovery_needs_write_off`, `recovery_never_reopens_loan`, `write_off_quorum_sane`.

A trigger reports its name with
`RAISE … USING ERRCODE = 'check_violation', CONSTRAINT = '<name>'`.
