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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type bDeduction struct {
	Kind, AmountKobo, Reason string
	LoanId, BorrowerName     *string
}

type bWithdrawable struct {
	MemberId, SavingsBalanceKobo, AsOf string
	WithdrawableKobo                   *string
	Unavailable                        *struct{ Code, Message string }
	Deductions                         []bDeduction
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

// D2: the society has set no floor. Savings shown; the figure NULL, with why.
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

// D4: THE FIGURE SHOWN IS THE FIGURE ENFORCED. The whole ₦9,000 does not fit
// (its charge comes out of it); ₦9,000 less that charge does, and is accepted
// when raised; one kobo more is refused by the database.
func TestApiBalance07_TheFigureShownIsTheFigureEnforced(t *testing.T) {
	w := newMoneyAPI(t)
	all := w.quoteVia(t, w.borrower, w.bMem, borrowerWithdrawable)
	if all.WithinWithdrawable == nil || *all.WithinWithdrawable {
		t.Fatalf("WRONG — the whole withdrawable figure, its charge on top, quoted as fitting: %+v", all)
	}
	charge, err := strconv.ParseInt(all.Charges.TotalKobo, 10, 64)
	fixture(t, err, "the quote's charge, a kobo string")
	most := borrowerWithdrawable - charge
	q := w.quoteVia(t, w.borrower, w.bMem, most)
	if q.Charges.TotalKobo != all.Charges.TotalKobo || q.WithinWithdrawable == nil || !*q.WithinWithdrawable || q.TotalOutKobo != k(borrowerWithdrawable) {
		t.Fatalf("WRONG — the largest amount that fits, %s: %+v; want within, %s out", k(most), q, k(borrowerWithdrawable))
	}
	if r := w.raiseVia(t, w.borrower, w.bMem, k(most), stepUp(t, w.borrower, "withdrawal_request"), "", nil); r.status != http.StatusCreated {
		t.Fatalf("NOT ENFORCED AS SHOWN — the amount quoted as fitting was refused: %d %s", r.status, r.body)
	}
	r := w.raiseVia(t, w.borrower, w.bMem, k(most+1), stepUp(t, w.borrower, "withdrawal_request"), "", nil)
	wantProblem(t, r, http.StatusUnprocessableEntity, "exceeds_withdrawable", "one kobo more than the quote says fits")
}

// D5: the member and an officer of the society read it; nobody else.
func TestApiBalance08_WhoReads(t *testing.T) {
	w := newMoneyAPI(t)
	own := w.position(t, w.borrower, w.bMem)
	off := w.position(t, w.secretary, w.bMem)
	if off.WithdrawableKobo == nil || own.WithdrawableKobo == nil || *off.WithdrawableKobo != *own.WithdrawableKobo {
		t.Fatalf("REFUSED — the secretary, raising for a member without a phone, must see the member's figure: %+v", off)
	}
	none := w.withdrawable(t, w.g1, noSuchRow)
	for who, u := range map[string]string{"another member": w.g1, "the treasurer of another society": w.abroad} {
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

// The series changes between the quote and the hold: the HOLD's charge
// applies, and the member is shown it on their request.
func TestApiBalance10_TheHoldsChargeAppliesAndTheMemberIsShownIt(t *testing.T) {
	w := newMoneyAPI(t)
	before := w.quoteVia(t, w.borrower, w.bMem, payKobo)
	v := w.raisedVia(t, payKobo)
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, v.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	if c := memberRequestCharges(t, w, v.RequestId); c != nil {
		t.Fatalf("PREMATURE — a charge shown before the payout is authorised: %s", *c)
	}
	// From today, the society's fee is ₦25 flat (its VAT 7.5%: ₦1.88): ₦26.88.
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", feeComponent(), time.Now().Format("2006-01-02"), "TEST DATA ONLY: a fee changed today", band{from: 0, flat: 2500})
	fixture(t, err, "today's new fee series")
	if r := w.authorise(t, w.s2, v.RequestId, stepUp(t, w.s2, "withdrawal_authorisation")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the hold: %d %s", r.status, r.body)
	}
	got := memberRequestCharges(t, w, v.RequestId)
	if before.Charges.TotalKobo != "1075" || got == nil || *got != "2688" {
		t.Fatalf("WRONG CHARGE — quoted %s, the member's request shows %v; want the hold's 2688 (the series in force when authorised)", before.Charges.TotalKobo, got)
	}
	if now := w.quoteVia(t, w.borrower, w.bMem, payKobo); now.Charges.TotalKobo != "2688" {
		t.Fatalf("STALE — a quote today reads %s; want today's series, 2688", now.Charges.TotalKobo)
	}
}

// memberRequestCharges: the authorised charge's total on the member's own
// request, as the member's list shows it; nil when none is shown.
func memberRequestCharges(t *testing.T, w *mapi, req string) *string {
	t.Helper()
	var l struct {
		Items []struct {
			RequestId         string
			AuthorisedCharges *mCharges
		}
	}
	r := w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil)
	if r.status != http.StatusOK {
		t.Fatalf("FIXTURE: the member's list: %d %s", r.status, r.body)
	}
	fixture(t, json.Unmarshal(r.body, &l), "the member's list")
	for _, it := range l.Items {
		if it.RequestId == req {
			if it.AuthorisedCharges == nil {
				return nil
			}
			return &it.AuthorisedCharges.TotalKobo
		}
	}
	t.Fatalf("FIXTURE: request %s not in the member's list: %s", req, r.body)
	return nil
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
