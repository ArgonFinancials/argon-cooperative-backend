//go:build tier1

// Acceptance tests for a member's withdrawable figure and the withdrawal
// quote — written before the handlers, for review, in pieces (finding 46).
// Every TestApiBalance* name is here.
//
//	GET /members/{id}/withdrawable        savings, what may be withdrawn, what was deducted and why
//	GET /members/{id}/withdrawal-quote    what a withdrawal of an amount would cost today
//
// Decisions these tests PIN (for the reviewer, 2026-10-20):
//
//	D1 A GUARANTEE HOLDS BACK THE GUARANTEED LOAN'S WHOLE OUTSTANDING BALANCE
//	   (the conservative default, pending the society's liability decision),
//	   and the member is told WHY: one deduction per guaranteed loan, naming
//	   the borrower and saying it is held in full. A repaid loan holds nothing
//	   and is not listed. (Tests 02, 03, 06.)
//	D2 THE FLOOR UNSET: the savings are always shown; the withdrawable figure is
//	   NULL, with the reason, never a zero that reads as "you have nothing".
//	   (Test 04.)
//	D3 THE QUOTE IS INDICATIVE; THE HOLD'S CHARGE IS AUTHORITATIVE. A quote says
//	   so in words; the charge fixed when the payout is authorised is shown on
//	   the member's request, and applies even when the series changed between
//	   the quote and the hold. (Tests 09, 10.)
//	D4 THE CHARGE COMES OUT OF THE WITHDRAWABLE FIGURE (proposed): the figure is
//	   savings less deductions, and a request fits when the amount AND its
//	   charge fit. THE FIGURE SHOWN IS THE FIGURE ENFORCED: the largest amount
//	   the quote says fits is accepted when raised, one kobo more refused.
//	   (Test 07.)
//	D5 WHO READS: the member, and an officer of the member's society (who raises
//	   for a member without a phone). Anyone else: 404, as for no member.
//	   (Test 08.)
package tier1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type bDeduction struct {
	Kind, AmountKobo, Reason string
	LoanId, BorrowerName     *string
}

type bRequested struct {
	RequestId, AmountKobo, TotalKobo, RaisedAt, Note string
	QuotedCharges                                    mCharges
}

type bWithdrawable struct {
	MemberId, SavingsBalanceKobo, AsOf string
	WithdrawableKobo                   *string
	LargestRequestableKobo             *string
	Unavailable                        *struct{ Code, Message string }
	Deductions                         []bDeduction
	Requested                          []bRequested
}

type bQuote struct {
	AmountKobo, TotalOutKobo, QuotedOn, Note string
	Charges                                  mCharges
	WithinWithdrawable                       *bool
	WithdrawableUnavailable                  *struct{ Code, Message string }
	Indicative                               bool
}

func (w *mapi) withdrawable(t *testing.T, as, member string) resp {
	return w.call(t, http.MethodGet, as, "/members/"+member+"/withdrawable", nil)
}

func (w *mapi) position(t *testing.T, as, member string) bWithdrawable {
	t.Helper()
	return decoded[bWithdrawable](t, w.withdrawable(t, as, member), http.StatusOK, "the member's position")
}

func (w *mapi) quoteVia(t *testing.T, as, member string, amount int64) bQuote {
	t.Helper()
	return decoded[bQuote](t, w.call(t, http.MethodGet, as, "/members/"+member+"/withdrawal-quote?amountKobo="+k(amount), nil), http.StatusOK, "the quote")
}

