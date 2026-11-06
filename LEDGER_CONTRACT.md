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
| Does a reversal refund the Argon charge? | **Commercial** | **D-02: ANSWERED, confirmed by Commercial (2026-10-22).** The charge is refunded with the principal. **No longer blocks shipping.** See below and §12. |
| **Argon's own fee on a transfer** | **Legal** | **REFRAMED 2026-10-13.** The wallet platform books the ₦10 / ₦50 transfer fee as Argon's income (its charges page calls it Argon's profit); VFD's own charge to Argon is captured nowhere. So the question is not whether Argon may pass a bank's fee through, or add a margin to it: **Argon charges its own fee in its own right**, and the CBN cap applies to that differently. **D-07 (2026-10-22): the CBN bands ARE Argon's charge in full** (§12): the margin component carries the band; it is not a bank fee passed through with a margin on top. **With it (2026-10-13): VAT.** VAT is charged on Argon's own fee (decided: the bank's VAT is inside whatever the bank charges Argon, the partner component), so Argon is supplying a VATable service in its own right. That is the same fact that reframes the fee question, and it may carry a VAT registration and remittance obligation for Argon. Legal is asked both together; it is not treated as arithmetic. |
| **The transfer levy's incidence** | **Compliance** | **OPEN (2026-10-11), its own item, not a footnote to the edition check.** If the levy is legally on electronic RECEIPTS, it falls on the recipient, and charging it to our sender would be a compliance error we built in deliberately. That is a different question from "are these the right figures". Until answered, the levy component refuses rather than assuming it applies (§9). |
| Loan interest calculation method | The loan rate's owner | **ANSWERED 2026-10-13 from the running loan platform: flat on principal** (argon-loans-backend `approval_terms.go:39-41`: total = principal × (1 + rate)), the method current customers have. For loan writes. **Flag to its owner:** that platform derives the term from the rate (months = ceil(rate ÷ 10), `approval_terms.go:45-53`), which is probably not intentional. |
| **VFD's actual charge to Argon (the partner component)** | **Whoever holds the VFD relationship** | **ANSWERED by D-07 (2026-10-22): the partner component is ZERO, deliberately** (§9, §12). VFD's cost to Argon is Argon's business expense, not the member's. Still unknown what VFD bills; if it is ever reported, the component is where it goes, and the member's charge does not change. |
| **Where a society's money is held** | **Legal + Product** | **OPEN (2026-10-13), raised the same day.** Pooled in Argon's VFD account with each society's share existing only as a row in our database (the wallet platform's structure, unreconciled), or segregated per society. Expected: segregation, because a Directorate auditing a society will ask where the money is. **Build nothing that assumes either answer.** |
| Opening balance contra account | Tier 1 accountant | `opening_balance` posts at zero charge as its own movement kind. What it debits is §7's assumption, not a decision. |

### The reversal charge is a fairness question before it is an accounting one

Whoever owns this should see what they are actually deciding.

A treasurer records a member's ₦20,000 withdrawal twice by mistake. The member
is charged ₦60.75 on each (§7a). The duplicate is reversed. (Until 2026-10-22
this read as a ₦25,000 contribution charged ₦87.50; contributions now carry no
charge by default, §9, and §7a was rewritten.) **If Argon retains the
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

**D-02 answers it: the charge is refunded with the reversal.** Confirmed by
Commercial, its owner, on 2026-10-22: a settled rule, no longer an assumption,
and it no longer blocks shipping. A member is not out of pocket for a
treasurer's error. `lib/ledgerFixture.ts` and `internal/ledger/port.go`
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

### 7a. Member withdrawal of ₦20,000 (rewritten 2026-10-22 under D-07)

**Superseded:** until 2026-10-22 this example was a ₦25,000 *contribution*
charged "0.35% capped at ₦200" (₦87.50, split between the partner and Argon).
That rate came from no source anyone can name, and two later decisions replaced
it: contributions carry **no charge by default** (§9), and the charge on a
transfer is **the CBN band, in full, as Argon's own charge, the partner
component zero** (D-07, §12). A charged contribution is now the exception, so
the example shows the ordinary charged movement: a withdrawal. The uncharged
contribution, the case a member sees most often, follows as 7a′.

`withdrawal_out` of ₦20,000, paid to the member's bank account. The charge is
ON TOP (§9): the member receives the ₦20,000 they asked for. Source: **CBN Guide
to Charges, 2026 revision**, sourced-pending-confirmation (§12).

**The charge, all four components** (the schema has four; this is the
structure the example teaches):

