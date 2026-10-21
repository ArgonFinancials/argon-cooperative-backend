//go:build tier1

// Suite 0017 — A REQUEST KEEPS THE CHARGE IT WAS QUOTED; THE LARGEST AMOUNT A
// MEMBER MAY REQUEST, EXACTLY. Written before the migration, for review in
// pieces (decided 2026-10-21).
//
// Finding 49: the quote is indicative and the hold's charge applies, so a
// member quoted ₦10.75 can have ₦26.88 taken if the series changes between,
// and nothing said so. Recomputing "what was quoted" from the series in force
// on the raise date fails exactly when a series takes effect the same day,
// after the request. So the request KEEPS its quote:
//
//   - A withdrawal request records the charge quoted when it is raised (the
//     series in force then), server-set as the expiry is, whatever the client
//     sends. A request of any other kind carries none.
//
// The withdrawable figure a member is shown is the LARGEST AMOUNT THEY MAY
// REQUEST, not the pool the charge comes out of (decided 2026-10-21: "you can
// withdraw ₦9,000" is read as "ask for ₦9,000 and get it"):
//
//   - largest_withdrawal(society, kind, pool, date): the largest amount whose
//     amount plus charge fits the pool, EXACTLY, at a band boundary above all
//     (an approximate method is wrong there by one band), and where a band
//     makes the charge FALL above a threshold.
//   - What it answers is what the database accepts when raised; one kobo more
//     is refused (the read and the write tied, test 05).
package tier1

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type quoted struct {
	fee, vat, levy, margin *int64
	on                     *time.Time
}

func (q quoted) total() int64 {
	var t int64
	for _, p := range []*int64{q.fee, q.vat, q.levy, q.margin} {
		if p != nil {
			t += *p
		}
	}
	return t
}

// String: the figures, readable in a failure (not the pointers).
func (q quoted) String() string {
	f := func(p *int64) string {
		if p == nil {
			return "null"
		}
		return fmt.Sprint(*p)
	}
	on := "null"
	if q.on != nil {
		on = q.on.Format("2006-01-02")
	}
	return fmt.Sprintf("{fee %s, vat %s, levy %s, margin %s, on %s}", f(q.fee), f(q.vat), f(q.levy), f(q.margin), on)
}

func (q quoted) none() bool {
	return q.fee == nil && q.vat == nil && q.levy == nil && q.margin == nil && q.on == nil
}

func quoteOn(t *testing.T, req string) quoted {
	t.Helper()
	var q quoted
	fixture(t, db.QueryRow(bg(), `SELECT quoted_partner_fee_kobo, quoted_vat_kobo, quoted_statutory_levy_kobo, quoted_argon_margin_kobo, quoted_on
		FROM approval_requests WHERE id = $1`, req).Scan(&q.fee, &q.vat, &q.levy, &q.margin, &q.on), "the request's quote")
	return q
}

// clientQuoted: the borrower's own request, its client sending a quote of ₦0.01
// in every component, dated 2020.
func (w *mworld) clientQuoted(t *testing.T) (string, error) {
	t.Helper()
	proof := stepUp(t, w.borrower, "withdrawal_request")
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('withdrawal_request', $1, $2) RETURNING id`,
			w.borrower, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the act: %w", err)
		}
		return x.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at,
			  raised_by_user_id, act_id, quoted_partner_fee_kobo, quoted_vat_kobo, quoted_statutory_levy_kobo, quoted_argon_margin_kobo, quoted_on)
			VALUES ($1, $2, $3, 'withdrawal_out', $4, (SELECT withdrawal_signatures_required FROM societies WHERE id = $1), now() + interval '1 day',
			  $5, $6, 1, 1, 1, 1, '2020-01-01') RETURNING id`,
			w.c.s.id, fmt.Sprintf("WDR-%d", seq.Add(1)), w.bMem, payKobo, w.borrower, act).Scan(&id)
	})
	return id, err
}

