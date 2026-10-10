# Ledger contract

**For whoever builds the backend.** This states obligations, not intentions.
Every "must" here is load-bearing: each one exists because of a specific way
this goes wrong, and the reason is given so you can argue with it rather than
guess at it.

The frontend is written against this. Where the frontend cannot enforce
something, it says so and the obligation moves to you.

**Money is in kobo. ₦1 = 100 kobo. Never a float, anywhere, at any layer.**

## 0. Money on the wire (decided 2026-09-30)

**One shape, both directions:** an amount is a **decimal kobo string, signed.
Positive is a debit, negative a credit.** `"2500000"`, `"-2491250"`. Never a
JSON number, and never a pair of fields.

- **Signed, not split `debitKobo`/`creditKobo`.** With a signed amount,
  "balanced" is a sum to zero, and a sum cannot be got wrong by filling in the
  wrong field. Split fields also need a rule for when both are populated; a
  signed integer cannot be in that state. The database stores signed kobo in
  one column for the same reason, and two shapes for one fact across the API
  boundary is the two-sources defect where it is hardest to see.
- **A string, not a number.** A JSON number is an IEEE-754 double. It would
  break the no-float rule at the one layer nobody inspects, and it would never
  visibly fail.
- **Parsed at the port boundary, on both sides.** A bare JSON number is
  **rejected, not coerced**. The backend's `money.Kobo` already does this.
- **A movement's AMOUNT is not a line's signed value.** Anything that takes
  "how much" (the guard, the charge rule) takes a non-negative magnitude, and
  refuses a negative rather than treating it as small. A reversal's charge
  line is a negative *line*, computed from the original's charge, not from
  `chargeFor(−amount)`.

This contract is what the frontend changes to. The frontend's split shape came
from its demo store, not from a decision.

---

## 1. `postAll` is one transactional call

```ts
postAll(reference: string, lines: PostedLine[]): Promise<void>
```

**Obligations**

1. **All-or-nothing.** Either every line in `lines` is written, or none is.
   There is no partial outcome.
2. **`reference` is the idempotency key.** A second call with a reference that
   has already been committed writes nothing and is not an error. It returns
   the record of what the first call did — see §2.
3. **The reference is minted by the client** from a SHA-256 of the uploaded
   file's content (`lib/importReference.ts`). You never generate one. A retry
   of one upload carries the same key; a corrected file carries its own.
4. **A failed call writes nothing and does not mark the reference committed.**
   A retry after failure must be able to succeed.

**If you can only accept rows individually, this guarantee is not achievable
at the client and the requirement is yours.** Posting 19 rows one at a time and
deleting the first 13 when the 14th fails is compensation, not a transaction —
the deletes can fail too, and on a 3G connection they will. The frontend
guarantees only what it can: one call, never a loop, verified by test. Say so
early if a batch endpoint is not on the table, because the alternative is a
half-migrated society with no way to tell which members were done.

**The port is currently synchronous** in `lib/importCommit.ts` because there is
nothing behind it. It must become `Promise`-returning when a real adapter
exists, and `commitImport` with it. That change is expected and is the
frontend's to make — flagged here so it is not a surprise.

---

## 2. Immutability: there is no update operation

A posted `LedgerEntry` is never edited. A correction is **a reversal and a
repost**, or **an adjusting entry**.

**The port must therefore offer no update operation at all** — not a disabled
one, not one that throws, not one behind a permission. *An operation that
exists is an operation someone eventually calls.* If `updateEntry` is on the
API surface, some future integration, migration script or support tool will
reach it at 2am, and the audit trail will have a hole nobody can date.

Permitted write operations, and no others:

| Operation | What it does |
|---|---|
| `postAll(reference, lines)` | Writes a batch. Atomic. Idempotent on reference. |
| `postReversal(originalId, reason, actor)` | Writes a new entry that mirrors an existing one and links to it. |
| `postAdjustment(lines, reason, actor)` | Writes a new balancing entry. Used when the original period is closed. |