| Component | Rule | Kobo |
|---|---|---:|
| Partner fee (VFD's charge to Argon) | **ZERO BY DECISION (D-07).** VFD's cost is Argon's business expense, not the member's. Kept so that, if VFD ever reports its fee, there is somewhere to put it; the member's charge would not change. | 0 |
| Argon's fee | The CBN band, in full: ₦10 on ₦5,000–₦50,000 | 1,000 |
| VAT on Argon's fee | 7.5% of Argon's fee, its own line (0013) | 75 |
| Transfer levy | ₦50 from ₦10,000, its own line, remitted not earned. **Its incidence is still open with Compliance (§6).** | 5,000 |
| **Total** | | **6,075** |

**The entry**, posted when the officer records the payout (the hold having
authorised it first, §11):

| Account | Dr | Cr |
|---|---:|---:|
| Member savings — ARG-0142 | 2,006,075 | |
| Bank / cash | | 2,000,000 |
| Charges payable — Argon (`charges_margin`) | | 1,000 |
| VAT payable on charges (`charges_vat`) | | 75 |
| Transfer levy payable (`statutory_levy_payable`) | | 5,000 |

The partner component is in the charge and **not in the entry**: the ledger
never writes a ₦0 line (a zero is the absence of a line, never a line of zero).
If VFD's fee is ever set, a fifth line appears on `charges_partner`.

Debits 2,006,075 = credits 2,000,000 + 1,000 + 75 + 5,000. ✅

The member's statement reads: withdrew ₦20,000, charges ₦60.75, ₦20,060.75 out
of savings. Three figures, because a member who sees ₦20,060.75 leave against a
₦20,000 request reads the difference as money going missing.

### 7a′. Member contribution of ₦25,000 (uncharged, the default)

`contribution_in` of ₦25,000 in cash. A contribution carries **no charge by
default** (§9): a society that charges none has no contribution series, and the
member is credited in full.

| Account | Dr | Cr |
|---|---:|---:|
| Bank / cash | 2,500,000 | |
| Member savings — ARG-0142 | | 2,500,000 |

Debits = credits. ✅ The statement reads: contributed ₦25,000, credited ₦25,000.

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

### 7c. Reversal of 7a (the transfer came back)

A **new** entry. 7a is untouched. The bank returned the ₦20,000 (a wrong
account number, say): the member did nothing wrong, and is made whole.

| Account | Dr | Cr |
|---|---:|---:|
| Bank / cash | 2,000,000 | |
| Charges payable — Argon (`charges_margin`) | 1,000 | |
| VAT payable on charges (`charges_vat`) | 75 | |
| Transfer levy payable (`statutory_levy_payable`) | 5,000 | |
| Member savings — ARG-0142 | | 2,006,075 |

Carries `reverses: <id of 7a>`, a written reason, and its own actor and
timestamp. Debits = credits. ✅

**This is D-02: Argon's charge is refunded with the principal** (confirmed by
Commercial, its owner, 2026-10-22 — §6, §12), and the VAT on it with it.
**The levy line is OPEN:** D-02 is about Argon's charge, and whether a levy
already remitted is refunded on a reversal is a statutory question, with
Compliance alongside its incidence (§6). Shown refunded here; if it is not, the
member's credit is 2,001,075 and this entry has no levy line.

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
- the **partner component is unknown, not ₦10**. ~~It stays refused until whoever holds the VFD relationship says what VFD charges;~~ **D-07 (2026-10-22): it is ZERO, deliberately, with a source saying so, not refused** (§12);
- Legal's question is reframed (§6): Argon charges its own fee in its own right.

The shape below is unchanged; the labels on the illustrative figures are corrected in suite 0011's fixtures.

