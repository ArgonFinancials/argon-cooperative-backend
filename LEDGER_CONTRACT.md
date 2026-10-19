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
| VFD balance vs Argon ledger: which is the record of account | Legal + Product | **CLOSED 2026-10-13 (§11).** Argon's ledger is the record of account of what the society decided; VFD's balance is the bank's record of what it holds; neither is adjusted to match the other; a difference is an item to explain. Settled by evidence from the running wallet platform, not by the B-04 summary, **which described a spike nobody ran** (finding 32). Carries an obligation: scheduled reconciliation against VFD (§11). |
| Does a reversal refund the Argon charge? | **Commercial** | **D-02: answered, assumed-pending.** The charge is refunded with the principal. Assumed until Commercial confirms; **blocks shipping.** See below. |
| **Argon's own fee on a transfer** | **Legal** | **REFRAMED 2026-10-13.** The wallet platform books the ₦10 / ₦50 transfer fee as Argon's income (its charges page calls it Argon's profit); VFD's own charge to Argon is captured nowhere. So the question is not whether Argon may pass a bank's fee through, or add a margin to it: **Argon charges its own fee in its own right**, and the CBN cap applies to that differently. Until answered, the margin component is refused until set (§9). **With it (2026-10-13): VAT.** VAT is charged on Argon's own fee (decided: the bank's VAT is inside whatever the bank charges Argon, the partner component), so Argon is supplying a VATable service in its own right. That is the same fact that reframes the fee question, and it may carry a VAT registration and remittance obligation for Argon. Legal is asked both together; it is not treated as arithmetic. |
| **The transfer levy's incidence** | **Compliance** | **OPEN (2026-10-11), its own item, not a footnote to the edition check.** If the levy is legally on electronic RECEIPTS, it falls on the recipient, and charging it to our sender would be a compliance error we built in deliberately. That is a different question from "are these the right figures". Until answered, the levy component refuses rather than assuming it applies (§9). |
| Loan interest calculation method | The loan rate's owner | **ANSWERED 2026-10-13 from the running loan platform: flat on principal** (argon-loans-backend `approval_terms.go:39-41`: total = principal × (1 + rate)), the method current customers have. For loan writes. **Flag to its owner:** that platform derives the term from the rate (months = ceil(rate ÷ 10), `approval_terms.go:45-53`), which is probably not intentional. |
| **VFD's actual charge to Argon (the partner component)** | **Whoever holds the VFD relationship** | **OPEN (2026-10-13).** Unknown: no response, webhook or invoice field in the running platform carries it. The partner component stays refused until set (§9). |
| **Where a society's money is held** | **Legal + Product** | **OPEN (2026-10-13), raised the same day.** Pooled in Argon's VFD account with each society's share existing only as a row in our database (the wallet platform's structure, unreconciled), or segregated per society. Expected: segregation, because a Directorate auditing a society will ask where the money is. **Build nothing that assumes either answer.** |
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

> **CLOSED 2026-10-13 — see §11. Kept below as the record of the dispute.**
>
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

**Corrected 2026-10-13: the ₦10 / ₦50 transfer fee is ARGON'S, not the bank's.** The running wallet platform books it as Argon's income; VFD's actual charge to Argon is captured nowhere. So:
- the figures listed below as the "partner fee" are **Argon's margin**;
- the **partner component is unknown, not ₦10**. It stays refused until whoever holds the VFD relationship says what VFD charges;
- Legal's question is reframed (§6): Argon charges its own fee in its own right.

The shape below is unchanged; the labels on the illustrative figures are corrected in suite 0011's fixtures.

**Revised 2026-10-11: four components, not two.** The figures come from three statutes, and VAT is a rate on the fee, so a single shared band structure cannot hold them. Each component is its own series, from its own named source, set explicitly or refused, on its own ledger line and account:
- **partner_fee** (the bank's fee, from the CBN guide): bands on the transfer amount.
- **partner_fee_vat** (VAT law): a RATE ON THE FEE. Written as a figure per band it would be right today and silently wrong the next time the fee changed, and nobody would notice, because it would still look like a VAT figure.
- **statutory_levy** (the levy's statute): its own bands, so "₦50 from ₦10,000" is one band and never cuts the fee's bands. It is remitted, not earned, on its own payable account. **Refused until Compliance answers its incidence (§6).**
- **argon_margin**: refused until Legal answers (§6).

Applied transfer figures supplied 2026-10-11, held only as ILLUSTRATIVE test data and SOURCED-AND-UNVERIFIED, pending Compliance confirming the edition and bands:
- under ₦5,000: ₦0;
- ₦5,000 – ₦9,999: ₦10 fee + ₦0.75 VAT;
- ₦10,000 – ₦50,000: ₦10 + ₦0.75 + ₦50 levy;
- above ₦50,000: ₦50 + ₦3.75 + ₦50 levy.

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

## 10. Decided 2026-10-11: rules every approval request inherits

**NOBODY AUTHORISES, SIGNS OR ATTESTS TO A MOVEMENT THEY BENEFIT FROM.** Widened 2026-10-14 from "nobody signs a request they benefit from", which was written for signatures and so did not reach the payout hold, a different object: a treasurer who was the member being paid could authorise their own payout and record it, the money leaving on one person's judgement and one person's attestation, both theirs. That was the fourth time the same conflict appeared, and the worst, because it bypasses signatures entirely. The rule now names the acts, not the object, so the next object inherits it instead of waiting to be caught a fifth time:
- **signs**: a signature on an approval request;
- **authorises**: a payout hold (authorised to execute), and releasing one: a release pays nothing, but it frees the member's withdrawable, the precondition for their next payout, so it is an act with a financial effect on the actor (added 2026-10-14 at review);
- **attests**: recording a movement as having happened (a contribution, a payout).
- **raises**: raising a request someone else will sign. The person who raises a request never signs it (decided 2026-10-18: an officer may raise a withdrawal request for a member without a phone, and is then recorded as its raiser; 0016). The same conflict as authorising and attesting: one person both asking and approving means nobody checked the other.

Checked per PERSON, not per member record. Where it is enforced:

| Request | Who benefits | Where it is enforced |
|---|---|---|
| Loan disbursement | the borrower; and every guarantor, who does not benefit but has an interest in it (they carry the risk) and is barred for the same reason | 0008 `signatory_not_guarantor`; 0009 `borrower_never_signs_own_loan` (finding 19) |
| Loan write-off | the borrower (debt extinguished), and every guarantor (liability extinguished) | the same two rules: any request naming the loan |
| Withdrawal | the member whose savings pay out | 0011 `requester_never_signs_own_withdrawal` (accepted 2026-10-11) |
| Payout hold (authorising a withdrawal to execute) | the member whose savings pay out | 0015 (tests first, 2026-10-14) |
| Releasing a hold | the member whose withdrawable it frees | 0015 |
| Recorded payout (attesting it happened) | the member whose savings pay out | 0015 |
| Recorded contribution (attesting money arrived) | the member credited | 0015 |
| Raising a request (any kind that records its raiser) | the raiser never signs it | 0016 |

**For any new request kind, or any new act on money:** before its tests are written, name who benefits, and make "that person signs, authorises or attests to it" a refusal test of its own. A kind or act with no such test is not ready for review.

**AUTHORISING AND ATTESTING ARE DIFFERENT DUTIES, HELD BY DIFFERENT PEOPLE.** Decided 2026-10-14. Authorising a payout is the judgement (is it approved, within what the member may withdraw, correctly charged); recording it is the attestation that it happened. One person doing both means nobody checked the other, and the hold stops being a control and becomes a step. The person who authorised a hold never records its payout (0015). The role matrix grants both duties to the treasurer and the secretary; that is a gap in the matrix, not a permission. Rules today are per kind; whether they become one general rule in the schema (a request's beneficiaries as data) is a decision to take when the fourth kind arrives.

**CHANGING A RULE THAT PROTECTS MEMBERS IS ITSELF A REQUEST.** A society's withdrawal quorum, account floor and write-off quorum are bye-law decisions:
- **Who may set them:** not the app and not an officer alone.
- **The intended shape:** an approval request of its own kind, under the rules already built, at the society's current quorum, stating the before and after values. This is the write-off's shape: the society decides, attributably, and nobody changes the rule that protects them by editing a field.
- **Until that request kind exists:** the operator script sets them, as it sets charge schedules, and the app has no privilege on those columns (0011).

**SCHEMA GAP, OPEN (noted 2026-10-18; when it lands is the user's decision, before or after the frontend wiring): a governance change made WITHOUT a society resolution cannot be recorded as such.** 0014 requires a resolution reference and date for every society setting. The first server run of the operator script (a test society, 2026-10-18) had no resolution, so the truth went into the reference TEXT ("TEST-SOCIETY-NO-RESOLUTION (the date is the day of this test, not of any resolution)"), where nothing can query it. A report asking which governance changes were made without a society resolution, the question an auditor asks, cannot be answered. The shape decided: a nullable "no resolution" flag with a required explanation, the date then not required, instead of the truth living in prose. It serves test societies, which will recur, and the real case: an emergency change made before a committee can meet. Tests first when it is built.

## 11. Decided 2026-10-13: the record of account, reconciliation, and recording what happened

**D-04 is CLOSED.**
- **Our immutable double-entry ledger is the record of account** of what the society decided.
- **VFD's balance is the bank's record** of what it holds.
- **Neither is ever adjusted to match the other;** a difference is an item to explain.

**The evidence is the running wallet platform** (read-only review, 2026-10-13). It treats its local books as the record of account: every balance read and every sufficiency check uses its stored balance, and VFD's balance is never read per account. That matches D-04 as first written. It has never once checked those books against VFD:
- no scheduled reconciliation;
- in-app "reconciliation" compares local data with local data;
- thirteen repair tools disagree with each other about which local figure is true, and none consults VFD.

That is how ₦1.39m of loan credits accumulated uncorrected, waiting on "VFD balances" nobody fetched.

**There was no B-04 spike.** "B-04" appears nowhere in code, docs or history in any of the four repositories. The summary this contract carried described work nobody did (finding 32). The answer came from reading what the running system does.

**OBLIGATION: scheduled reconciliation against VFD.** This is what turns D-04 from an assertion into a control.
- On a schedule, compare what the ledger says each VFD account holds with what VFD reports (statements, or balances where statements are unavailable).
- **Record every difference** as an item to explain, with who resolved it and how.
- Never "correct" either side to match the other.
- A reconciliation that cannot run (VFD unreachable, no statement) is itself a recorded item, never a silent skip.

**THE LEDGER MUST BE ABLE TO RECORD ANYTHING THAT ACTUALLY HAPPENED.**
- A rule that prevents recording reality is not a control; it is a blindfold.
- **Validation belongs BEFORE the money moves.** After it, the only question is what is true.
- So a payout is checked, and its amount held, when it is **authorised to execute**. The confirmed outcome is then always recorded.
- A recorded outcome that differs from what was authorised (a different amount, a second payment, a payout with no authorisation, a payout the member could not afford) is **flagged for a person, never refused**.
- Found in suite 0011 as built (finding 33): the database checked withdrawable when the payout was recorded, so it would have refused to record a transfer that had already left the bank.

**How the VFD boundary is built: re-implemented in Go, in this repository** (decided 2026-10-13).
- The cooperative's record of account cannot depend on a ledger that is hand-edited and unreconciled.
- A shared Go module holding only the VFD client is the better end state, once someone owns the wallet-side changes. It does not block this work. The wallet platform's repositories are not to be changed from here.
- **Take the knowledge, none of the defects:** `docs/VFD_INTEGRATION.md` is the spec.

**Where a society's money is held is open** (§6). Nothing is built that assumes pooled or segregated funds.