// loanRequest: a disbursement request for an agreed, guaranteed loan; with
// sent, its client also sends a quote.
func (w *mworld) loanRequest(t *testing.T, sent bool) (string, error) {
	t.Helper()
	loan := w.loan(t)
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	cols, vals := "", ""
	if sent {
		cols, vals = ", quoted_partner_fee_kobo, quoted_vat_kobo, quoted_statutory_levy_kobo, quoted_argon_margin_kobo, quoted_on", ", 1, 1, 1, 1, '2020-01-01'"
	}
	var id string
	err := db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, loan_id`+cols+`)
		VALUES ($1, $2, $3, 'loan_disbursement', $4, 2, now() + interval '7 days', $5`+vals+`) RETURNING id`,
		w.c.s.id, fmt.Sprintf("LN-%d", seq.Add(1)), w.bMem, loanPrincipal, loan).Scan(&id)
	return id, err
}

func largest(t *testing.T, society string, pool int64) int64 {
	t.Helper()
	var n *int64
	fixture(t, db.QueryRow(bg(), `SELECT largest_withdrawal($1, 'withdrawal_out', $2, current_date)`, society, pool).Scan(&n), "largest_withdrawal")
	if n == nil {
		t.Fatalf("UNANSWERED — largest_withdrawal(%d) is NULL", pool)
	}
	return *n
}

// =============================================================================
// THE QUOTE, KEPT
// =============================================================================

// ₦6,000: the ₦10 fee band, VAT 7.5% of it, no levy: ₦10.75, quoted today.
func TestQuote01_ARequestKeepsTheChargeItWasQuoted(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.raiseBy(t, raiseSpec{member: w.bMem})
	fixture(t, err, "the borrower's request")
	q := quoteOn(t, req)
	if q.total() != 1075 || q.on == nil || q.on.Format("2006-01-02") != time.Now().Format("2006-01-02") {
		t.Fatalf("UNRECORDED — the request keeps %v (total %d); want the ₦10.75 quoted today", q, q.total())
	}
}

func TestQuote02_AClientsQuoteIsOverwritten(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.clientQuoted(t)
	fixture(t, err, "a request whose client sent its own quote")
	if q := quoteOn(t, req); q.total() != 1075 || q.on == nil || q.on.Year() == 2020 {
		t.Fatalf("ACCEPTED — the client chose the quote it will be held to: %v (total %d); want the server's ₦10.75, today", q, q.total())
	}
}

func TestQuote03_AnotherKindCarriesNoQuoteEvenIfSent(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.loanRequest(t, true)
	fixture(t, err, "a loan request whose client sent a quote")
	if q := quoteOn(t, req); !q.none() {
		t.Fatalf("ACCEPTED — a loan disbursement keeps a withdrawal's quote the client sent: %v", q)
	}
}

// =============================================================================
// THE LARGEST AMOUNT A MEMBER MAY REQUEST
// =============================================================================
//
// The illustrative series: Argon's fee ₦0 below ₦5,000, ₦10 to ₦50,000; VAT
// 7.5% of it; the transfer levy ₦50 from ₦10,000.

// ₦10,030 may leave. ₦9,999.99 costs ₦10.75 (₦10,010.74 out): fits.
// ₦10,000.00 costs ₦60.75 (₦10,060.75 out): does not. The pool less the charge
// on the whole pool (₦10,030 − ₦60.75 = ₦9,969.25) is wrong by one band.
func TestLargest01_ExactOneKoboBelowTheLevy(t *testing.T) {
	w := newMoneyWorld(t)
	if got := largest(t, w.c.s.id, 1003000); got != 999999 {
		t.Fatalf("INEXACT — the largest request from ₦10,030 is %d kobo; want 999999 (₦9,999.99), one kobo below the levy", got)
	}
}

// ₦5,010 may leave. ₦4,999.99 costs nothing: fits. ₦5,000.00 costs ₦10.75
// (₦5,010.75 out): does not.
func TestLargest02_ExactOneKoboBelowTheFee(t *testing.T) {
	w := newMoneyWorld(t)
	if got := largest(t, w.c.s.id, 501000); got != 499999 {
		t.Fatalf("INEXACT — the largest request from ₦5,010 is %d kobo; want 499999 (₦4,999.99), one kobo below the fee", got)
	}
}

// ₦9,000 may leave: ₦8,989.25 and its ₦10.75 fit EXACTLY (an exact fit fits).
func TestLargest03_WithinOneBand(t *testing.T) {
	w := newMoneyWorld(t)
	if got := largest(t, w.c.s.id, 900000); got != 898925 {
		t.Fatalf("INEXACT — the largest request from ₦9,000 is %d kobo; want 898925 (₦8,989.25), its ₦10.75 fitting exactly", got)
	}
}

// From today this society's fee is ₦50 below ₦10,000 and NOTHING from ₦10,000
// (TEST DATA ONLY: a fee waived above a threshold). ₦10,050 may leave:
// ₦10,000.00 costs only the ₦50 levy (₦10,050 out): fits, though ₦9,996.26
// and everything up to ₦9,999.99 do not. One bisection over the whole range
// stops at ₦9,996.25.
func TestLargest04_WhereTheChargeFallsAboveAThreshold(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", feeComponent(), time.Now().Format("2006-01-02"),
		"TEST DATA ONLY: a fee waived from ₦10,000", band{from: 0, to: 999999, flat: 5000}, band{from: 1000000, flat: 0})
	fixture(t, err, "today's fee series, waived from ₦10,000")
	if got := largest(t, w.c.s.id, 1005000); got != 1000000 {
		t.Fatalf("INEXACT — the largest request from ₦10,050 is %d kobo; want 1000000 (₦10,000.00, where the fee falls away)", got)
	}
}

// What it answers is what the database accepts: the borrower's ₦9,000 pool
// (₦10,000 saved, the ₦1,000 floor). The largest is raised; one kobo more is
// refused by 0016's own rule.
func TestLargest05_TheLargestIsAcceptedOneKoboMoreIsNot(t *testing.T) {
	w := newMoneyWorld(t)
	most := largest(t, w.c.s.id, borrowerWithdrawable)
	if _, err := w.raiseBy(t, raiseSpec{member: w.bMem, amount: most}); err != nil {
		t.Fatalf("NOT ENFORCED AS ANSWERED — the largest request, %d kobo, was refused: %v", most, err)
	}
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, amount: most + 1})
	refused(t, err, "one kobo more than the largest", refusedAs("withdrawal_request_within_withdrawable"))
}

// =============================================================================
// CONTROLS
// =============================================================================

func TestQuote_Control_ALoanRequestCarriesNoQuote(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.loanRequest(t, false)
	accepted(t, err, "a loan disbursement request")
	if q := quoteOn(t, req); !q.none() {
		t.Fatalf("OVER-REACHING — a loan disbursement was quoted as a withdrawal: %v", q)
	}
}