Reversals and adjustments are **new entries with new ids and their own posting
timestamps**. They never alter the original. The original's `reversedBy` is a
derived read, not a mutation — if your storage requires writing a pointer back,
that pointer is the single exception and must be append-only and audited.

---

## 3. Every member-facing transaction is a balanced double-entry pair

**The trial balance sums to zero by construction, not by reconciliation.**

Obligations:

1. A transaction is rejected at write time if its debits do not equal its
   credits. Not flagged, not queued for review — **rejected**.
2. The sum is computed in kobo integers. No rounding step may occur between
   computing the lines and writing them.
3. A charge is never netted into the principal. The gross, the charge and the
   net are **three lines**, because a member reading a statement must be able
   to see where the difference went. This is the same rule as the confirmation
   screen, one layer down.
4. Zero-amount lines are not written. A free movement produces no charge line
   at all, rather than a line for ₦0 — see `opening_balance`.

If a transaction cannot be expressed as balanced entries, that is a modelling
gap to raise, not a case for a single-sided entry.

---

## 4. Attribution is point-in-time

Every state-changing action is attributable to a specific user.

**Obligations**

1. Every entry carries `actorUserId`, `actorRoleAtPosting`, and `postedAt`.
2. **`actorRoleAtPosting` is snapshotted onto the entry at write time. It is
   never resolved by looking up who currently holds the post.**
3. Officer handover creates a new appointment with its own date range. It must
   not rewrite, re-key or re-attribute any existing entry.

**Why this is not a detail.** A March disbursement signed by March's treasurer
must still read as signed by March's treasurer after a handover in June. If
attribution resolves through "who is treasurer now", a handover silently
rewrites the past — every historical signature reassigns itself to a person who
was not there, and the audit trail breaks retrospectively, for every prior year
at once. A cooperative's defence in a dispute is that the books say who did
what and when. Resolve-at-read-time destroys exactly that.

Approval signatures carry the same rule: who signed, in what post, at what
timestamp, frozen at signing.

---

## 5. What the read side owes the accounting surface

Derived from §10 of the build specification. Specified now rather than
discovered while building.

| Read | Returns | Notes |
|---|---|---|
| `chartOfAccounts()` | code, name, type, whether postable | Types: asset, liability, equity, income, expense. Non-postable parents exist for grouping. |
| `trialBalance(asOf)` | every account with debit and credit totals | **Must sum to zero.** If it does not, that is a defect to surface loudly, never a variance to display. |
| `generalLedger(accountCode, from, to)` | entries with a running balance | Ordered by **value date, then posting time, then entry id**. Deterministic, and in the books' order. |
| `journal(filter)` | entries by reference, member, date range, actor | The reference filter is how an import is audited after the fact. Same order. |
| `memberStatement(memberId, from, to)` | entries with running balance | What a member reads as a receipt. Charges appear as their own lines. Same date basis and order as the books. |

