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
| `0010` | `tier1_0010_test.go` (12, a signature and a rejection rest on a step-up) | `0010_interface.sql` (M0) + `mutants_0010/` (12) | `migrations/0010_sign_step_up.up.sql` |
| `approvals_api` | `tier1_api_approvals_test.go` (18, view, sign, reject; the fraud path) | the breaks in `apifake_approvals_test.go` (`--svc`) | `internal/api/approvals.go` |
| `money_api` | `tier1_api_money_test.go` (35: record a contribution, authorise a payout (the hold), release a hold, record a payout; who may, the step-up, the server's charges, the database's reasons and flags in the answer; every act the schema knows can be stepped up for; 0015: the Idempotency-Key, conflict_of_interest, duties_separated) | the breaks in `apifake_money_test.go` (`--svc`) | `internal/api/money.go` |
| `me_api` | `tier1_api_me_test.go` (9: who the caller is, the join between sign-in and every other endpoint (finding 40); the caller's own member records and current posts only; empty lists, never null; 401 without a live session) | the breaks in `apifake_me_test.go` (`--svc`) | `internal/api/me.go` (after review) |
| `requests_api` | `tier1_api_requests_test.go` (19: raise a withdrawal request (the member, or the treasurer or secretary for them); the key, the step-up, the figures, resubmission; the raiser never signs; the member's list and the society's, in their projections) | the breaks in `apifake_requests_test.go` (`--svc`), on a base carrying `0016_reference.sql` until the migration exists (guarded) | `internal/api/requests.go` (after review) |
| `0011` | `tier1_0011_test.go` (31 + 4 controls: four-component charge configuration, the server's charge, each charge on its own account, a single movement's step-up, the quorum, nobody signs their own; the payout and withdrawable tests moved to 0012 on 2026-10-13) | `0011_interface.sql` (M0) + `mutants_0011/` (35) | `migrations/0011_money_movements.up.sql` |
| `0012` | `tier1_0012_test.go` (28 + 4 controls: the payout hold, checked before money moves; the recording, whatever happened, any difference flagged; contributions recorded when their charge cannot be determined) | `0012_interface.sql` (M0: 0011's recording-time judgements) + `mutants_0012/` (32) | `migrations/0012_payout_holds.up.sql` |
| `0013` | `tier1_0013_test.go` (6 + 1 control: VAT on Argon's own fee, not the partner's, in the quote and on a hold; the old VAT names refused; a vat line only on the VAT account) | `0013_interface.sql` (M0: the rename alone, VAT still on the partner's fee) + `mutants_0013/` (6) | `migrations/0013_vat_on_argon_fee.up.sql` |
| `0014` | `tier1_0014_test.go` (23 + 5 controls: the operator's functions, for every governance setting, name the operator and the server login, say why, and log every change append-only; a society's setting carries its resolution, never dated in the future; every quorum (withdrawal, write-off, signature count) is at least 2 and the refusal says why; the guard threshold has no minimum (interface friction, not authorisation); the lifecycle takes no resolution; a charge series is never backdated) | `0014_interface.sql` (M0: the functions change the setting and nothing else) + `mutants_0014/` (25) | `migrations/0014_operator_settings.up.sql`; the operator's script, `deploy/45-society-settings.sh`, is tested in `test/ops/` |
| `0015` | `tier1_0015_test.go` (8 + 5 controls: nobody authorises or attests to a movement they benefit from (the hold, releasing it, a recorded payout, a recorded contribution; per person); the officer who authorised a hold never records its payout; a single contribution names its client reference, recorded once per society) | `0015_interface.sql` (M0: the client reference's column, no rules) + `mutants_0015/` (10) | `migrations/0015_benefit_and_duties.up.sql` (after review) |
| `0016` | `tier1_0016_test.go` (15 + 3 controls: a withdrawal request names its raiser and rests on the raiser's own step-up, its act present, the raiser's own, a withdrawal_request act, from this transaction, used once; a request no quorum can approve refused when raised, with the remedy; raised by the member or an officer for them; the server sets the expiry from the society's window (seven days shipped; the operator sets it, logged, at least a day); within withdrawable when raised, with the figures; the raiser never signs; a resubmission is for the same member) | `0016_interface.sql` (M0) + `mutants_0016/` (19) | `migrations/0016_withdrawal_requests.up.sql` (after review) |

**Was temporary, and guarded (2026-10-14 to 2026-10-15):** `money_api`'s base carried 0015's interface until its migration existed, with the fake checking 0015's rules meanwhile. `suite_money_api.sh` refused to run once the migration appeared while its base still named the interface; the guard fired as designed and the base now names the real 0015.

**Suites 0011 and 0012 follow the schema's VAT era (2026-10-13).** Their deliberate breaks run on schemas before 0013, their GREEN and the drill at head, so their charge fixtures read the era from the schema (`vatOnArgonsFee`): the VAT names, and which component carries the illustrative ₦10/₦50 (the partner's before 0013, Argon's from it; from 0013 the partner's is zero AS TEST DATA ONLY, labelled so in the fixture). Their tests assert the same rules in both eras (`asFee`). Proved: both suites pass on today's schema with every break, all 480 tests pass against the 0013 reference, and with the era forced to the old one at schema 13, 63 of 67 money tests fail on the renamed component's check.

**Suite 0011's fixture moves (2026-10-09), each proved.** Where the 0011 columns exist:
- a single contribution posted through `insertEntry` carries its poster's own step-up act (`movementAct`); an actor who is no user takes none, so TestU5 keeps its foreign-key refusal;
- the test society decides its withdrawal quorum (two, the count its requests are raised at);
- every withdrawal-request fixture names its member;
- the read-side fixtures that post §7a's charges configure the schedule that produces them (`chargesLike7a`, `chargesContributions`).

Against 0001–0010, the 0011 interface and a reference implementation of every rule, all 456 tests pass (the API suites as the app roles; revised to four components 2026-10-12). Each move was then sabotaged on its own:
- no act: 143 tests fail, every one on "rests on its poster's own PIN step-up";
- no quorum: 70 fail on "has not decided how many signatures";
- no member: 50 fail on "names the member whose savings it moves";
- the read-side fixtures configuring no charges: 52 fail on "by the series this contribution_in ... is charged".

Three existing fixtures had to change because they posted charges no schedule produced, which is exactly what the rule refuses: TestRole_Control_ContributionWithEveryRoleRight, TestLineSociety_Control_OwnAccountsAccepted and TestApiStatement10 (that test is about order, not amounts).

**A rule that covers for another (stated, not hidden):** under the mutant that lets a payout name no request, TestMoney28 is still refused, by the commit-time "matches its request" rule. The protection exists twice. Test 28 proves the refusal's reason, not that it is the only thing standing.

**Two more found while revising, both fixed so only the rule under test can refuse:**
- TestMoney22 (a levy line belongs on the levy account) first put the levy on the VAT account. The VAT account's own rule (it carries only VAT) refused it too, so the test passed under the mutant that drops the levy rule. It now uses an account no charge rule guards.
- TestMoney31 (a request is paid once) was refused the second time because the money had run out, not because the request had been paid. The member now has savings enough for two payouts.

**0012 as the app (the probe, 2026-10-13).** As argon_app:
- the app cannot supply a hold's charges (P33), and authorises a payout on its own step-up (K29);
- it records the held payout (K27), and cannot record the same transfer twice (P32, its meaning since 0012);
- a second, different transfer on one hold is recorded (K28) and flagged with what was expected and what happened (P34);
- it cannot write a flag away (P35).

Each new grant was widened on its own: table-wide INSERT on holds turned P33 red (refused by the hold's rule instead of the role), and DELETE on flags turned P35 red. Flags carry expected, observed, when and the hold; a mute flag (`m_flag_mute`) and an undetermined charge reported as zero (`m_undetermined_as_zero`) are mutants that turn their tests red.

**0011 as the app (the probe, 2026-10-13).** As argon_app:
- the app cannot write a charge series (P25), and cannot set any quorum or the floor by UPDATE (P26, P28–P30) or by creating a society that carries one (P27);
- it can still correct a society's name (K26);
- it cannot record a contribution without a step-up (P31), and can on its own act (K25);
- it pays out an approved withdrawal, charges on top (K27), once (P32).

Each grant was put back on its own, in the commit that added the checks: table-wide UPDATE on `societies` turned P26 and P28–P30 red and left P27 holding; table-wide INSERT turned only P27 red (taking away UPDATE alone would have looked closed); INSERT on charge schedules turned only P25 red. The probe's own balance cases (C3, K3) now carry the poster's act, so C3 meets only the balance rule.

**Habit (2026-10-12): after splitting one rule into several, re-ask of every test which rule actually refuses it.** A split turns each new rule's neighbours into potential cover, and the split itself created test 22's cover, in a suite already proven once. Re-proving is not enough, because the suite passes either way. Read each mutant's red REASON: a WRONG REFUSAL means a neighbour covered for the rule under test.

Signatures and rejections in every fixture go through `onStepUp`
(`tier1_0002_test.go`): where the schema has `act_id` (0010), each rests on
the person's own step-up act, taken in the same transaction, so only the rule
a test is about can refuse it. A member record with no person behind it can
take no step-up and signs with none (TestU3). There, 0002's "no person"
refusal must win, so 0010's triggers must be named to sort AFTER the existing
ones (`signatory_approvals_step_up`, `approval_rejections_step_up`). The
mutants' `m_t` sorts first, and against them TestU3 sees the step-up refusal.
Proved 2026-10-07: run with 0010's "reusable" mutants, the 0002/0007/0008/0009
tests pass except TestU3 (the ordering above). With the helper sabotaged to
skip acts, 50 go red under the signature rule and 9 under the rejection rule.

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

**The habit did not hold: finding 25 is finding 16 recurring (2026-10-07).**
0010's new `act_id` column was not in argon_app's INSERT lists, and a mutant
read a column the app may not read. The cause and shape were the same: the
suites run as the owner, so a correct rule the app cannot execute passed
everything. It was caught only because the probe happened to be run. So it is
now a STEP: when a suite's GREEN target is a migration, `mutate.sh` runs the
probe as argon_app after GREEN, and a probe failure fails the run, as a
surviving mutant does. Proved: with the app's INSERT grant on
`signatory_approvals` removed, suite 0009 went 18/18 GREEN and the run FAILED
on `permission denied for table signatory_approvals`. With the grant back, it
passed. A migration that adds a column, trigger or function the app depends on
also adds the probe cases that exercise it as argon_app.

**The same step one layer up (2026-10-08): the API suites' GREEN runs the real
server as the roles production gives it.** Hand-checking the handlers' reads
as argon_app was how findings 16 and 25 got through, so it is replaced, not
relied on. Now in GREEN:
- grants.sql is applied to the test database;
- the real handlers run on an argon_app connection, and the real auth service
  on an argon_auth connection, as cmd/api does (`roles_test.go`);
- each role is read back from the server (`current_user`), not taken from the
  URL;
- fixtures are still written as the owner, because they are the world the
  server finds, not what it does;
- the fakes keep the owner's connection, because grants.sql fits only the
  real schema.

There is no quiet fallback: a real-server test with no role connections fails
as a broken fixture, naming what to set. The drill does the same against its
restored copy's grants.

Proved in the commit that adds it, one fault at a time:
- **(a)** argon_app loses SELECT on `approval_rejections`. approvals_api GREEN
  goes red, signing and reading answer 500, and the run fails. The owner-run
  suite had been 18/18 against the same grants.
- **(b)** No role URLs. 144 API tests fail, each naming what to set. The one
  that passes tests the SMS refusal and uses no server.
- **(c)** A broken grants.sql. The run says "GREEN SETUP FAILED" and gives the
  database's error.

It found one fixture the owner had been covering for. The batch suite's
fault injector counted its hits in sequences only the owner may use, and the
fault now fires inside argon_app's transaction. The instrument now runs with
its creator's rights (SECURITY DEFINER); the app's grants are unchanged.

**And a defect in the instrument, as every new one has had:** GREEN's setup
failing (the first attempt: grants.sql revokes on migrate's `schema_migrations`,
which the file-by-file schema did not have) printed "NOT RUN" once per test and
no reason. M0 and the mutants already said "SCHEMA DID NOT APPLY" with the
reason; GREEN now says "GREEN SETUP FAILED" with it (proved by (c)). The test
database now gets `schema_migrations` as migrate creates it.

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
