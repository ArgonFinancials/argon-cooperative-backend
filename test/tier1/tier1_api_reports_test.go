//go:build tier1

// ACCEPTANCE TESTS — THE REPORTS (stage 2, 2026-11-25). The income statement,
// the cash flow by the direct method, and the period report LEDGER_CONTRACT
// §4 names: every one by VALUE DATE, for whoever reads the books. Each test's
// figures come from what the fixture did (a write-off, a recovery, a hold's
// fixed charges, a contribution's amount), never from the report's own query.
package tier1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

type isOut struct {
	Income, Expenses                                []struct{ Code, Name, AmountKobo string }
	TotalIncomeKobo, TotalExpensesKobo, SurplusKobo string
}

type cfOut struct {
	OpeningCashKobo, TotalInKobo, TotalOutKobo, ClosingCashKobo string
	Inflows, Outflows                                           []struct {
		Kind       string
		Entries    int
		AmountKobo string
	}
}

type prOut struct {
	ContributionsByBranch                        []struct{ BranchCode, AmountKobo string }
	TotalContributionsKobo, LoansOutstandingKobo string
	Charges                                      struct{ PartnerKobo, MarginKobo, VatKobo, LevyKobo, TotalKobo string }
}

func decodedReport[T any](t *testing.T, r resp, what string) T {
	t.Helper()
	var v T
	if r.status != http.StatusOK || json.Unmarshal(r.body, &v) != nil {
		t.Fatalf("NOT READ — %s: %d %s", what, r.status, r.body)
	}
	return v
}

func ks(v int64) string { return strconv.FormatInt(v, 10) }

func (w *lreads) report(t *testing.T, name, from, to string) resp {
	return w.get(t, w.s1, "/societies/"+w.c.s.id+"/reports/"+name+"?from="+from+"&to="+to)
}

func lineOf(lines []struct{ Code, Name, AmountKobo string }, code string) (string, bool) {
	for _, l := range lines {
		if l.Code == code {
			return l.AmountKobo, true
		}
	}
	return "", false
}

// The write-off (31 December 2026) is December's expense; the ₦500 recovered
// (10 January 2027) is January's income. Each account shows in each period,
// at zero where nothing moved it.
func TestApiReports01_TheIncomeStatementIsThePeriodsByValueDate(t *testing.T) {
	w, _ := newWrittenOffRead(t)
	dec := decodedReport[isOut](t, w.report(t, "income-statement", "2026-12-01", "2026-12-31"), "December's income statement")
	if loss, ok := lineOf(dec.Expenses, "5901"); !ok || loss != ks(lrPrincipal) {
		t.Fatalf("WRONG EXPENSE — December's loan losses read %q (shown: %v); want %d, the write-off", loss, ok, lrPrincipal)
	}
	if rec, ok := lineOf(dec.Income, "4901"); !ok || rec != "0" {
		t.Fatalf("MISSING ROW — December's loan recoveries read %q (shown: %v); want 0, shown (an account left out reads as one at zero, and only one of those is right)", rec, ok)
	}
	if dec.SurplusKobo != ks(-lrPrincipal) {
		t.Fatalf("WRONG SURPLUS — December reads %s; want %d (a deficit)", dec.SurplusKobo, -lrPrincipal)
	}
	jan := decodedReport[isOut](t, w.report(t, "income-statement", "2027-01-01", "2027-01-31"), "January's income statement")
	if rec, _ := lineOf(jan.Income, "4901"); rec != "50000" || jan.SurplusKobo != "50000" || jan.TotalExpensesKobo != "0" {
		t.Fatalf("WRONG PERIOD — January reads income %q, expenses %s, surplus %s; want the ₦500 recovered and nothing else", rec, jan.TotalExpensesKobo, jan.SurplusKobo)
	}
	wantProblem(t, w.report(t, "income-statement", "2027-01-31", "2027-01-01"), http.StatusBadRequest, "bad_period", "a period that ends before it starts")
}

