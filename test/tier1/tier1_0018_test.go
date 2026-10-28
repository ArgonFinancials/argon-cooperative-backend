//go:build tier1

// 0018: ONE ARITHMETIC FOR WHAT A MEMBER MAY WITHDRAW (decided 2026-10-28,
// balance handlers piece 1). "Savings, less what guarantees hold, less the
// floor, less payouts authorised and not yet paid" was written twice inline,
// in the raise check (0016) and the hold check (0012/0013), and the read would
// have been a third copy. Now it is one function, member_position, returning
// the PARTS (so the read can say which loan holds the money) and the figure;
// both triggers call it, and so does the API. The figure a member is SHOWN and
// the figure they are REFUSED BY cannot differ, by construction.
//
// The triggers' rewrite is guarded by the suites written before it: 0012's and
// 0016's GREEN run on every migration, 0018 included, so a rewrite that
// changed what either refuses turns them red.
package tier1

import (
	"testing"
)

type pos struct {
	savings, held, open int64
	floor, withdrawable *int64
}

func positionOf(t *testing.T, member string) pos {
	t.Helper()
	var p pos
	var savings, held, open *int64
	fixture(t, db.QueryRow(bg(), `SELECT savings_kobo, held_kobo, floor_kobo, open_holds_kobo, withdrawable_kobo FROM member_position($1)`, member).
		Scan(&savings, &held, &p.floor, &open, &p.withdrawable), "member_position")
	// The parts are always answered (only the figure and the floor may be
	// NULL): a function that answers nothing is red, not a broken test.
	if savings == nil || held == nil || open == nil {
		t.Fatalf("NO ANSWER — member_position gave savings %s, held %s, open %s; every part is always answered", i64(savings), i64(held), i64(open))
	}
	p.savings, p.held, p.open = *savings, *held, *open
	return p
}

type heldRow struct {
	loan, borrower, name string
	owed                 int64
}

func guaranteesHeld(t *testing.T, member string) []heldRow {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT loan_id::text, borrower_member_id::text, borrower_name, owed_kobo FROM member_guarantees_held($1) ORDER BY loan_id`, member)
	fixture(t, err, "member_guarantees_held")
	defer rows.Close()
	var out []heldRow
	for rows.Next() {
		var h heldRow
		fixture(t, rows.Scan(&h.loan, &h.borrower, &h.name, &h.owed), "a guarantee held")
		out = append(out, h)
	}
	return out
}

func i64(p *int64) string {
	if p == nil {
		return "NULL"
	}
	return k(*p)
}

// positionWorld: guarantor one has saved ₦50,000; guarantees the borrower's
// ₦24,000 loan (disbursed, all owed); has a ₦6,000 payout authorised and not
// yet paid; and a ₦5,000 payout authorised AND paid, which must count once, as
// the savings it took, never again as a hold.
func positionWorld(t *testing.T) (*mworld, string) {
	t.Helper()
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, 5000000)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	w.held(t, w.g1Mem, payKobo)
	paidReq, paidHold := w.held(t, w.g1Mem, 500000)
	_, err = w.record(t, payoutRec{req: paidReq, hold: paidHold, member: w.g1Mem, amount: 500000, ch: transferCharges(500000)})
	fixture(t, err, "the paid payout")
	return w, loan
}

// The parts and the figure, each exactly: ₦50,000 less the ₦5,010.75 paid out
// is the savings; ₦24,000 held; ₦1,000 floor; ₦6,010.75 authorised and open.
func TestPosition01_ThePartsAndTheFigure(t *testing.T) {
	w, _ := positionWorld(t)
	p := positionOf(t, w.g1Mem)
	savings, open := int64(5000000-501075), int64(payKobo+1075)
	want := savings - loanPrincipal - floorKobo - open
	if p.savings != savings || p.held != loanPrincipal || p.floor == nil || *p.floor != floorKobo || p.open != open {
		t.Fatalf("WRONG PARTS — savings %s, held %s, floor %s, open %s; want %s, %s, %s, %s (the paid payout counted once, as savings)",
			k(p.savings), k(p.held), i64(p.floor), k(p.open), k(savings), k(loanPrincipal), k(floorKobo), k(open))
	}
	if p.withdrawable == nil || *p.withdrawable != want {
		t.Fatalf("WRONG FIGURE — %s; want %s", i64(p.withdrawable), k(want))
	}
}

// Each guarantee holding money names its loan and borrower: a member shown a
// smaller figure is told which loan holds it, from the same source.
func TestPosition02_EachGuaranteeHeldNamesItsLoanAndBorrower(t *testing.T) {
	w, loan := positionWorld(t)
	got := guaranteesHeld(t, w.g1Mem)
	var name string
	fixture(t, db.QueryRow(bg(), `SELECT u.full_name FROM members m JOIN users u ON u.id = m.user_id WHERE m.id = $1`, w.bMem).Scan(&name), "the borrower's name")
	if len(got) != 1 || got[0].loan != loan || got[0].borrower != w.bMem || got[0].name != name || got[0].owed != loanPrincipal {
		t.Fatalf("WRONG — %+v; want one: loan %s, borrower %s (%s), %s owed", got, loan, w.bMem, name, k(loanPrincipal))
	}
}

// A loan repaid in full holds nothing and is not listed.
func TestPosition03_ARepaidLoanIsNotHeld(t *testing.T) {
	w, loan := positionWorld(t)
	_, err := w.repay(loan, loanPrincipal)
	fixture(t, err, "the loan repaid in full")
	if got := guaranteesHeld(t, w.g1Mem); len(got) != 0 {
		t.Fatalf("LISTED — a repaid loan still appears as held: %+v", got)
	}
	if p := positionOf(t, w.g1Mem); p.held != 0 {
		t.Fatalf("HELD — %s held for a loan repaid in full", k(p.held))
	}
}

// No floor set: the figure is NULL (it cannot be worked out), and the parts
// are still there, the savings among them.
func TestPosition04_NoFloorNoFigureThePartsStill(t *testing.T) {
	w, _ := positionWorld(t)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "a society with no floor")
	p := positionOf(t, w.g1Mem)
	if p.withdrawable != nil || p.floor != nil || p.savings != 5000000-501075 {
		t.Fatalf("WRONG — figure %s, floor %s, savings %s; want NULL, NULL, and the savings shown", i64(p.withdrawable), i64(p.floor), k(p.savings))
	}
}

// Owing more than saved: the figure is 0, never below.
func TestPosition05_NeverBelowZero(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, loanSavings)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	if p := positionOf(t, w.g1Mem); p.withdrawable == nil || *p.withdrawable != 0 {
		t.Fatalf("BELOW ZERO — %s; want 0 (₦24,000 held against ₦10,000 saved)", i64(p.withdrawable))
	}
}
