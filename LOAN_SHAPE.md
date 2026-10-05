# Loans: the shape (APPROVED 2026-10-05)

**Status: approved; the contract the loan tests are written against**, as
LEDGER_CONTRACT.md is for the ledger. Kept in particular, at review: the
boundary (outstanding is always derived, never stored), consent only by the
guarantor's own step-up, and nobody marking a loan settled. Each **Decision** is marked with its owner. Nothing below
invents a figure a society has not agreed.

## 1. The boundary: the loan says what was agreed, the ledger says what is owed

A loan is two representations of one thing. Each owns one half, and neither
copies the other's half.

| The LOAN record owns | The LEDGER owns |
|---|---|
| that a member was lent money, on which product's terms | every kobo that moved: disbursement, repayments, write-off |
| principal, term, schedule (what is due, when) | the outstanding balance, **always derived, never stored on the loan** |
| guarantors and their consent | which money arrived, and on which value date |
| who approved it (the PRD 8.4 approval request) | who posted each movement, frozen at posting (§4) |

**The link:** every `loan_disbursement` and `loan_repayment` entry names its
loan (`ledger_entries.loan_id`). The column is required for those two kinds and
refused for every other. The loan's outstanding balance is the sum of its
entries' lines on the `member_loan` account, read with the books' value-date
basis. There is no `outstanding_kobo` column. A stored balance that could drift
from its entries is the failure the journal exists to prevent (§5), and the
frontend's `Loan.outstandingKobo` becomes a derived read.

**Rules at the boundary (database-enforced, like §3):**
- A loan has at most one disbursement entry, and its amount is the principal.
  A loan is active **because** its disbursement is posted, not because a flag
  says so.
- A repayment cannot be posted to a loan that is not disbursed, or to another
  member's loan, or across societies (0006's lesson, at this boundary too).
- A repayment is not refused for being more than is owed. An overpayment is
  money that arrived, and refusing to record it would make the books lie. It
  surfaces as a disagreement (§6).

## 2. What a loan is, as a record

- **Agreement:** society, borrower (member), product, principal (kobo), term,
  purpose, the approval request that authorised it, and `agreed_at`.
- **Terms snapshotted at agreement**, as attribution is (§4). The product's
  term limits, guarantor count and savings multiple are copied onto the loan
  when it is agreed. Changing a product later never rewrites a past loan.
- **The schedule:** instalment rows (number, due date, amount), fixed at
  agreement and summing to the principal. Immutable, like ledger rows. A
  reschedule is a NEW agreement that supersedes the old one, with a reason and
  its own approval, never an edit.
- **Lifecycle as append-only events**, not an editable state column: applied,
  guarantor consented or declined, approved or rejected (the approval
  request's outcome), disbursed (the ledger entry), written off (a decision,
  §6). The frontend's `review | committee | active | arrears | closed` are
  **derived** from these events and the ledger. "Arrears" in particular is a
  condition of an active loan, not a state (§5).

