//go:build tier1

// Acceptance tests for the loan read side's WRITTEN OFF condition — a reviewed
// addition to tier1_api_loans_test.go, written before the handler change.
// Their own suite (loans_api_wo) until approved and built, so the approved
// loan read suite stays green meanwhile; then they merge into it.
//
// A write-off records where the loss went; the debt did not disappear. So the
// loan reads written_off, never settled, with the society's reason in the
// server's sentence; money recovered later is reported, never counted as a
// repayment; and, as everything on the read side, it is as at a VALUE DATE.
package tier1

import (
	"testing"
)

// L9: ₦3,000 over three months, disbursed 5 October 2026, never repaid;
// written off on 31 December 2026 (quorum 2, signed by the treasurer and the
// chairman); ₦500 recovered on 10 January 2027.
func newWrittenOffRead(t *testing.T) (*lreads, string) {
	t.Helper()
	w := &lreads{lworld: newLoanWorld(t)}
	srv := apiServer(t)
	w.srv, w.url = srv, srv.URL
	s := w.c.s
	var loss, recovery string
	fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, '5901', 'Loan losses', 'expense', 'loan_loss') RETURNING id`,
		s.id).Scan(&loss), "loss account")
	fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, '4901', 'Loan recoveries', 'income', 'loan_recovery_income') RETURNING id`,
		s.id).Scan(&recovery), "recovery account")
	l9 := w.approvedLoan(t, w.product, true)
	fixture(t, one(`UPDATE societies SET write_off_signatures_required = 2 WHERE id = $1`, s.id), "quorum")
	var req string
	fixture(t, db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, loan_id, reason)
		VALUES ($1, $2, $3, 'loan_write_off', $4, 2, now() + interval '7 days', $5, 'The borrower died in August; the estate has no assets')
		RETURNING id`, s.id, "WO-"+digest()[:12], w.bMem, lrPrincipal, l9).Scan(&req), "the write-off request")
	fixture(t, s.sign(req, w.s1Mem, "treasurer"), "first write-off signature")
	fixture(t, s.sign(req, w.s2Mem, "chairman"), "second write-off signature")
	_, err := w.moveOn("2026-12-31", "loan_write_off", l9, "", line{acct: loss, kobo: lrPrincipal}, line{acct: w.c.loan, kobo: -lrPrincipal, member: w.bMem})
	fixture(t, err, "the write-off")
	_, err = w.moveOn("2027-01-10", "loan_recovery", l9, "", line{acct: w.c.bank, kobo: 50000}, line{acct: recovery, kobo: -50000})
	fixture(t, err, "₦500 recovered")
	return w, l9
}

// Nothing is owed after a write-off, and it is still not "settled".
func TestApiLoansWrittenOff1_AWrittenOffLoanSaysSoAndWhy(t *testing.T) {
	w, l9 := newWrittenOffRead(t)
	p := w.position(t, l9, "2027-01-05")
	if p.Outstanding != "0" {
		t.Fatalf("ACCEPTED — outstanding %s after the write-off, want 0 (the loss is on the loss account)", p.Outstanding)
	}
	wantCondition(t, p, "written_off", "",
		"Written off on 31 December 2026: ₦3,000.00 was not recovered. The borrower died in August; the estate has no assets.")
}

// Money recovered is reported, not counted as a repayment: the loan neither
// reopens nor reads overpaid.
func TestApiLoansWrittenOff2_ARecoveryIsReportedNotRepaid(t *testing.T) {
	w, l9 := newWrittenOffRead(t)
	p := w.position(t, l9, "2027-02-01")
	if p.Outstanding != "0" || p.Paid != "0" {
		t.Fatalf("ACCEPTED — outstanding %s, paid %s after a recovery; want 0 and 0 (a recovery is not a repayment)", p.Outstanding, p.Paid)
	}
	wantCondition(t, p, "written_off", "",
		"Written off on 31 December 2026: ₦3,000.00 was not recovered. The borrower died in August; the estate has no assets. Since recovered: ₦500.00.")
}

// As at 15 December the write-off (31 December) had not happened: the loan
// was simply behind.
func TestApiLoansWrittenOff3_AWriteOffIsAsAtItsValueDate(t *testing.T) {
	w, l9 := newWrittenOffRead(t)
	wantCondition(t, w.position(t, l9, "2026-12-15"), "in_arrears", "",
		"2 instalments missed: ₦1,000.00 due 5 November 2026, unpaid; ₦1,000.00 due 5 December 2026, unpaid.")
}