**Every read uses the VALUE DATE (decided 2026-10-05)**: the date the movement
happened in the society's books, the date period close uses. `asOf`, `from`
and `to` are value dates. An undated entry (an opening balance "accumulated
before migration") is **before every date**. A member's statement and the
society's ledger therefore never disagree about which entries fall in a
period. Ordering by posting time, which this table said until then, was
deterministic and wrong: it disagreed with the books.

**Known gap: no pagination.** Every read returns the whole result. That is
right today. It becomes urgent at the **first society with a year of daily
collections** (hundreds of members, every day, for a year), not at some row
count: plan cursor pagination (value date, posting time, entry id, the order
above) before that society is onboarded.
| `reports(period)` | contributions by branch, loans outstanding, charges collected | Charges split partner vs margin — see §6. |

**Balances are DERIVED from entries, never stored as the authoritative figure.**
A cached or materialised balance is permitted for speed on one condition: it
must be reproducible exactly from the entries, and it must carry the point it
was computed to. A stored balance that can drift from its entries is precisely
the failure the journal exists to prevent.

**Period close.** Closing a period makes it read-only: no entry may be posted
with a date inside a closed period. A correction to a closed period posts in
the current period as an **adjusting entry** referencing the original. A close
produces a trial balance snapshot, and the close is refused if that snapshot
does not sum to zero.

---

## 6. Open questions — owners, not assumptions

| Question | Owner | State |
|---|---|---|
| Charge posting rules | Engineering + Tier 1 accountant | **Must join the B-29 review.** Which accounts charges post to, and when they are recognised, is not settled. |
| Partner charge vs Argon margin recorded separately | Commercial | **Must be separate.** See below. |
| VFD balance vs Argon ledger: which is the record of account | Legal + Product | **DISPUTED** (2026-10-02): two contradicting claims in this project, the original source unavailable. See below. Blocks anything that reconciles against VFD. |
| Does a reversal refund the Argon charge? | **Commercial** | **D-02: answered, assumed-pending.** The charge is refunded with the principal. Assumed until Commercial confirms; **blocks shipping.** See below. |
| **Argon's margin on a regulated fee** | **Legal** | **OPEN (2026-10-09).** If the CBN guide sets a CEILING on what may be charged for a transfer, the question is not "may Argon add a margin on top" but "is Argon's total charge within the cap". That depends on whether Argon charges as a financial institution in its own right, or passes through VFD's fee as the licensed entity. The two have different answers, and the second is our structure. Until answered, a margin can be configured and is refused until set (§9). |
| Loan interest calculation method | The loan rate's owner | **OPEN (2026-10-09).** Flat on the principal, or on the reducing balance. Separate from movement charges (§9). Until decided, 0008 refuses any rate above zero. |
| Opening balance contra account | Tier 1 accountant | `opening_balance` posts at zero charge as its own movement kind. What it debits is §7's assumption, not a decision. |

### The reversal charge is a fairness question before it is an accounting one

Whoever owns this should see what they are actually deciding.

A treasurer posts a member's ₦25,000 contribution twice by mistake. The member
is charged ₦87.50 on each. The duplicate is reversed. **If Argon retains the
charge on the reversal, that member is out of pocket for the society's error** —
they did nothing, they were not asked, and the fee is for a transaction that no
longer exists in their statement.

This is not an edge case, and it is not confined to duplicates. **The same
shape recurs wherever money is undone:**

- a dispute resolved in the member's favour
- a cash contribution recorded against the wrong member and moved
- a payroll remittance reversed after a reconciliation error
- any correction to a figure the member never entered

In every one of them the member bears a cost for someone else's mistake, and
the amount is small enough that nobody will escalate it and frequent enough to
be noticed. A cooperative exists on the belief that its books are fair to
members; this is a recurring, visible, tiny charge that says otherwise.

The decision therefore is not "which account does the reversal credit". It is:
**who bears the cost of an error a member did not make?** Options are to refund
the charge with the reversal, to absorb it against the society, or to retain
it. Each is defensible, and the accounting follows the choice rather than
driving it.

**D-02 answers it, assumed-pending: the charge is refunded with the reversal.**
A member is not out of pocket for a treasurer's error. The owner is Commercial,
and until Commercial confirms, this is an assumption that **blocks shipping**
— not a settled rule. `lib/ledgerFixture.ts` and `internal/ledger/port.go`
both model the refund case. The entries differ materially between the three
options, so if Commercial chooses otherwise, §7c and both of those change.

---

**Partner charge and Argon margin must be recorded on separate lines.** Summed
into one total, the pass-through-versus-margin distinction is unrecoverable
from history — you cannot later ask what Argon actually earned, because the
answer was never written down. Transaction charges are the only revenue line in
the product, so this is the only place that number exists.

> **DISPUTED (2026-10-02). Do not build on either claim.** This project holds
> two contradicting statements of which balance is the record of account:
> `lib/decisions.ts` D-04 as first written says **Argon's ledger**; the same
> file's D-19 note said **the VFD balance is the record of account and Argon's
> ledger the book of account**. Both, and the paragraph below, were carried
> from the Step 0 B-04 spike as *summarised*; the original is unavailable to
> both repositories. The answer comes from whoever ran B-04, not from reading
> documents. Blocks anything that reconciles against VFD.

**VFD balance vs Argon ledger.** They will periodically disagree: in-flight
transfers, provider fees, settlement timing. **Adjusting the books to match VFD
would destroy the thing the ledger exists for.** The ledger is the record of
what the society decided and recorded; VFD's balance is the record of what a
payment provider currently holds. A difference between them is a
*reconciliation item to be explained*, never a correction to be applied.

> Carried from the Step 0 B-04 finding as summarised to me. I do not have the
> original text in front of me — check this paragraph against it before this
> document is treated as settled.

---

## 7. Worked example

Figures in kobo. `₦` shown for readability only.

### 7a. Member contribution of ₦25,000

`contribution_in`, 0.35% capped at ₦200, borne by the member.
Charge = `min(20_000, round(2_500_000 × 0.0035))` = **8,750 kobo (₦87.50)**.

| Account | Dr | Cr |
|---|---:|---:|
| Bank / cash | 2,500,000 | |
| Member savings — ARG-0142 | | 2,491,250 |
| Charges payable — partner (VFD) | | *x* |
| Charges payable — Argon margin | | *y* |

`x + y = 8,750`. The split is the open commercial question in §6; **two lines
regardless of the split**, even if one is currently zero — in which case that
line is omitted, not written as ₦0.

Debits 2,500,000 = credits 2,491,250 + 8,750. ✅

The member's statement reads: contributed ₦25,000, charge ₦87.50, credited
₦24,912.50. Three figures, because a member who sees only ₦24,912.50 arrive
against a ₦25,000 payment reads it as money going missing.

### 7b. Opening balance of ₦480,000 (migration)

`opening_balance`. **Zero charge, deliberately** — see `lib/guard.ts`. Charging
on a migration takes a cut of money that was in the society's books before
Argon existed.

| Account | Dr | Cr |
|---|---:|---:|
| Migration control | 48,000,000 | |
| Member savings — ARG-0142 | | 48,000,000 |

No charge lines at all. Debits = credits. ✅

`asOf` is the date the society's records say the balance is accumulated to,
supplied once for the whole import. **It is never the import date.** A member
whose savings accrued over seven years must not have a ledger that begins on
the day the software changed.

Migration control accumulates the total of every opening balance and is
reconciled against the society's actual position as a separate, explicit
exercise. It is not expected to be zero.

### 7c. Reversal of 7a (posted in error)

A **new** entry. 7a is untouched.

| Account | Dr | Cr |
|---|---:|---:|
| Member savings — ARG-0142 | 2,491,250 | |
| Charges payable — partner (VFD) | *x* | |
| Charges payable — Argon margin | *y* | |
| Bank / cash | | 2,500,000 |

Carries `reverses: <id of 7a>`, a written reason, and its own actor and
timestamp. Debits = credits. ✅

**This is D-02: the charge is reversed with the principal** (assumed-pending,
owner Commercial — §6). If Commercial decides Argon retains its charge on a
reversal, the credit to bank becomes 2,491,250 and the charge lines drop out —
a different set of entries. Shipping waits on that confirmation.

---

## What the frontend already enforces

So you know what is already tested and need not be re-litigated:

- Money in kobo, one formatter, no floats — `lib/guard.ts`
- `opening_balance` at zero charge as its own movement kind
- Reference minted from file content, stable across retries — 8 tests
- Commit: re-derives its own total, one atomic call, idempotent on reference,
  refuses rather than partially posting, freezes every line — 20 tests
- No edit action offered on anything posted, anywhere in the interface
- The as-at date asked once and carried, never defaulted to today

---

## 8. Decided 2026-10-07: what a member may withdraw, charges, and who authorises

**Withdrawable (D-10)** = savings − guarantees given − account floor − project
commitments. Two of its terms had no value, so:
- **The account floor is a society setting with no default.** Until a society
  sets it, withdrawable is not stated, and the reason says why: "this society
  has not set its account floor". The savings balance is always shown.
- **Guarantees given: a CONSERVATIVE DEFAULT, pending the guarantor-liability
  decision, not the answer.** Until liability is decided, the FULL outstanding
  balance of every loan the member guarantees is held back. It is the most
  conservative reading, it is a real number, and it can only move in the
  member's favour when the decision lands. The member sees a smaller figure
  with its reason ("₦X is held while you guarantee {name}'s loan"), never "no
  figure": a figure withheld because of someone else's undecided question
  reads as the product being broken.
- **Project commitments read zero, stated,** until projects exist.

**Charges on a movement are configuration with no default, refused until set.**
The server computes them; it never accepts a client-computed charge amount.
That is the weakest point in a money path. **Known gap, to close when the Board
decides (q4):** `postAll` today accepts charge lines computed by the client
and checks only their structure. It is the exception to narrow, not the
pattern to copy.

**Every withdrawal that moves money out of the society needs its signatories**
(PRD 8.4). The guard threshold is interface friction: it decides what a person
is asked to type or hold, not who must authorise. Conflating the two would let
a society set a high threshold and quietly make most withdrawals unilateral.
**Possible refinement, a bye-law decision we do not invent:** a lower signature
count below an amount, as with the write-off quorum bands.

**Signing and rejecting a request need the signer's own PIN step-up**,
consistent with imports and guarantees. A signature is the most attributable
act in the product.

## 9. Decided 2026-10-09: how charges are configured, who bears them, when a withdrawal is posted

**Charges are banded configuration with no default, refused until set.**
- **Per society and movement kind:** a schedule, effective from a date, of amount bands. Each band has a flat fee and an optional rate and cap (the cap limits the rate part), for the partner and for Argon's margin **separately**.
- **Rounding:** a rate rounds half up to the kobo.
- **The edition is a required field:** a schedule names the edition of the regulation its figures were taken from (for example "CBN Guide to Charges, 2020 edition"), so a later reviewer can see what the numbers came from and whether it still applies.
- **Nothing is seeded:** a default that becomes stale regulation is worse than a refusal, because nobody reviews a value that is already there.
- **Who sets it:** an operator, as the owner, until the platform's Finance role exists. The app only reads it.
- **Corrections:** a schedule is never edited; a correction is a new schedule.

**Sourced-and-unverified:** the CBN figures cited when this was decided were never confirmed:
- electronic transfers ₦10 below ₦5,000, ₦25 to ₦50,000, ₦50 above;
- over-the-counter cash withdrawals free within limits;
- a 2026 revision in draft (circular of 21 April 2026).

They appear only as illustrative test data, marked so, pending the compliance owner confirming the edition and the bands. The shape is right regardless of whether the numbers are.

**Argon's margin can be expressed and is refused until set.** It is never assumed to be zero, nor any figure. That waits on Legal (§6).

**The fee is ON TOP for withdrawals and transfers.** The member receives what they asked for, and amount plus fee must fit within withdrawable. Deducting it would make a member asking for ₦50,000 receive ₦49,950: the undisclosed-gap problem in a different place.

**Contributions carry no charge by default.** A society with no contribution schedule charges none, though the configuration can charge them. §7a assumed a charge; the regulation does not require one.

**An approved withdrawal is posted when an officer records the payout,** on the officer's own PIN step-up, naming the transfer that paid it. Approval authorises; nothing is posted for money not yet paid. A single contribution rests on its poster's own step-up, as an import does.

**Two rates, kept apart.** The movement charge above is not the loan interest rate. The loan rate belongs to loan writes and still needs a calculation method, flat on the principal or on the reducing balance, from the loan rate's owner.