**Decision: interest and penalties.** *Owner: the society, with the Tier 1
accountant and Commercial (being chased).* Neither is in the PRD, and the
frontend shows no rate on purpose ("a figure an officer will quote to a
member"). Until decided, **a schedule is principal only**, and the rate is a
**configuration of the product, not an assumption in the code**: each product
carries `interest_rate_bp` (basis points, an integer, never a float). Today
the only value the schema accepts is 0, refused otherwise by name
(`interest_method_undecided`), because a rate without an agreed method of
applying it is still invented policy. When the answer comes, that refusal is
replaced by the method; no loan or code path assumed zero. When a rate is agreed, it becomes a product
term, snapshotted onto the loan. Interest then posts as its own income line on
each repayment (a separate line, as charges are, D-03). That leaves the
boundary above unchanged.

**Decision: charges on loan movements.** *Owner: Commercial.* The requirements
capture lists loans among the movements that might carry a charge. If they do,
it is the same three-line rule as contributions (§3.3). Until decided:
none.

**Decision: in-kind loans.** *Owner: Tier 1 accountant.* An "input loan" is
disbursed as seed and fertiliser, not cash. Its disbursement credits a goods or
supplier account, not the bank. That account does not exist in the chart yet.

## 3. Guarantors: a different consent from a signatory

| | Signatory (PRD 8.4, built) | Guarantor (proposed) |
|---|---|---|
| Who | an officer in a current signing post | a member of the society, not the borrower |
| Acts for | the society: releasing its money | themselves: pledging their own standing for another member |
| When | after the loan is agreed, before disbursement | **before** the approval request can be raised |
| Consent | approval of a request, quorum N of M | a personal pledge on one loan; no quorum, but the product's count must be met |
| Attribution | post at signing, frozen | the person, the time, a PIN step-up (a new act kind, `guarantee`) |
| Can refuse | yes (reject) | yes (decline), and withdraw until disbursement |

**Rules (proposed, database-enforced):**
- An approval request for a loan cannot be raised until the snapshotted number
  of current, un-withdrawn guarantor consents exists.
- A guarantor is an active member of the borrower's society, is not the
  borrower, and counts once per loan **per person, not per member record**
  (the per-user lesson from signatory_approvals).
- Consent is the guarantor's own act, through their own step-up. An officer
  cannot record it on their behalf, or the consent is decorative.

**Decision: what a guarantor is liable for.** *Owner: the society (bye-laws),
with Legal (being chased).* Is it a share of the principal, the whole, or only
what remains after the borrower's savings? Are the guarantor's own savings
**locked** (withdrawable falls, as with project commitments) or only pledged?
On default, recovery from a guarantor is a ledger movement with its own
approval, never an automatic sweep.

**Decided 2026-10-05: a signatory on a loan may NOT also guarantee it**, in
either order. The signatory releases the society's money; the guarantor
pledges their own standing against the same loan. One person doing both puts
the control that protects the society and the control that protects the loan
on one judgement.

## 4. Who may do what (summary)

The member applies for themselves. Guarantors consent for themselves. The
approval request follows the existing signatory flow (PRD 8.4, quorum and
expiry as built). Disbursement and repayment entries are posted like any other
entry (§4 attribution). The internal auditor reads everything and does nothing.

## 5. Arrears: computed by the server, never stored, never by the client

For an active loan **as at a value date D**:
- **due** = the sum of instalments with a due date on or before D;
- **paid** = the loan's repayment lines on `member_loan` with value date on or before D;
- **arrears** = due − paid, when positive.

**Missed instalments:** repayments are allocated to the **oldest instalment
first**. An instalment is missed when its due date has passed and the
allocated payments have not covered it. **Days in arrears** run from the due
date of the oldest uncovered instalment.

Returned by the server with its reason in words, for the same reason as the
inbox's urgency: two clients computing it would be two answers. For example:
"2 instalments missed: ₦41,666 due 5 August, unpaid; ₦41,667 due 5 September,
₦20,000 paid." The frontend's `Loan.missed` and its arrears tab read this.

**Decided 2026-10-05:** the build specification's "no arrears state in the
product" means **fees**. It came from dropping society billing: there is no
invoice, so no society can be behind on one. Loan arrears are a different
thing and they exist. The specification now says so.

## 6. "Settled", when the ledger is the authority on the balance

**Nobody marks a loan settled.** A loan's condition is derived from the two
halves:

| Schedule | Ledger balance (derived) | Condition |
|---|---|---|
| complete | zero | **settled** |
| incomplete | zero | **settled early** (if the product allows prepayment; *Decision, society*) |
| complete or not | above zero, schedule fully due | **disagreement: underpaid** |
| any | below zero | **disagreement: overpaid**. The society owes the member. |

A **disagreement** is not resolved on the loan. The ledger wins on what is
owed, and the loan record never overrides it. It surfaces as an item in an
officer's queue (the inbox, with its reason). It is resolved by correcting the
MONEY, never by editing the loan:
- a missing repayment is posted;
- a wrong one is reversed;
- an overpayment is refunded as its own movement;
- a debt that will not be recovered is **written off**. That is a decision with
  its own approval (proposed 2-of-N, like a reversal over the threshold),
  posting Dr bad-debt expense / Cr `member_loan`. The balance reaches zero, and
  the condition then reads **written off**, not settled.

Each of those is an attributable ledger act with a reason. After it, the
condition follows by derivation. So "settled" can never be true while the
ledger says money is owed, and it can never be set by someone typing it.

## 7. What the tests would hold (once approved)

- **Schema:**
  - loan_id required on loan movements and refused on others;
  - one disbursement per loan, equal to its principal;
  - no repayment before disbursement, nor to another member's loan, nor across societies;
  - the schedule is immutable and sums to the principal;
  - guarantor consents are per person, and the count is met before the request is raised;
  - a guarantor is never the borrower.
- **Reads:**
  - outstanding is derived and agrees with the general ledger for `member_loan` (two reads, one record);
  - arrears and missed instalments for a fixture with partial payments;
  - each settled / settled-early / underpaid / overpaid / written-off condition, and every disagreement surfacing with its reason.
- **The discriminating question (finding 12)** for each: what would this test do if the decision had gone the other way?

## 8. Open decisions, by owner

| Decision | Owner |
|---|---|
| Interest and penalties: none until set | Society, Tier 1 accountant, Commercial |
| Charges on loan movements | Commercial |
| In-kind disbursement account | Tier 1 accountant |
| Guarantor liability; are their savings locked | Society bye-laws, Legal |
| ~~May a signatory also guarantee the same loan~~ | **Decided: no**, in either order |
| Prepayment allowed (settled early) | Society |
| Write-off approval quorum | Society (proposed: as reversals over threshold) |
| ~~"No arrears state" applies to fees, not loans~~ | **Decided: fees only**; spec amended |
| Loans funding projects (requirements capture) | Later; routes through this same loan |