// Money in and out by kind; the period's first day is in the period, never in
// the opening figure; opening plus in less out is closing.
func TestApiReports02_TheCashFlowIsMoneyInAndOutByKind(t *testing.T) {
	w, _ := newWrittenOffRead(t)
	var before int64
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id
		JOIN accounts a ON a.id = l.account_id WHERE a.society_id = $1 AND a.role = 'bank' AND (e.value_date IS NULL OR e.value_date < '2027-01-10')`,
		w.c.s.id).Scan(&before), "the cash before 10 January")
	cf := decodedReport[cfOut](t, w.report(t, "cash-flow", "2027-01-10", "2027-01-31"), "the cash flow from the recovery's day")
	if cf.OpeningCashKobo != ks(before) || cf.ClosingCashKobo != ks(before+50000) || cf.TotalOutKobo != "0" ||
		len(cf.Inflows) != 1 || cf.Inflows[0].Kind != "loan_recovery" || cf.Inflows[0].Entries != 1 || cf.Inflows[0].AmountKobo != "50000" {
		t.Fatalf("WRONG CASH FLOW — %+v; want opening %d (the recovery's own day is in the period), one recovery of 50000 in, closing %d", cf, before, before+50000)
	}
	all := decodedReport[cfOut](t, w.report(t, "cash-flow", "2000-01-01", "2100-01-01"), "the cash flow of every dated entry")
	disbursed := false
	for _, o := range all.Outflows {
		disbursed = disbursed || o.Kind == "loan_disbursement"
	}
	for _, i := range all.Inflows {
		if i.Kind == "loan_disbursement" {
			t.Fatalf("WRONG DIRECTION — a loan disbursement reads as money in: %+v", all.Inflows)
		}
	}
	if !disbursed {
		t.Fatalf("MISSING — the loans paid out are not money out: %+v", all.Outflows)
	}
}

// Loans outstanding at the END of the period: the write-off of 31 December
// lowers them by its amount between the 30th and the 31st.
func TestApiReports03_LoansOutstandingAreAtThePeriodsEnd(t *testing.T) {
	w, _ := newWrittenOffRead(t)
	d30 := decodedReport[prOut](t, w.report(t, "period", "2026-12-01", "2026-12-30"), "the period to the 30th")
	d31 := decodedReport[prOut](t, w.report(t, "period", "2026-12-01", "2026-12-31"), "the period to the 31st")
	a, _ := strconv.ParseInt(d30.LoansOutstandingKobo, 10, 64)
	b, _ := strconv.ParseInt(d31.LoansOutstandingKobo, 10, 64)
	if a-b != lrPrincipal {
		t.Fatalf("WRONG DATE — loans outstanding read %d to the 30th and %d to the 31st; want them %d apart (the write-off on the 31st)", a, b, lrPrincipal)
	}
}

// Contributions by branch, and charges split as the hold fixed them.
func TestApiReports04_ContributionsByBranchAndChargesSplitAsFixed(t *testing.T) {
	w := newMoneyAPI(t)
	get := func(from, to string) prOut {
		return decodedReport[prOut](t, w.call(t, http.MethodGet, w.s1, "/societies/"+w.c.s.id+"/reports/period?from="+from+"&to="+to, nil), "the period report")
	}
	before := get("2026-09-01", "2026-09-01")
	w.saved(t, w.bMem, 120000) // value date 2026-09-01
	after := get("2026-09-01", "2026-09-01")
	x, _ := strconv.ParseInt(before.TotalContributionsKobo, 10, 64)
	y, _ := strconv.ParseInt(after.TotalContributionsKobo, 10, 64)
	if y-x != 120000 || len(after.ContributionsByBranch) == 0 {
		t.Fatalf("WRONG CONTRIBUTIONS — %d before, %d after a ₦1,200 contribution (branches %v); want 120000 more", x, y, after.ContributionsByBranch)
	}
	_, hold := w.heldVia(t, payKobo)
	decoded[mMovement](t, w.payOut(t, w.poster, payoutCall{hold: hold, member: w.bMem, ref: w.ref(), kobo: payKobo, proof: stepUp(t, w.poster, "withdrawal_payout")}),
		http.StatusCreated, "the payout")
	var fee, vat, levy, margin int64
	fixture(t, db.QueryRow(bg(), `SELECT partner_fee_kobo, coalesce(vat_kobo, 0), statutory_levy_kobo, argon_margin_kobo FROM withdrawal_holds WHERE id = $1`, hold).
		Scan(&fee, &vat, &levy, &margin), "the charges the hold fixed")
	r := get("2000-01-01", "2100-01-01")
	if r.Charges.PartnerKobo != ks(fee) || r.Charges.MarginKobo != ks(margin) || r.Charges.VatKobo != ks(vat) || r.Charges.LevyKobo != ks(levy) ||
		r.Charges.TotalKobo != ks(fee+vat+levy+margin) {
		t.Fatalf("WRONG CHARGES — %+v; want partner %d, margin %d, VAT %d, levy %d, as the hold fixed them (LEDGER_CONTRACT §6: kept apart)", r.Charges, fee, margin, vat, levy)
	}
}

func TestApiReports05_OnlyTheBooksReadersReadThem(t *testing.T) {
	w := newMoneyAPI(t)
	for _, name := range []string{"income-statement", "cash-flow", "period"} {
		for _, as := range []string{w.abroad, userOf(t, w.stranger)} {
			if r := w.call(t, http.MethodGet, as, "/societies/"+w.c.s.id+"/reports/"+name+"?from=2026-01-01&to=2026-12-31", nil); r.status != http.StatusNotFound {
				t.Fatalf("SHOWN — someone outside the society read its %s: %d %s; want 404", name, r.status, r.body)
			}
		}
	}
}