**Revised 2026-10-11: four components, not two.** The figures come from three statutes, and VAT is a rate on the fee, so a single shared band structure cannot hold them. Each component is its own series, from its own named source, set explicitly or refused, on its own ledger line and account:
- **partner_fee** (what VFD charges Argon): bands on the transfer amount. **D-07: ZERO, deliberately** (§12); kept because the structure is right and, if VFD ever reports its fee, there is somewhere to put it.
- **partner_fee_vat** (VAT law): a RATE ON THE FEE. Written as a figure per band it would be right today and silently wrong the next time the fee changed, and nobody would notice, because it would still look like a VAT figure.
- **statutory_levy** (the levy's statute): its own bands, so "₦50 from ₦10,000" is one band and never cuts the fee's bands. It is remitted, not earned, on its own payable account. **Refused until Compliance answers its incidence (§6).**
- **argon_margin**: **D-07: the CBN band, in full** (§12). Argon's own charge, from the named source.

Applied transfer figures supplied 2026-10-11. **2026-10-22: their source is named, "CBN Guide to Charges, 2026 revision" (the edition in force), and their state is SOURCED-PENDING-CONFIRMATION** (was sourced-and-unverified); who confirms it is being named by the reviewer (§12):
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

**Argon's margin can be expressed and is refused until set.** It is never assumed to be zero, nor any figure. **D-07 (2026-10-22) says what it is set to: the CBN band** (§12); a society with no series set is still refused.

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

**A REQUEST NO QUORUM CAN APPROVE IS REFUSED WHEN RAISED** (decided 2026-10-19; 0016). Who may sign a request is the society's current signatories, by person, less its raiser and the member it pays. Fewer than the quorum, and it could only sit collecting signatures until it expired, the member learning a week later that their society's arithmetic made it impossible. This was a LIVE DEFECT before 0016: a society holds one open-ended appointment per post, the role matrix's signatories are the chairman, treasurer and secretary, so a society of three at a quorum of three could never approve an officer's own withdrawal, nor (from 0016) one an officer raises. The refusal states the arithmetic AND the remedy: how many may sign, how many are needed, and "appoint another signatory, or lower the withdrawal quorum". **THE LIMIT IS IN OUR INTERFACE, NOT IN A SOCIETY'S GOVERNANCE** (2026-10-19). The schema permits any post but the internal auditor to be a signatory: the chairman, treasurer, secretary, a branch officer and a loan officer, one open-ended holder per post, so up to FIVE signatories. The frontend's role matrix names only three of them (chairman, treasurer, secretary) as signatories. That mismatch is a FRONTEND DEFECT, and it produces a ceiling nobody decided: **for a society that follows the matrix as built, the maximum workable withdrawal quorum is 2.** With exactly three signatories at a quorum of 3, every withdrawal an officer asks for themselves, or raises for a member, is impossible to approve, because nobody signs a request they raise or that pays them, which leaves two. A society should read this before it sets a quorum of 3, and know that the limit comes from our interface.

**ANSWERED 2026-10-22: FIVE SIGNING POSTS** (§12): the chairman, treasurer, secretary, branch officer and loan officer; everyone except the internal auditor, who is read-only by Directorate rule. This is what the database already permits; nothing changes in the schema. The three-post limit was the frontend's role matrix, written from a design artifact before the schema existed: a frontend defect, being fixed there. **Once the matrix is corrected the ceiling above no longer applies: a society may designate any of the five.** The operator warning STAYS: a society that designates only three still hits the arithmetic at a quorum of 3.

**The operator is WARNED, not refused,** when setting a quorum the signatories cannot meet: a society may legitimately be appointing another signatory next week, and refusing would make Argon the arbiter of a bye-law. The warning is loud, in the preview, and names which requests become impossible.

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

## 12. Decided 2026-10-22: six decisions settled, with their owners

| # | Decision | Owner | State |
|---|---|---|---|
| **D-02** | **A reversal refunds the Argon charge** with the principal (§6, §7c). | **Commercial** | **ANSWERED: confirmed by Commercial.** Was assumed-pending; no longer blocks shipping. |
| **D-07** | **The CBN bands are Argon's charge IN FULL,** not a bank fee passed through with a margin on top. The member pays the band; VFD's cost to Argon comes out of Argon's side as a business expense, as the loan platform already does (it books the ₦10 as Argon's income and captures VFD's fee nowhere). | **Commercial** (recorded 2026-10-22: a pricing decision about what Argon charges, the same owner as D-02). The individual who signs it off is being confirmed by the reviewer. | **DECIDED.** The reasons, for the record: nobody knows what VFD bills, so a partner component would be permanently unset and refuse every charge; and the CBN figure is a CEILING, so charging the band AND passing VFD's fee through would put the member above it. **All four components are kept:** the partner fee is **ZERO, deliberately**, its series' source saying so, NOT refused (the structure is right; if VFD reports its fee there is somewhere to put it); VAT stays on Argon's fee; the levy and the margin are unchanged. **FLAG, not acted on:** confirm with whoever runs the loan platform's books that booking the fee as Argon's income is deliberate there and not an oversight. |
| **D-13** | **A dispute correction requires a SECOND OFFICER.** | **Governance** | **ANSWERED: confirmed by Governance.** Not yet built: when dispute corrections are, the second officer is a refusal test of its own (§10: name who acts, and who may not act alone). |
| **D-16** | **Insurance stays OUT.** | Commercial and Legal (GROUP_D_DECISIONS §4) | **UNCHANGED, not blocking:** the underwriter is not finalised. |
| — | **Charge rates: the source is named.** The figures are from **"CBN Guide to Charges, 2026 revision"**, the edition in force, stored as the named source beside them. | Who confirms it: **being named by the reviewer.** | **SOURCED-PENDING-CONFIRMATION** (was sourced-and-unverified). |
| — | **FIVE SIGNING POSTS:** chairman, treasurer, secretary, branch officer, loan officer; everyone except the internal auditor (read-only by Directorate rule). | The reviewer (the PM question of §10, answered) | **DECIDED.** Already what the database permits; the three-post limit was the frontend's role matrix, a frontend defect being fixed in that session. Once corrected, §10's ceiling no longer applies; the operator warning stays (three designated at a quorum of 3 still hits the arithmetic). |

**Decided the same day, on the frontend's questions:**

| # | Decision | Owner | State |
|---|---|---|---|
| — | **SIGNING RIGHTS DO NOT FOLLOW THE POST.** A handover transfers the post; the society decides separately, and explicitly, whether the new holder signs. `appointments.is_signatory` is set per appointment and is NOT carried from the outgoing holder (as the schema already does): a successor's signature is refused until it is set, which is recoverable and visible; a new officer silently able to sign would be neither. The handover flow must ASK. | The reviewer | **DECIDED.** Signing rights are a society's decision about a person, not a property of a chair. **No appointments API exists: nobody can designate a signatory today. Queued after onboarding.** |
| — | **"Vice chairman" is not a post.** The frontend's fixture named one; the fixture was wrong (finding 44: a fixture describes a world the system can be in), and becomes a branch officer. | The reviewer | **DECIDED for the fixture. OPEN, a product question with the five-post decision:** do societies need a vice chairman as a distinct post, and should it sign? Adding a post is a product decision nobody has made. |

| — | **BROWSER ACCESS (CORS) IS CLOSED BY DEFAULT.** A preflight is answered before authentication (it carries no credentials by design); only configured origins (`ARGON_ALLOWED_ORIGINS`), matched exactly, never a wildcard; nothing configured means no origin. An allowed origin is not a trusted one: a real call still needs its session. CORS protects the PAGE, not the endpoint: a call from an unlisted origin is still answered, only unreadable to a browser page, so nothing may be built on "an unlisted origin is blocked". | The reviewer | **DECIDED and BUILT** (`internal/api/cors.go`; found by the frontend's first real connection). Production's origin is a decision someone makes when the frontend's domain exists, not a default it inherits. |
| — | **The see-an-officer `message` is MEMBER-FACING.** It says what happened and what to do next (a new device, confirmed in person by an officer of their society); nothing about SMS providers or what is temporary. The cause is for the client in `reason` (`sms_unavailable`) and for us in the adapter's log; no developer field "in case". | The reviewer | **DECIDED and BUILT.** |
| — | **A new device cannot sign in at all** while there is no SMS provider: every new device ends at "see an officer", and no endpoint lets an officer confirm one. An officer confirming a device in person is the design's own fallback, not a workaround. | The reviewer | **OPEN, queued** with onboarding. **Narrowed 2026-10-29:** with the Termii sender live (the row below), a new device gets a code; "see an officer" remains for a person with no single agreed number or a shared handset, and for an outage that keeps failing. Officer confirmation is still needed for those. |

| — | **THE SESSION BECOMES A COOKIE FOR BROWSERS** (decided by the reviewer, 2026-10-22; the token in localStorage is readable by any script on the page and authorises money). The API sets an httpOnly, Secure cookie on a domain the app and the API share. **The Bearer header STAYS, for clients with no browser** (the probe and the smoke tests exercise the API as argon_app through it); the distinction is explicit: a request carrying an Origin (a browser always sends one) with a Bearer header is a page script holding the token, and is to be considered for refusal. **Sign-out ends the session AND clears the cookie, in one response**; ending the session is the authoritative half (a cleared cookie on a live session leaves anyone holding the old value signed in). **CSRF: SameSite alone is NOT enough;** a token too (double-submit, or the custom-header check if the API only ever takes JSON with one): decide which, and test the refusal and the accepted case. **SameSite=Strict signs out a member who follows a shared link into the app** (the cookie is not sent on cross-site navigation): Lax may be right, with the token doing the CSRF work. `Access-Control-Allow-Credentials: true` comes with it, safe because origins are exact. | The reviewer | **DECIDED; queued after the statement stage, before the balance rewrite** (a security defect in a live path: every screen wired from here builds against whichever scheme exists). |
| — | **CSRF: A SYNCHRONIZER TOKEN, BOUND TO THE SESSION** (2026-10-23). Double-submit is ruled out by the layout, not by preference: its cookie is set by the API's host and the page runs on the app's host, so the page can never read it. A custom-header check alone would make CORS the CSRF defence, the largest thing that could be built on "an unlisted origin is blocked". So: a token bound to the session (as of 2026-10-24 DERIVED, nothing stored: the next row); given to the page in the sign-in response BODY, held in memory (never storage, a cookie or a URL), recovered after a reload from GET /me (whose answer a cross-site page cannot read); required as `X-CSRF-Token` on every state-changing request of a cookie session. A missing or wrong token, or one from ANOTHER session, is refused (403 `csrf_failed`) BEFORE the handler runs, the session untouched and nothing counted against a lockout, so a cross-site page cannot sign a member out by failing on purpose. GETs and Bearer requests carry none. | The reviewer | **DECIDED.** (Its first form stored a digest on `auth_sessions`, migration 0018: withdrawn 2026-10-24, the next row.) |
| — | **THE CSRF TOKEN IS DERIVED, AND NOTHING STORES IT** (2026-10-24). "Random, only its digest stored" and "/me gives it again after a reload" were approved together and cannot both hold: a random token cannot be recovered from its digest. So the token is `HMAC(ARGON_CSRF_KEY, "argon-csrf:" + the session token)`: given out from the cookie at sign-in and by GET /me, and CHECKED BY RECOMPUTING IT WITH THE CURRENT KEY on every state change. Nothing stores it: a stored digest would be checked against the key it was made with (so a rotation would sign everyone out) or not checked at all (a column written and never read, whose presence implies a check that does not happen); it adds no security, both deriving from the session token and the key. **The key** is server-held, configured (`ARGON_CSRF_KEY`, hex, at least 32 bytes, separate from the code key so each rotates alone), and **its absence is a refusal to start**, never a generated default (a key made at each start would sign every page out on every restart). **ROTATING THE KEY** invalidates every live page's CSRF token: each page's next state change is refused (403 `csrf_failed`), and the page recovers by reloading and re-reading /me, which gives the token under the new key. No sign-in is needed; sessions are untouched. | The reviewer | **DECIDED.** Migration 0018 and its suite withdrawn (nothing stored); session_api tests 28–32 pin it: never the session token nor its plain digest; dependent on the server's key; a rotation refuses the old token and the page recovers through /me; no key, no start; the configuration requires the key. |
| — | **SameSite=STRICT, ON A HARD REQUIREMENT** (2026-10-23). SameSite is judged against the page making the request, not how the member arrived: a shared link navigates to the APP's host, which never carries the API's cookie under any setting; the app page's calls to the API are same-site, and Strict sends them. **HARD REQUIREMENT ON THE DOMAIN DECISION: THE APP AND THE API MUST BE SUBDOMAINS OF ONE REGISTRABLE DOMAIN** (app.&lt;domain&gt;, api.&lt;domain&gt;). If they are not, every API call is cross-site: Strict and Lax both withhold the cookie, and None is increasingly blocked as a third-party cookie. The domain must not be chosen on other grounds (hosting convenience) and this discovered afterwards. **Settled only by a real-browser proof** (the frontend's suite against the real server, never a stub: arrive by a cross-site link, then call the API, the cookie sent); if any engine drops it, Lax, the token doing the work, decided from the result. **Decided in advance (2026-10-26): if the three engines (Chromium, Firefox, WebKit) disagree, the answer is Lax for every browser, never "Strict where it works":** a setting that differs by browser is a security posture nobody can state. Development: the app calls `localhost:8081`, never `127.0.0.1:8081` (a different site; `.env.example`). | The reviewer | **SUPERSEDED 2026-10-29: THE SESSION COOKIE IS LAX, ruled on the evidence available.** Chromium and Firefox passed the Strict proof against the real server; **the WebKit run was never completed** (it needs HTTPS or a real Safari). Two engines passing and one unproven is not agreement, and the rule decided in advance was Lax whenever they do not agree; "Strict while we wait to find out" is the ruled-out per-browser posture with a deadline attached. The CSRF token is the mechanism (useless without the cookie, the cookie useless without it); Strict was reinforcement. Lax is also the safer failure: a WebKit navigation case that dropped a Strict cookie would sign Safari members out with no pattern anyone could reproduce. The **device cookie stays Strict**: it rides only on the app's own sign-in calls (Path=/auth/), never on a navigation, and losing it means a code, not a silent sign-out. The domain requirement is unchanged: Lax withholds the cookie from cross-site calls exactly as Strict does. Built: `internal/api/session.go`; session_api test 09, break `cookie_strict`. |
| — | **A BROWSER NEVER HOLDS A CREDENTIAL IN SCRIPT** (2026-10-23). A request carrying an Origin AND a Bearer header is refused (401 `bearer_from_browser`); a browser sign-in with a device token in its BODY is refused (400 `device_token_from_browser`, before the PIN is checked, so nothing counts against the lockout). **These are NOT security boundaries:** a client that is not a browser sends whatever Origin it likes, so a stolen token used from curl is untouched. They are ENFORCEMENT OF THE DESIGN: a page that keeps a credential in script fails loudly on its first call instead of working while being the exposure this change removes. Nothing may be built on them as a defence. A request with a session cookie AND a Bearer header is refused (400 `ambiguous_credentials`): one request, one identity, never a precedence rule. A browser's sign-in gets the session in a cookie and the CSRF token in the body, never the session token; a non-browser's (no Origin) gets the session token in the body as before, no cookie, no CSRF token. | The reviewer | **DECIDED.** |
| — | **THE TWO COOKIES** (2026-10-23). **`__Host-argon_session`**: HttpOnly, Secure, SameSite=Strict (**Lax since 2026-10-29**, the SameSite row above), Path=/, no Domain (the prefix makes the BROWSER refuse it otherwise, so no other host on the domain can set or overwrite it); Max-Age = the session's remaining life when issued (8 h officer, 30 d member), the server authoritative: a cookie outliving its session is a dead value refused like any other. **`__Secure-argon_device`** (the device token leaves the page's storage in the same change: longer-lived than a session, and with it a stolen PIN skips the new-device path): HttpOnly, Secure, SameSite=Strict, **Path=/auth/** so it rides only on sign-in calls (`__Host-` would need Path=/; the path scoping is worth more), Max-Age 400 days (the browsers' cap), renewed on every sign-in that uses it. A device cookie is NOT a session: alone, it is 401. **Sign-out** (a POST: the CSRF token) ends the session AND clears the session cookie in one 204; it leaves the device known (a session ended by its own holder does not revoke its device; one ended from ANOTHER session does: the stolen-phone path, already the schema's). Any 401 on a request that carried a session cookie clears it. `Access-Control-Allow-Credentials: true` to exact origins only; `X-CSRF-Token` an allowed header. | The reviewer | **DECIDED and BUILT** (2026-10-26: `internal/api/session.go`, the gate and the sign-in middleware; `ARGON_CSRF_KEY` required by config and by `NewServer`). Tests: pieces 5–7 of 2026-10-23 (suite session_api, 32 tests; 0018 withdrawn 2026-10-24). **A deploy needs `ARGON_CSRF_KEY` in the instance's deploy env first:** `30-install-api.sh` refuses without it. Add it with the line in deploy/README.md, "A new key on an existing instance", which writes a key only when no valid one is there (never a plain append: run twice, the later key wins and every page's token is refused). Needed before the DEPLOY, not the push: a push runs CI only. |
| — | **A MEMBER CANNOT END THEIR OTHER SESSIONS** (gap, 2026-10-23). The schema revokes a device when its session is ended from another session (the stolen-phone case), but no endpoint lets a member do it: the case they would most want. The cookie tests end the other session at the database and say so. | The reviewer | **OPEN, a gap.** |
| — | **A REQUEST IS A QUESTION; THE HOLD IS WHERE MONEY IS COMMITTED** (2026-10-26, balance rewrite piece 2). A withdrawal request the member has raised but no officer has authorised is shown on its own line ("Requested, waiting for approval. Not taken from what you can withdraw until it is approved.") and is NOT deducted; only an authorised payout comes off the withdrawable figure. **The designed consequence, which will look like a defect to whoever meets it first:** a member can raise two requests that each fit alone but not together; both are accepted when raised, and the SECOND HOLD TO BE AUTHORISED IS REFUSED, saying why (piece 4). That is the rule working, not a fault. | The reviewer | **DECIDED and BUILT** (2026-10-28: `internal/api/balance.go` and the hold refusal in `money.go`, every figure from the database: `member_position` (0018), `largest_withdrawal` and the kept quote (0017); Go arranges and calculates nothing about money). Tests: TestApiBalance12 and 16 (a released hold puts the request back), 14 and 15 (the refusal's causes). |
| — | **SIGN-IN CODES GO THROUGH TERMII; OUR FAILURE IS NEVER THE MEMBER'S** (2026-10-28, four pieces). Re-implemented here in Go, never by calling another platform's service. The sender is chosen (`ARGON_SMS`: `termii` or `refusing`, no default; unset is a refusal to start), with `TERMII_URL` (https), `TERMII_API_KEY` and `TERMII_SENDER_ID`, none defaulted; the DND route fixed in code; `+234` numbers only; ONE call per code, no retry. "Sent" means ACCEPTED by Termii (a 2xx, a readable body, `code: "ok"`, a message id), not delivered. Four outcomes, each recorded in `sms_sends` (0019: final, no text or code column, a message id exactly when accepted): **accepted** → `code_sent`; **refused / not_reached** → `code_not_sent`, NO challenge, "We couldn't send your code just now. Please try again in a minute. If it keeps failing, an officer of your society can confirm this phone with you in person."; **unknown** (no answer in time, a 5xx, an unreadable body) → `code_sent` WITH the challenge (the code may arrive and must work) and "Your code may take a little longer to arrive. If it hasn't come in a minute, try again." **Nothing counts against the lockout:** the PIN was right and the failure was ours. **A Termii failure never routes to an officer by itself;** `see_officer` stays for no single agreed number or a shared handset. The code, the key, the full number and the text are never logged (tested). | The reviewer | **DECIDED and BUILT, NOT LIVE** (2026-10-29: `internal/auth/termii.go`, `internal/api/signin_sms.go`, `internal/config/sms.go`, 0019; suites 0019, sms, sms_api). Live only after `deploy/AUTH_SMS.md`'s go-live list: (a) one real send checked against Termii's answer, (b) the sender ID's registration covers this product, (c) which account sends, (d) the loan platform's key rotated. Until then the instance runs `ARGON_SMS=refusing`, added with the line in deploy/README.md before the deploy that first needs it. **Named gap: delivery receipts** (a signed webhook; not built). |
| — | **TOO SOON FOR ANOTHER CODE IS NOT A LOCKOUT** (ruled 2026-10-30; found by the frontend on screen: a member whose PIN was right told "signing in is paused for this number"). A sign-in inside the database's resend minute answers 429 `code_resend_too_soon`, "Please wait a moment before asking for another code.": nothing about attempts, nothing about pausing, nothing counted. The lockout keeps 429 `try_later`. A page countdown cannot stand in for it (a second tab is a fresh browser). A refused send still starts the minute (the code row is written before the send). | The reviewer | **DECIDED and BUILT** (`internal/api/signin_resend.go`, `auth.ErrResendTooSoon`; sms_api test 07, four breaks). |
| — | **ONE ACCOUNT PER ROLE IN A SOCIETY, EXCEPT `general`** (2026-10-30; a finding of its own, deploy/README.md finding 56). A role names where the code posts. The money endpoints posted to the first account by code when a role had two, while the batch refused the same chart: two money paths disagreed about where money goes, and one guessed. The database refuses a second account of a posting role (0020); a money path finding none, or two, refuses and writes nothing. | The reviewer (onboarding piece A) | **DECIDED and BUILT** (0020; `internal/api/money.go`; TestApiMoney36). |
| — | **THE CSRF TOKEN IS THE ONLY CSRF PROTECTION** (2026-10-30, a consequence of the domain decision). The cooperative goes live as two subdomains of the company's existing domain, as the hard requirement above needs. That domain also carries the company's other platforms. SameSite is judged by registrable domain, so **a page on any of those subdomains is SAME-SITE to the API**: the browser sends it the Lax session cookie, and SameSite protects nothing against it. What still stands between such a page and a member's session: (a) **the CSRF token**, required on every state change of a cookie session. The page cannot read it: CORS lets only the app's origin read an answer, and the token is only ever in an answer. It cannot send the `X-CSRF-Token` header either without a preflight, which it fails. (b) **CORS**, which stops the page READING, never SENDING: the API decodes a body whatever its Content-Type, so a form post with a JSON body arrives without a preflight, with the cookie, and only the token refuses it. (c) **`__Host-`** on the session cookie: no other subdomain can set or overwrite it. The device cookie (`__Secure-`, Path=/auth/) CAN be overwritten by another subdomain; a device token is bound to its user, so a planted value is an unknown device and the member is asked for a code: a nuisance, not an exposure. **So the token is no longer a reinforcement of SameSite; it is the whole defence.** Any change to the CSRF check, its key, which requests it covers, or the CORS origin list is a change to the only CSRF protection, and is reviewed as one. A compromise of a page on any of the company's other subdomains (a script injection, a subdomain takeover) is a CSRF attempt on the cooperative that only the token stops. **Not covered by the token: sign-in itself** (there is no session yet), so another subdomain's page can sign a member's browser into an account the attacker controls (login CSRF). | The reviewer | **RECORDED. Both choices DECIDED 2026-10-31:** (1) **a request with a body that is not `application/json` is refused, 415 `unsupported_media_type`**, before anything runs: it forces a preflight on every write, which only the app's origin passes, and closes login CSRF (session fixation: a member's browser signed into an attacker's account, paying into someone else's society), reachable until then. **BUILT** (`internal/api/json.go`; session_api tests 33-39, nine breaks). (2) **Go live on the existing domain now; a domain of the cooperative's own is decided BEFORE the readiness gate (the next row) lifts.** No real society is affected until then, and moving later is configuration, not code. Put to the team lead by the reviewer, as a question with its reason. |
| — | **THE LOCKOUT IS SPLIT BY DEVICE; THE OFFICER LOCK LASTS UNTIL AN OFFICER ACTS** (2026-11-05; deploy/README.md findings 60, 62, 63). Two locks per account, neither resetting the other. **The member's OWN devices** (the stolen-phone case: sign-in on a device this server trusted for them, and every step-up): 15 min, 30 min, then locked until an officer releases it, however long that takes. **Any other device, and an unregistered number exactly alike** (so a lock never says which numbers are registered): 15 min, 30 min, then 48 hours, never the officer. The reason, the reviewer's: on an unknown device the code already does the officer's job, so the officer step there protected nothing and was the one part a stranger could reach; "a control that protects nothing and can be triggered by anyone is worse than no control, because it looks like one." **THE RELEASE SHIPS WITH THE FIX, NEVER WITHOUT IT** (non-negotiable): an officer of the member's society, never the auditor, never their own, on their own step-up, with a reason; it clears both locks and forgives what came before; it is refused when nothing is locked. **Not closed, by decision (finding 63):** a right PIN from an unknown device answers "code sent", so a stranger who guesses a PIN learns it and keeps it; members' clarity was not traded for it. Revisit if the new-device code is ever made optional. | The reviewer | **DECIDED and BUILT** (0022, `internal/auth/service.go`, `internal/api/locks.go`; suites 0022, locks_api, signin_api 15). |
| — | **REACHABLE IS NOT READY** (ruled by the reviewer, 2026-10-30, going live). **No real society's records go on this platform until backups are off the instance and a restore drill from S3 has passed.** Being reachable on a public name changes nothing about that; it makes it urgent. Until then the platform holds test data only: demo societies, and nothing a real society relies on. **Off the instance** means `BACKUP_STORE` is `s3://`, in a bucket the instance's role can write and cannot delete (deploy/AWS_REQUEST.md Part B). **Passed** means `60-restore.sh --drill` restored the newest S3 dump into a scratch database, matched its manifest and passed every suite, recorded as a `.ok` under the bucket's `drills/`. Nothing short of that meets it: not a second disk on the same instance, not a dump copied to a laptop, not "the bucket exists". **It is not waived for a pilot, a demo with real members, or a deadline.** Lifting it is an entry in this table that names the passing drill's key, not something anyone does. The reviewer said in ruling it that the pressure to skip it will come from the reviewer: this row is the answer to that pressure too. **Checked by the system, not by memory:** when onboarding is built, creating a society is refused while `BACKUP_STORE` is not `s3://` or no S3 drill has passed (a refusal test of its own). | The reviewer | **MET 2026-11-06: lifted by the row below**, which names the drill. Before that: backups on the instance's own disk (`file://`), Part B outstanding. |
| — | **THE READINESS GATE IS LIFTED** (2026-11-06, on the reviewer's word, with the results they ran on the instance). **The passing drill: `dumps/2026/11/06/argon-20261106T090257Z`**, schema 22, recorded as `prod/drills/2026/11/06/drill-20261106T090711Z.ok` in the bucket. It restored, matched its manifest, passed the probe and all 40 acceptance suites: **740 tests**, every suite's count in the record equal to the tests that suite holds (read from the record, not the console: a sum of the console lines first came to 737). **Off the instance, proven rather than configured:** `BACKUP_STORE` is `s3://`, in a bucket with Object Lock in Compliance mode (35 days) and versioning; the instance's role writes and reads it, and a delete from the instance was refused, `AccessDenied … argon-prod-host … is not authorized to perform: s3:DeleteObject`. The nightly backup, weekly drill and daily freshness timers run against S3, and the freshness check reports the dump and the drill fresh from there. **What this lifts:** real societies' records may go on the platform; nothing else. **TWO THINGS IT DOES NOT CLOSE, named here because the gate lifting could be read as if it did:** **(1) Nobody is notified (`ALERT_CHANNEL=none`).** Every alert (a failed backup, a failed or stale drill, the certificate) is kept open on the instance and shown at SSH login, and reaches no one who does not log in. The gate's condition is now kept true by those alerts: a backup that stops is, today, noticed only by someone who happens to log in. The Slack webhook is the reviewer's, in hand. **(2) No heartbeat (`ALERT_HEARTBEAT_URL` unset).** If the instance dies, nothing outside it notices, and no alert on the server can say the server is gone. Also still true, from the drill itself: dumps are nightly only, so a failure can lose up to 24 hours (no point-in-time recovery). **Checked by the system, not by memory, still to build:** society creation refuses while `BACKUP_STORE` is not `s3://` or no S3 drill has passed (onboarding, next). | The reviewer | **LIFTED. OPEN: (1) the alert channel; (2) the heartbeat.** |
| — | **A SOCIETY IS FOUNDED BY THE OPERATOR, ON ITS FOUNDING RESOLUTION, ON THE STANDARD CHART; ITS FIRST DEVICES BY ENROLMENT CODE** (decided 2026-11-06). **(1) An operator script** (`46-create-society.sh`, 0023's `operator_create_society`): an API endpoint would need an Argon staff identity that does not exist, and inventing one for this is a worse shape. Named operator, server login, reason, logged, as every operator act. **(2) The backup gate:** refused while `BACKUP_STORE` is not `s3://` or the newest passing drill is over 8 days old ("ever passed" would let a September drill certify today's backups). **In the script only, never the database:** the database could only know through a row someone writes, and the owner can write it by hand; a check satisfied by asserting the thing it checks is not a check. **(3) One standard chart** for every society: a paper chart's own names mapped onto the posting roles is a migration problem, not a creation one; a society adds its own general accounts after. A reviewed standard, not variation from day one; **the Tier 1 accountant reviews it in time.** **(4) The founding resolution sets all three quorums**, never the defaults: stated, attributable, never inherited. **(5) First devices by ENROLMENT CODE, not SMS:** it waits on no procurement; it works for a member with no phone or a shared handset; every first device has the same shape (the operator issues the first officers' at handover, an officer everyone else's), so the mechanism is used from day one, not a fallback nobody trusts. Termii becomes an improvement, not a prerequisite. The code's rules: **one live code per person, single use, 30 days, stored as a digest, void after 5 wrong tries, and those tries never count against the PIN lockout. Nobody issues their own** (§10). | The reviewer | **(1)–(4) BUILT** (suite 0023: 15 tests and a control, 17 mutants, 2 control mutants; `test/ops/create_society_test.sh`: 12 tests, 12 sabotages; probe P51). **The standard chart: APPROVED 2026-11-06** (eleven accounts, `standard_chart` in 0023; 1 asset, 2 liability, 3 equity, 4 income, 5 expense, the convention a treasurer recognises; "Migration control" says it is the society's history, not a pot of money; "VAT payable" and "Statutory levy payable", separate and plainly named, make D-07's separation visible in the books: tax is held, not earned). The Tier 1 accountant reviews this one standard in time. **Reissue: decided 2026-11-06:** a new code voids the person's live one (a lost slip must not strand a member for 30 days, and the old slip stops working the moment it is replaced), and each replacement is attributable in its own right: who issued it, when, and which code it replaced, so a slip reissued three times for one person is a pattern anyone can see. **(5) DECIDED, NOT BUILT:** the next piece, tests first. |
| — | **THE ORDER** (2026-10-22): the statement stage; the session cookie; the balance rewrite and handlers; onboarding WITH officer-confirmed devices (every member's first sign-in is a new device, so a society cannot onboard anyone without it); the appointments API. | The reviewer | **DECIDED. From society creation on, superseded 2026-11-02 by §13:** society creation, then VFD, then the rest. |

**Still open, unchanged by these:** the transfer levy's incidence (Compliance, §6); the VAT registration and remittance question that D-07's "VAT on Argon's own fee" carries (Legal, §6); where a society's money is held (Legal + Product, §6).

## 13. Decided 2026-11-02: the last approval pays

**A product decision, the reviewer's.** The last approval TRIGGERS THE PAYMENT. No officer sends money by hand and no officer records a payout by hand. A society using the platform should not still be walking to a bank. The split between approving and paying existed because nothing could pay yet, not because it should be two human acts.

**The shape:** quorum reached → the system transfers through the Argon wallet on VFD → the system records the payout when the transfer confirms.

**THE ORDER CHANGES** (superseding the onboarding-onward part of §12's order row): society creation, then the VFD integration, then the rest.

| # | Decision | Owner | State |
|---|---|---|---|
| — | **THE HOLD STAYS: IT IS WHERE MONEY IS COMMITTED, AND WHAT MONEY IS WHILE IT IS IN FLIGHT.** Everything 0012 checks at the hold is unchanged and owes nothing to a person: withdrawable (savings − guarantees held − the floor − every other open hold), the charges fixed at that moment, one open hold per request, open holds counted so two payouts never draw the same money. Between quorum and VFD's confirmation the money is leaving; the hold is that state. **What goes is the second human act.** The hold is no longer placed by a signatory on a separate `withdrawal_authorisation` step-up after approval; it is placed by the system **in the same transaction as the signature that reaches quorum**, resting on that signature's own step-up. Nobody authorising their own payout is carried by the requester never signing their own withdrawal (0021). | The reviewer | **DECIDED. To be built in the VFD piece, tests first.** |
| — | **THE LAST SIGNATURE IS REFUSED WHEN THE MONEY NO LONGER FITS.** If the hold cannot be placed (the charges changed, or what the member can withdraw fell since the request was raised), the quorum-reaching signature is refused with the hold refusal's explanation: the arithmetic and the cause (`holdRefusal`). Nothing is signed, nothing is committed, and the request stays open for someone to act on. A signed request that cannot be paid is a promise the society has made and cannot keep: the member sees "approved" and waits. The signatory learns at the moment of signing instead. This replaces §12's "the second hold to be authorised is refused": the refusal moves to the last signature. | The reviewer | **DECIDED.** |
| — | **A HOLD IS RELEASED ONLY ON VFD'S DEFINITIVE ANSWER, NEVER ON A GUESS.** A timeout is not a failure. A hold released while its transfer may still land frees money that is leaving (0012 flags `payout_on_released_hold`, but by then a second payout may have gone). A requery that says failed or reversed releases it, by the system, VFD's answer as the reason, and the member is told. No answer: the hold stays open and becomes an exception. **A person cannot release a hold whose transfer is pending at VFD.** | The reviewer | **DECIDED.** |
| — | **NO AUTOMATIC RETRY.** Automatic retries are how a payment goes out twice. After a definitive failure the hold is released, the member told, the failure put on the exception screen, and **one action by a signatory sends it again**: a person in the loop at the only moment where duplication is possible. | The reviewer | **DECIDED.** |
| — | **A FAILED OR STUCK TRANSFER IS AN EXCEPTION ON SOMEONE'S SCREEN, AND THE MEMBER IS NEVER LEFT WITH SILENCE.** A payment that silently does not arrive is worse than one that needs a button. The exception is seen by **the society's signatories and Argon operations**. **Stuck** means no definitive answer **30 minutes** after sending: **PROVISIONAL**, a figure chosen before seeing the data, to be revisited against VFD's real confirmation times once they are measured. The member's own screen says **"your payment is delayed, the society has been told"**. A member whose money has not arrived and whose screen says nothing assumes the worst, and is usually right to. | The reviewer | **DECIDED; the 30 minutes PROVISIONAL.** |
| — | **VFD'S CONFIRMATION IS THE ATTRIBUTION OF A SYSTEM-RECORDED PAYOUT, IN PLACE OF A PIN STEP-UP.** The step-up proved a person was present when they asserted that money left. Here the bank's confirmation is the evidence, and it is stronger than a person's assertion. The recording is still a real act in the books, done by the system rather than a person, and it names: **what moved it** (the Argon system, never a user standing in for it); **VFD's own id for the confirmation, and its time**; **how it was confirmed** (webhook or requery); **the transfer reference**; **the hold, and through it the request and its signatures.** That chain is better attribution than a human payout ever had. Every 0012 recording rule carries over unchanged and matters more: record what happened, never refuse it; flag a different amount, a second payout on one hold, a payout on a released hold, an overdrawn member, a payout with no hold; the charges are the hold's; one transfer reference, recorded once. | The reviewer | **DECIDED.** The entry today requires a user, their post and a same-transaction `withdrawal_payout` step-up; the system path needs its own record in their place. |
| — | **0015'S DUTY SEPARATION STAYS FOR EVERYTHING A PERSON POSTS.** "The officer who authorised a hold never records its payout" separated two people. For a system-recorded payout the attesting party is the bank: a stronger separation, not a weaker one. The rule is kept for every entry a person still posts, because those paths remain. | The reviewer | **DECIDED.** |
| — | **LOAN DISBURSEMENTS GET THE SAME IN-FLIGHT STATE.** A disbursement is recorded after approval with no hold at all (0008). An automatic disbursement needs a hold as a withdrawal does. Same design, same piece. | The reviewer | **DECIDED, in the VFD piece.** |
| — | **THE PAYOUT REFERENCE STAYS UNIQUE PLATFORM-WIDE.** The fix to make it unique per society, ruled because officers typed bank references by hand, is **DROPPED**: references minted through the one Argon wallet are unique across the platform, so `payout_recorded_once` (0012) is already right. | The reviewer | **DECIDED.** |
| — | **CONTRIBUTIONS ARRIVING THROUGH VFD ARE NOT ASSUMED.** A different question: money arriving unasked, matched to a member, with no approval in front of it. | The reviewer | **OPEN: brought as its own piece when the VFD work is reached.** |
| — | **NOTHING IS DELETED UNTIL THE VFD WORK REPLACES IT.** The officer endpoints for authorising a hold, releasing it and recording a payout stay until the VFD piece replaces them, in that piece, tests first; the frontend is told then. | The reviewer | **DECIDED.** |