func deductionsOf(p bWithdrawable, kind string) []bDeduction {
	var out []bDeduction
	for _, d := range p.Deductions {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// guarantorWorld: guarantor one has saved ₦10,000 and guarantees the
// borrower's ₦24,000 loan, disbursed, of which `owed` is still owed.
func (w *mapi) guarantorWorld(t *testing.T, owed int64) (loan string) {
	t.Helper()
	w.saved(t, w.g1Mem, loanSavings)
	loan, _ = w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	if repaid := loanPrincipal - owed; repaid > 0 {
		_, err = w.repay(loan, repaid)
		fixture(t, err, "repayments")
	}
	return loan
}

// =============================================================================
// THE FIGURE AND WHY
// =============================================================================

// Savings ₦10,000, the society's floor ₦1,000: ₦9,000, and the floor named.
func TestApiBalance01_TheFigureAndTheFloor(t *testing.T) {
	w := newMoneyAPI(t)
	p := w.position(t, w.borrower, w.bMem)
	if p.SavingsBalanceKobo != k(loanSavings) || p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(borrowerWithdrawable) || p.Unavailable != nil {
		t.Fatalf("WRONG FIGURE — %+v; want savings %s, withdrawable %s", p, k(loanSavings), k(borrowerWithdrawable))
	}
	f := deductionsOf(p, "floor")
	if len(f) != 1 || f[0].AmountKobo != k(floorKobo) || !strings.Contains(f[0].Reason, "minimum balance") {
		t.Fatalf("UNEXPLAINED — the floor's deduction: %+v; want %s, saying it is the society's minimum balance", f, k(floorKobo))
	}
}

// D1: a guarantee holds back the loan's WHOLE outstanding balance, and says so.
// ₦10,000 saved, ₦3,000 still owed on the guaranteed loan, the ₦1,000 floor: ₦6,000.
func TestApiBalance02_AGuaranteeIsHeldInFullAndSaysWhy(t *testing.T) {
	w := newMoneyAPI(t)
	loan := w.guarantorWorld(t, 300000)
	p := w.position(t, w.g1, w.g1Mem)
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(loanSavings-300000-floorKobo) {
		t.Fatalf("WRONG FIGURE — %+v; want %s (savings less the ₦3,000 owed and the floor)", p, k(loanSavings-300000-floorKobo))
	}
	g := deductionsOf(p, "guarantee")
	if len(g) != 1 || g[0].AmountKobo != k(300000) || g[0].LoanId == nil || *g[0].LoanId != loan ||
		g[0].BorrowerName == nil || *g[0].BorrowerName != "Borrower" {
		t.Fatalf("UNATTRIBUTED — the guarantee's deduction: %+v; want ₦3,000 on loan %s, the borrower named", g, loan)
	}
	if r := g[0].Reason; !strings.Contains(r, "Borrower") || !strings.Contains(r, "in full") {
		t.Fatalf("UNEXPLAINED — %q; the member must be told whose loan, and that the whole of what is owed is held", r)
	}
}

// A loan repaid in full holds nothing, and is not listed as if it did. The
// absence is COMPUTED, not a state the guarantee moved into: the guarantee
// still exists and holds nothing, so when the loan owes again (here the last
// repayment is reversed, as when a bank returns it) the deduction comes back by
// itself.
func TestApiBalance03_ARepaidLoanHoldsNothingUntilItOwesAgain(t *testing.T) {
	w := newMoneyAPI(t)
	loan := w.guarantorWorld(t, 300000)
	last, err := w.repay(loan, 300000)
	fixture(t, err, "the last ₦3,000 repaid")
	p := w.position(t, w.g1, w.g1Mem)
	if g := deductionsOf(p, "guarantee"); len(g) != 0 || p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(loanSavings-floorKobo) {
		t.Fatalf("HELD — a loan repaid in full: %+v; want no guarantee deduction and %s", p, k(loanSavings-floorKobo))
	}
	fixture(t, w.reverse(loan, last, 300000, "The bank returned this repayment: the transfer was recalled"), "the last repayment reversed")
	p = w.position(t, w.g1, w.g1Mem)
	if g := deductionsOf(p, "guarantee"); len(g) != 1 || g[0].AmountKobo != k(300000) || *p.WithdrawableKobo != k(loanSavings-300000-floorKobo) {
		t.Fatalf("SETTLED FOR GOOD — the loan owes ₦3,000 again and the guarantee holds %+v; want the deduction back, computed from what is owed now", p)
	}
}

// reverse: a reversal of a repayment of `kobo` on `loan`, a NEW entry pointing
// at it (0001: the original is never altered).
func (w *mapi) reverse(loan, original string, kobo int64, reason string) error {
	return tx(func(x pgx.Tx) error {
		var id string
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, loan_id, reverses_id, reason)
			VALUES ($1, 'loan_repayment', '2026-10-06', $2, 'treasurer', 'Treasurer', $3, $4, $5) RETURNING id`,
			w.c.s.id, w.c.s.actor, loan, original, reason).Scan(&id); err != nil {
			return err
		}
		return w.c.s.insertLines(x, id, line{acct: w.c.loan, kobo: kobo, member: w.bMem}, line{acct: w.c.bank, kobo: -kobo})
	})
}

// D2: the society has set no floor. Savings shown; the figure NULL, with why;
// and (2026-10-26, piece 1) no largest requestable figure beside it: a member
// with no floor set is never handed a confident "largest" next to a blank.
func TestApiBalance04_AnUnsetFloorShowsSavingsAndWhyNotTheRest(t *testing.T) {
	w := newMoneyAPI(t)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "a society that has set no floor")
	r := w.withdrawable(t, w.borrower, w.bMem)
	p := decoded[bWithdrawable](t, r, http.StatusOK, "the position with no floor")
	if p.SavingsBalanceKobo != k(loanSavings) {
		t.Fatalf("HIDDEN — the savings must always be shown: %s", r.body)
	}
	if p.WithdrawableKobo != nil || p.Unavailable == nil || p.Unavailable.Code != "floor_unset" || !strings.Contains(p.Unavailable.Message, "minimum balance") {
		t.Fatalf("WRONG — %s; want withdrawableKobo null and floor_unset, saying the society has not set its minimum balance", r.body)
	}
	if p.LargestRequestableKobo != nil {
		t.Fatalf("CONFIDENT BESIDE A BLANK — no floor, yet largestRequestableKobo is %s; want null with the figure", *p.LargestRequestableKobo)
	}
	// The quote still prices the fee; whether it fits is unknown, and the quote
	// says WHY beside the fee, or the fee reads as the live part of the screen
	// and the blank beside it as a loading state.
	q := w.quoteVia(t, w.borrower, w.bMem, payKobo)
	if q.Charges.TotalKobo != "1075" || q.WithinWithdrawable != nil || q.WithdrawableUnavailable == nil ||
		q.WithdrawableUnavailable.Code != "floor_unset" || q.WithdrawableUnavailable.Message != p.Unavailable.Message {
		t.Fatalf("UNEXPLAINED BLANK — the quote with no floor: %+v; want the fee, withinWithdrawable null, and the same floor_unset reason as /withdrawable", q)
	}
}

// An authorised payout not yet paid is spoken for, its charges with it:
// ₦9,000 less ₦6,000 and its ₦10.75.
func TestApiBalance05_AnAuthorisedPayoutIsSpokenFor(t *testing.T) {
	w := newMoneyAPI(t)
	w.heldVia(t, payKobo)
	p := w.position(t, w.borrower, w.bMem)
	held := payKobo + 1075
	a := deductionsOf(p, "authorised_payout")
	if len(a) != 1 || a[0].AmountKobo != k(held) || !strings.Contains(a[0].Reason, "authorised") {
		t.Fatalf("UNSEEN — the authorised payout: %+v; want %s, saying it is authorised and not yet paid", a, k(held))
	}
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(borrowerWithdrawable-held) {
		t.Fatalf("WRONG FIGURE — %+v; want %s", p, k(borrowerWithdrawable-held))
	}
}

// Owing more on the guaranteed loan than they saved: nothing to withdraw,
// never a negative figure. And the savings beside it, with the deduction
// that took it: "you can withdraw ₦0" alone reads as an error or an empty
// account.
func TestApiBalance06_NeverBelowZeroAndTheSavingsBesideIt(t *testing.T) {
	w := newMoneyAPI(t)
	w.guarantorWorld(t, loanPrincipal)
	p := w.position(t, w.g1, w.g1Mem)
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != "0" {
		t.Fatalf("WRONG FIGURE — %+v; want 0 (₦24,000 held against ₦10,000 saved), never below", p)
	}
	if g := deductionsOf(p, "guarantee"); p.SavingsBalanceKobo != k(loanSavings) || len(g) != 1 || g[0].AmountKobo != k(loanPrincipal) {
		t.Fatalf("UNEXPLAINED ZERO — %+v; want the ₦10,000 saved shown beside the 0, and the ₦24,000 guarantee that took it", p)
	}
}

// D4: THE FIGURE SHOWN IS THE FIGURE ENFORCED, and THE SERVER SAYS IT
// (2026-10-26, piece 1): largestRequestableKobo comes from the database's
// largest_withdrawal (0017), never worked out here. Accepted at exactly that
// amount, refused one kobo above. Two cases: the ordinary one, and the band
// boundary, where "withdrawable less the charge on withdrawable" is wrong.
func TestApiBalance07_TheFigureShownIsTheFigureEnforced(t *testing.T) {
	w := newMoneyAPI(t)
	largestOf := func(want int64, what string) {
		t.Helper()
		p := w.position(t, w.borrower, w.bMem)
		if p.LargestRequestableKobo == nil || *p.LargestRequestableKobo != k(want) {
			t.Fatalf("WRONG LARGEST — %s: %s; want %s", what, orNone(p.LargestRequestableKobo), k(want))
		}
		// The quote agrees with the figure: fits at the largest, not one kobo above.
		if q := w.quoteVia(t, w.borrower, w.bMem, want); q.WithinWithdrawable == nil || !*q.WithinWithdrawable {
			t.Fatalf("QUOTE DISAGREES — %s: the largest requestable %s quoted as not fitting: %+v", what, k(want), q)
		}
		if q := w.quoteVia(t, w.borrower, w.bMem, want+1); q.WithinWithdrawable == nil || *q.WithinWithdrawable {
			t.Fatalf("QUOTE DISAGREES — %s: one kobo above the largest quoted as fitting (its charge forgotten?): %+v", what, q)
		}
		if r := w.raiseVia(t, w.borrower, w.bMem, k(want), stepUp(t, w.borrower, "withdrawal_request"), "", nil); r.status != http.StatusCreated {
			t.Fatalf("NOT ENFORCED AS SHOWN — %s: the largest requestable %s was refused when raised: %d %s", what, k(want), r.status, r.body)
		}
		r := w.raiseVia(t, w.borrower, w.bMem, k(want+1), stepUp(t, w.borrower, "withdrawal_request"), "", nil)
		wantProblem(t, r, http.StatusUnprocessableEntity, "exceeds_withdrawable", what+": one kobo more than the largest requestable")
	}
	// 1. ₦9,000 withdrawable, the ₦10.75 band: ₦8,989.25.
	largestOf(borrowerWithdrawable-1075, "₦9,000 withdrawable")
	// 2. ₦10,050 withdrawable. From ₦10,000 the levy applies (₦60.75 in all),
	// so nothing from ₦10,000 fits; ₦9,999.99 costs ₦10.75 and fits. The old
	// arithmetic (₦10,050 less ₦60.75 = ₦9,989.25) is short by ₦10.74.
	w.saved(t, w.bMem, 105000)
	largestOf(999999, "₦10,050 withdrawable, at the levy's threshold")
}

// D5 (decided 2026-10-21): READING IS TIED TO THE DUTY. The member; the
// treasurer and the secretary, who raise for a member without a phone; the
// auditor, whose task is auditing. A member's savings are their personal
// position, not the society's books: an officer whose duty does not need it
// (the chairman) gets the same 404 as anyone else.
func TestApiBalance08_WhoReads(t *testing.T) {
	w := newMoneyAPI(t)
	own := w.position(t, w.borrower, w.bMem)
	for who, u := range map[string]string{"the treasurer": w.poster, "the secretary": w.secretary, "the internal auditor": w.auditor} {
		off := w.position(t, u, w.bMem)
		if off.WithdrawableKobo == nil || own.WithdrawableKobo == nil || *off.WithdrawableKobo != *own.WithdrawableKobo {
			t.Fatalf("REFUSED — %s, whose duty needs it, must see the member's figure: %+v", who, off)
		}
	}
	none := w.withdrawable(t, w.g1, noSuchRow)
	for who, u := range map[string]string{"another member": w.g1, "the treasurer of another society": w.abroad,
		"the chairman (no duty needs a member's savings)": w.s2} {
		if r := w.withdrawable(t, u, w.bMem); r.status != http.StatusNotFound || string(r.body) != string(none.body) {
			t.Fatalf("ACCEPTED — %s read the borrower's savings: %d %s", who, r.status, r.body)
		}
		if r := w.call(t, http.MethodGet, u, "/members/"+w.bMem+"/withdrawal-quote?amountKobo="+k(payKobo), nil); r.status != http.StatusNotFound {
			t.Fatalf("ACCEPTED — %s quoted against the borrower's savings: %d %s", who, r.status, r.body)
		}
	}
}

// =============================================================================
// THE QUOTE: INDICATIVE; THE HOLD'S CHARGE APPLIES (D3)
// =============================================================================

// ₦6,000: the ₦10 fee band, VAT 7.5% of it, no levy: ₦10.75. Indicative, and
// says so in words a member reads.
func TestApiBalance09_AQuoteSaysItIsIndicative(t *testing.T) {
	w := newMoneyAPI(t)
	q := w.quoteVia(t, w.borrower, w.bMem, payKobo)
	if q.Charges.TotalKobo != "1075" || q.TotalOutKobo != k(payKobo+1075) || q.WithinWithdrawable == nil || !*q.WithinWithdrawable {
		t.Fatalf("WRONG QUOTE — %+v; want ₦10.75 on ₦6,000, ₦6,010.75 out, within", q)
	}
	if !q.Indicative || !strings.Contains(q.Note, "indicative") || !strings.Contains(q.Note, "fixed when the payout is authorised") {
		t.Fatalf("BINDING-LOOKING — %+v; a quote must say it is indicative and when the figure is fixed", q)
	}
	if q.QuotedOn != time.Now().Format("2006-01-02") {
		t.Fatalf("UNDATED — quoted on %q; want today", q.QuotedOn)
	}
}

// The series changes between the raise and the hold (2026-10-26, piece 3):
// the request KEEPS the charge it was quoted (0017) beside the charge the hold
// applies, and says why they differ, naming the date the charges changed.
func TestApiBalance10_TheQuotedChargeBesideTheChargeApplied(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, v.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	c := memberRequestOf(t, w, v.RequestId)
	if c.QuotedCharges == nil || c.QuotedCharges.TotalKobo != "1075" || c.AuthorisedCharges != nil || c.ChargeChanged != nil {
		t.Fatalf("WRONG BEFORE THE HOLD — quoted %v, authorised %v, changed %v; want 1075 quoted, nothing authorised, no sentence", c.QuotedCharges, c.AuthorisedCharges, c.ChargeChanged)
	}
	// From today, the society's fee is ₦25 flat (its VAT 7.5%: ₦1.88): ₦26.88.
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", feeComponent(), time.Now().Format("2006-01-02"), "TEST DATA ONLY: a fee changed today", band{from: 0, flat: 2500})
	fixture(t, err, "today's new fee series")
	if r := w.authorise(t, w.s2, v.RequestId, stepUp(t, w.s2, "withdrawal_authorisation")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the hold: %d %s", r.status, r.body)
	}
	c = memberRequestOf(t, w, v.RequestId)
	if c.QuotedCharges == nil || c.QuotedCharges.TotalKobo != "1075" {
		t.Fatalf("RECOMPUTED — the quoted charge now reads %v; want the 1075 kept from the raise", c.QuotedCharges)
	}
	if c.AuthorisedCharges == nil || c.AuthorisedCharges.TotalKobo != "2688" {
		t.Fatalf("WRONG CHARGE — authorised %v; want the hold's 2688 (the series in force when authorised)", c.AuthorisedCharges)
	}
	if c.ChargeChanged == nil || !strings.Contains(*c.ChargeChanged, "₦10.75") || !strings.Contains(*c.ChargeChanged, "₦26.88") || !strings.Contains(*c.ChargeChanged, time.Now().Format("2 January 2006")) {
		t.Fatalf("UNSAID — quoted ₦10.75, charged ₦26.88, and chargeChanged reads %v; want both amounts and the date the charges changed", c.ChargeChanged)
	}
	if now := w.quoteVia(t, w.borrower, w.bMem, payKobo); now.Charges.TotalKobo != "2688" {
		t.Fatalf("STALE — a quote today reads %s; want today's series, 2688", now.Charges.TotalKobo)
	}
}

type bMemberRequest struct {
	RequestId                        string
	QuotedCharges, AuthorisedCharges *mCharges
	ChargeChanged                    *string
}

// memberRequestOf: the request as the member's own list shows it.
func memberRequestOf(t *testing.T, w *mapi, req string) bMemberRequest {
	t.Helper()
	var l struct{ Items []bMemberRequest }
	r := w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil)
	if r.status != http.StatusOK {
		t.Fatalf("FIXTURE: the member's list: %d %s", r.status, r.body)
	}
	fixture(t, json.Unmarshal(r.body, &l), "the member's list")
	for _, it := range l.Items {
		if it.RequestId == req {
			return it
		}
	}
	t.Fatalf("FIXTURE: request %s not in the member's list: %s", req, r.body)
	return bMemberRequest{}
}

// =============================================================================
// THE WIRE
// =============================================================================

func TestApiBalance11_MoneyIsAStringOnTheWire(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.withdrawable(t, w.borrower, w.bMem)
	if b := string(r.body); !strings.Contains(b, `"savingsBalanceKobo":"`+k(loanSavings)+`"`) || !strings.Contains(b, `"withdrawableKobo":"`+k(borrowerWithdrawable)+`"`) {
		t.Fatalf("ACCEPTED — money is not a string: %s", r.body)
	}
}

// =============================================================================
// REQUESTED, NOT YET AUTHORISED (2026-10-26, piece 2)
// =============================================================================

// A request is a question; the hold is where money is committed. Listed with
// its quoted charge and NOT deducted; once authorised it leaves the list and
// becomes the authorised_payout deduction; never both at once.
func TestApiBalance12_ARequestIsListedAndNotTakenUntilItIsAuthorised(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	p := w.position(t, w.borrower, w.bMem)
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(borrowerWithdrawable) {
		t.Fatalf("TAKEN — an open request reduced the figure to %s; want %s, nothing taken until it is approved", orNone(p.WithdrawableKobo), k(borrowerWithdrawable))
	}
	if len(p.Requested) != 1 || p.Requested[0].RequestId != v.RequestId || p.Requested[0].AmountKobo != k(payKobo) ||
		p.Requested[0].QuotedCharges.TotalKobo != "1075" || p.Requested[0].TotalKobo != k(payKobo+1075) || !strings.Contains(p.Requested[0].Note, "Not taken from what you can withdraw") {
		t.Fatalf("UNSEEN — requested %+v; want the ₦6,000 request, quoted 1075, 601075 in all, with the sentence", p.Requested)
	}
	if p.LargestRequestableKobo == nil || *p.LargestRequestableKobo != k(borrowerWithdrawable-1075) {
		t.Fatalf("TAKEN — the largest requestable moved to %s with an open request; want %s", orNone(p.LargestRequestableKobo), k(borrowerWithdrawable-1075))
	}
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, v.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	if r := w.authorise(t, w.s2, v.RequestId, stepUp(t, w.s2, "withdrawal_authorisation")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the hold: %d %s", r.status, r.body)
	}
	p = w.position(t, w.borrower, w.bMem)
	if len(p.Requested) != 0 {
		t.Fatalf("TWICE — after its hold the request is still listed as requested: %+v", p.Requested)
	}
	if a := deductionsOf(p, "authorised_payout"); len(a) != 1 || a[0].AmountKobo != k(payKobo+1075) {
		t.Fatalf("UNSEEN — the authorised payout: %+v; want %s", a, k(payKobo+1075))
	}
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(borrowerWithdrawable-payKobo-1075) {
		t.Fatalf("WRONG FIGURE — %s; want %s, the hold taken once", orNone(p.WithdrawableKobo), k(borrowerWithdrawable-payKobo-1075))
	}
}

// The ordinary case: nothing changed between the raise and the hold, so
// nothing is said. A note that appears when nothing changed teaches members to
// ignore it (piece 3).
func TestApiBalance13_NoChangeNoSentence(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, v.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	if r := w.authorise(t, w.s2, v.RequestId, stepUp(t, w.s2, "withdrawal_authorisation")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the hold: %d %s", r.status, r.body)
	}
	c := memberRequestOf(t, w, v.RequestId)
	if c.QuotedCharges == nil || c.AuthorisedCharges == nil || c.QuotedCharges.TotalKobo != "1075" || c.AuthorisedCharges.TotalKobo != "1075" {
		t.Fatalf("WRONG — quoted %v, authorised %v; want 1075 both", c.QuotedCharges, c.AuthorisedCharges)
	}
	if c.ChargeChanged != nil {
		t.Fatalf("CRYING WOLF — nothing changed, yet chargeChanged reads %q", *c.ChargeChanged)
	}
}

// =============================================================================
// A REFUSED HOLD SAYS WHAT CHANGED (2026-10-26, piece 4)
// =============================================================================

type bHoldRefusal struct {
	Code, Message                                                             string
	AmountKobo, QuotedChargeKobo, ChargeNowKobo, NeededKobo, WithdrawableKobo *string
	Causes                                                                    []string
}

func holdRefusalOf(t *testing.T, r resp) bHoldRefusal {
	t.Helper()
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("ACCEPTED — the hold: %d %s; want 422 exceeds_withdrawable", r.status, r.body)
	}
	var h bHoldRefusal
	fixture(t, json.Unmarshal(r.body, &h), "the refusal")
	return h
}

func orNone(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}

// The charges change between the raise and the hold: ₦6,000 quoted at ₦10.75
// fitted ₦6,020; at ₦26.88 it does not. The hold's charge applies (D3): the
// refusal says the charge changed, and only that.
func TestApiBalance14_AHoldRefusedBecauseTheChargesChangedSaysSo(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.g1Mem, 602000+floorKobo) // ₦6,020 withdrawable
	req := w.approved(t, w.g1Mem, payKobo)
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", feeComponent(), time.Now().Format("2006-01-02"), "TEST DATA ONLY: a fee changed today", band{from: 0, flat: 2500})
	fixture(t, err, "today's new fee series")
	h := holdRefusalOf(t, w.authorise(t, w.s2, req, stepUp(t, w.s2, "withdrawal_authorisation")))
	if h.Code != "exceeds_withdrawable" || orNone(h.AmountKobo) != k(payKobo) || orNone(h.QuotedChargeKobo) != "1075" || orNone(h.ChargeNowKobo) != "2688" ||
		orNone(h.NeededKobo) != k(payKobo+2688) || orNone(h.WithdrawableKobo) != "602000" {
		t.Fatalf("WRONG ARITHMETIC — %+v; want amount %s, quoted 1075, now 2688, needed %s, withdrawable 602000", h, k(payKobo), k(payKobo+2688))
	}
	if len(h.Causes) != 1 || h.Causes[0] != "charge_changed" {
		t.Fatalf("WRONG CAUSE — %v; want exactly [charge_changed]: the charges moved, the member's money did not", h.Causes)
	}
	if !strings.Contains(h.Message, "₦10.75") || !strings.Contains(h.Message, "₦26.88") {
		t.Fatalf("UNSAID — %q; want both charges named", h.Message)
	}
}

// Two requests that each fit alone (piece 2's designed consequence): both are
// raised, the first hold succeeds, the second is refused because the first
// took the money, and nothing about the charge changed.
func TestApiBalance15_TheSecondOfTwoHoldsIsRefusedBecauseTheFirstTookTheMoney(t *testing.T) {
	w := newMoneyAPI(t)
	first := w.approved(t, w.bMem, payKobo)
	second := w.approved(t, w.bMem, payKobo)
	if r := w.authorise(t, w.s2, first, stepUp(t, w.s2, "withdrawal_authorisation")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the first hold: %d %s", r.status, r.body)
	}
	h := holdRefusalOf(t, w.authorise(t, w.s2, second, stepUp(t, w.s2, "withdrawal_authorisation")))
	if len(h.Causes) != 1 || h.Causes[0] != "withdrawable_fell" {
		t.Fatalf("WRONG CAUSE — %v; want exactly [withdrawable_fell]: the first payout took the money", h.Causes)
	}
	if orNone(h.WithdrawableKobo) != k(borrowerWithdrawable-payKobo-1075) || orNone(h.NeededKobo) != k(payKobo+1075) ||
		orNone(h.QuotedChargeKobo) != "1075" || orNone(h.ChargeNowKobo) != "1075" {
		t.Fatalf("WRONG ARITHMETIC — %+v; want withdrawable %s, needed %s, quoted and now both 1075", h, k(borrowerWithdrawable-payKobo-1075), k(payKobo+1075))
	}
}

// A hold authorised and then RELEASED (the transfer will not happen): the
// money comes back and the request is open again, so it is listed again. A
// member who could not see it would think the request had vanished; an
// officer would see nothing to act on (piece 3, 2026-10-28).
func TestApiBalance16_AReleasedHoldPutsTheRequestBackInTheList(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, v.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	r := w.authorise(t, w.s2, v.RequestId, stepUp(t, w.s2, "withdrawal_authorisation"))
	if r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the hold: %d %s", r.status, r.body)
	}
	var h mHold
	fixture(t, json.Unmarshal(r.body, &h), "the hold")
	if rr := w.releaseVia(t, w.s2, h.HoldId, "The member's bank account details were wrong"); rr.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the release: %d %s", rr.status, rr.body)
	}
	p := w.position(t, w.borrower, w.bMem)
	if len(p.Requested) != 1 || p.Requested[0].RequestId != v.RequestId {
		t.Fatalf("VANISHED — after its hold was released the request is not listed: %+v; want it open again", p.Requested)
	}
	if p.WithdrawableKobo == nil || *p.WithdrawableKobo != k(borrowerWithdrawable) || len(deductionsOf(p, "authorised_payout")) != 0 {
		t.Fatalf("NOT RETURNED — figure %s, deductions %+v; want %s, the released hold taking nothing", orNone(p.WithdrawableKobo), p.Deductions, k(borrowerWithdrawable))
	}
}
