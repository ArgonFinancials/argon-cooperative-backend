//go:build tier1

// Acceptance tests for the loan READ side (LOAN_SHAPE.md §1, §5, §6) —
// written before the handlers, for review. Every TestApiLoans* name is here.
//
//	GET /loans/{loanId}?asOf=            one loan's position
//	GET /members/{memberId}/loans?asOf=  a member's loans (the member, or an officer)
//	GET /societies/{id}/loans?asOf=&condition=   the loan book (officers only)
//
// Decided 2026-10-05/06:
//   - What is OWED comes from the LEDGER (the loan's own lines on
//     member_loan, by VALUE DATE, as at asOf). Nothing is stored on the loan.
//   - Repayments are allocated to the OLDEST instalment first.
//   - The reason is the server's sentence, pinned here, because a client
//     deriving "2 missed" would be a second answer.
//   - DISAGREEMENT is a condition like the others, not an error: the schedule
//     and the ledger do not agree, it needs a person, it goes to the officers'
//     list with its reason, and the loan is never edited to agree.
//   - asOf is required: the server never assumes "today".
//
// Which handler runs: default api.NewServer (internal/api); ARGON_TEST_API_FAKE
// =<break> runs the mutation vehicle in apifake_loans_test.go.
package tier1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---- the fixture -----------------------------------------------------------------
//
// One borrower (₦10,000 savings), eight loans of ₦3,000 over three months
// (₦1,000 due 5 November 2026, 5 December 2026, 5 January 2027), each
// approved by both signatories with both guarantors in place, and each but L3
// disbursed on 5 October 2026. Every entry is POSTED today; value dates say
// when it happened.
//
//   L1 behind      repaid ₦1,500 on 20 Nov, and ₦500 on 10 Jan 2027
//   L2 on time     repaid ₦1,000 on 5 Nov
//   L3 agreed      approved, never disbursed
//   L4 settled     repaid ₦1,000 on each due date
//   L5 early       repaid ₦3,000 on 5 Nov (the product allows prepayment)
//   L6 strict      repaid ₦3,000 on 5 Nov on a product that does NOT allow it
//   L7 underpaid   repaid ₦1,000 on 5 Nov and 5 Dec only
//   L8 overpaid    repaid ₦3,000 on 5 Nov and ₦500 on 6 Nov

const lrPrincipal = int64(300000)

type lreads struct {
	*lworld
	srv                            interface{ Close() }
	url                            string
	auditor, former, abroad        string
	strict                         string
	L1, L2, L3, L4, L5, L6, L7, L8 string
}

func newLoanReads(t *testing.T) *lreads {
	t.Helper()
	w := &lreads{lworld: newLoanWorld(t)}
	srv := apiServer(t)
	w.srv, w.url = srv, srv.URL
	s := w.c.s
	w.auditor = s.user("Internal auditor")
	fixture(t, s.appoint(s.member(w.auditor), "internal_auditor", false, "2020-01-01", ""), "auditor")
	w.former = s.user("Former secretary")
	fixture(t, s.appoint(s.member(w.former), "secretary", false, "2020-01-01", "2020-12-31"), "ended term")
	elsewhere := newChart(t).s
	w.abroad = elsewhere.user("Chairman elsewhere")
	fixture(t, elsewhere.appoint(elsewhere.member(w.abroad), "chairman", true, "2020-01-01", ""), "officer of another society")
	fixture(t, db.QueryRow(bg(), `INSERT INTO loan_products (society_id, code, name, max_term_months, guarantors_required,
		savings_multiple_pct, interest_rate_bp, allows_prepayment) VALUES ($1, 'strict', 'Strict loan', 12, 2, 300, 0, false)
		RETURNING id`, s.id).Scan(&w.strict), "a product without prepayment")

	pay := func(loan, on string, kobo int64) {
		_, err := w.moveOn(on, "loan_repayment", loan, "", line{acct: w.c.bank, kobo: kobo}, line{acct: w.c.loan, kobo: -kobo, member: w.bMem})
		fixture(t, err, "a repayment on "+on)
	}
	w.L1 = w.approvedLoan(t, w.product, true)
	pay(w.L1, "2026-11-20", 150000)
	pay(w.L1, "2027-01-10", 50000)
	w.L2 = w.approvedLoan(t, w.product, true)
	pay(w.L2, "2026-11-05", 100000)
	w.L3 = w.approvedLoan(t, w.product, false)
	w.L4 = w.approvedLoan(t, w.product, true)
	pay(w.L4, "2026-11-05", 100000)
	pay(w.L4, "2026-12-05", 100000)
	pay(w.L4, "2027-01-05", 100000)
	w.L5 = w.approvedLoan(t, w.product, true)
	pay(w.L5, "2026-11-05", 300000)
	w.L6 = w.approvedLoan(t, w.strict, true)
	pay(w.L6, "2026-11-05", 300000)
	w.L7 = w.approvedLoan(t, w.product, true)
	pay(w.L7, "2026-11-05", 100000)
	pay(w.L7, "2026-12-05", 100000)
	w.L8 = w.approvedLoan(t, w.product, true)
	pay(w.L8, "2026-11-05", 300000)
	pay(w.L8, "2026-11-06", 50000)
	return w
}

// approvedLoan: agreed (₦3,000, 3 months), both guarantees, the request
// approved by both signatories; disbursed on 5 October 2026 if `disburse`.
func (w *lreads) approvedLoan(t *testing.T, product string, disburse bool) string {
	t.Helper()
	loan, err := w.agree(loanTerms{product: product, principal: lrPrincipal, term: 3})
	fixture(t, err, "an agreed loan")
	w.guaranteed(t, loan, w.g1, w.g1Mem)
	w.guaranteed(t, loan, w.g2, w.g2Mem)
	req, err := w.requestFor(loan, "loan_disbursement", w.bMem, lrPrincipal, `now() + interval '7 days'`)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "first signature")
	fixture(t, w.c.s.sign(req, w.s2Mem, "chairman"), "second signature")
	if disburse {
		_, err = w.moveOn("2026-10-05", "loan_disbursement", loan, "",
			line{acct: w.c.loan, kobo: lrPrincipal, member: w.bMem}, line{acct: w.c.bank, kobo: -lrPrincipal})
		fixture(t, err, "the disbursement")
	}
	return loan
}

func (w *lreads) get(t *testing.T, as, path string) resp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, w.url+path, nil)
	req.Header.Set("Authorization", "Bearer "+signIn(t, as))
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "GET "+path)
	defer r.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, e := r.Body.Read(buf)
		b.Write(buf[:n])
		if e != nil {
			break
		}
	}
	return resp{r.StatusCode, []byte(b.String())}
}

// ---- the wire shape, decoded loosely ----------------------------------------------

type lInst struct {
	N      int    `json:"n"`
	Amount string `json:"amountKobo"`
	Paid   string `json:"paidKobo"`
	State  string `json:"state"`
}
type lPos struct {
	LoanId       string  `json:"loanId"`
	AgreedAt     string  `json:"agreedAt"`
	Approval     *string `json:"approvalStatus"`
	DisbursedOn  *string `json:"disbursedOn"`
	Outstanding  string  `json:"outstandingKobo"`
	Due          string  `json:"dueToDateKobo"`
	Paid         string  `json:"paidToDateKobo"`
	Arrears      string  `json:"arrearsKobo"`
	Missed       int     `json:"missedInstalments"`
	Days         int     `json:"daysInArrears"`
	Condition    string  `json:"condition"`
	Disagreement *struct {
		Kind string `json:"kind"`
	} `json:"disagreement"`
	Reason      string  `json:"reason"`
	Instalments []lInst `json:"instalments"`
}
type lBook struct {
	Loans []lPos `json:"loans"`
}

func (w *lreads) position(t *testing.T, loan, asOf string) lPos {
	t.Helper()
	return ok200[lPos](t, w.get(t, w.s1, "/loans/"+loan+"?asOf="+asOf))
}

func wantCondition(t *testing.T, p lPos, cond, kind, reason string) {
	t.Helper()
	gotKind := ""
	if p.Disagreement != nil {
		gotKind = p.Disagreement.Kind
	}
	if p.Condition != cond || gotKind != kind {
		t.Fatalf("ACCEPTED — condition %q (disagreement %q), want %q (%q). Reason given: %q", p.Condition, gotKind, cond, kind, p.Reason)
	}
	if reason != "" && p.Reason != reason {
		t.Fatalf("ACCEPTED — the reason reads:\n  %q\nwant exactly:\n  %q", p.Reason, reason)
	}
}

// =============================================================================
// WHO MAY READ
// =============================================================================

func TestApiLoans1_TheBorrowerReadsTheirLoan(t *testing.T) {
	w := newLoanReads(t)
	if r := w.get(t, w.borrower, "/loans/"+w.L1+"?asOf=2026-12-31"); r.status != http.StatusOK {
		t.Fatalf("REFUSED — the borrower reading their own loan: %d %s", r.status, r.body)
	}
}

func TestApiLoans2_AnOfficerAndTheAuditorRead(t *testing.T) {
	w := newLoanReads(t)
	for who, u := range map[string]string{"the treasurer": w.s1, "the internal auditor": w.auditor} {
		if r := w.get(t, u, "/loans/"+w.L1+"?asOf=2026-12-31"); r.status != http.StatusOK {
			t.Fatalf("REFUSED — %s reading a loan of their society: %d %s", who, r.status, r.body)
		}
	}
}

// Refusals are byte-identical to the thing not existing.
func (w *lreads) wantNotFoundLike(t *testing.T, as, who, path, nonePath string) {
	t.Helper()
	mine, none := w.get(t, as, path), w.get(t, as, nonePath)
	if mine.status != http.StatusNotFound {
		t.Fatalf("ACCEPTED — %s read %s: %d %s", who, path, mine.status, mine.body)
	}
	if string(mine.body) != string(none.body) || none.status != http.StatusNotFound {
		t.Fatalf("ACCEPTED — the refusal tells %s it exists:\n  it:      %s\n  nothing: %d %s", who, mine.body, none.status, none.body)
	}
}

func TestApiLoans3_AnotherMemberGetsNotFound(t *testing.T) {
	w := newLoanReads(t)
	w.wantNotFoundLike(t, w.g1, "a guarantor (a member with no post)", "/loans/"+w.L1+"?asOf=2026-12-31", "/loans/"+noSuchRow+"?asOf=2026-12-31")
}

func TestApiLoans4_AFormerOfficerGetsNotFound(t *testing.T) {
	w := newLoanReads(t)
	w.wantNotFoundLike(t, w.former, "an officer whose term ended", "/loans/"+w.L1+"?asOf=2026-12-31", "/loans/"+noSuchRow+"?asOf=2026-12-31")
}

func TestApiLoans5_AnOfficerOfAnotherSocietyGetsNotFound(t *testing.T) {
	w := newLoanReads(t)
	w.wantNotFoundLike(t, w.abroad, "an officer of another society", "/loans/"+w.L1+"?asOf=2026-12-31", "/loans/"+noSuchRow+"?asOf=2026-12-31")
}

func TestApiLoans6_TheLoanBookIsForOfficersOnly(t *testing.T) {
	w := newLoanReads(t)
	w.wantNotFoundLike(t, w.g1, "a member with no post", "/societies/"+w.c.s.id+"/loans?asOf=2026-12-31", "/societies/"+noSuchRow+"/loans?asOf=2026-12-31")
}

func TestApiLoans7_AMembersLoansAreTheirsToRead(t *testing.T) {
	w := newLoanReads(t)
	w.wantNotFoundLike(t, w.g1, "another member", "/members/"+w.bMem+"/loans?asOf=2026-12-31", "/members/"+noSuchRow+"/loans?asOf=2026-12-31")
}

// =============================================================================
// THE FIGURES: from the ledger, by value date, oldest instalment first
// =============================================================================

// L1 as at 31 December: ₦3,000 lent, ₦1,500 repaid. The schedule says ₦2,000
// was due; the ledger says ₦1,500 is owed. The ledger is the authority.
func TestApiLoans8_OutstandingIsFromTheLedger(t *testing.T) {
	w := newLoanReads(t)
	if p := w.position(t, w.L1, "2026-12-31"); p.Outstanding != "150000" {
		t.Fatalf("ACCEPTED — outstanding %s, want 150000 (₦3,000 lent less ₦1,500 repaid, from the ledger; not the schedule's ₦1,000)", p.Outstanding)
	}
}

// The ₦500 dated 10 January 2027 was POSTED today; as at 31 December it had
// not happened.
func TestApiLoans9_AsOfIsAValueDate(t *testing.T) {
	w := newLoanReads(t)
	if p := w.position(t, w.L1, "2026-12-31"); p.Paid != "150000" {
		t.Fatalf("ACCEPTED — paid to date %s as at 31 December, want 150000 (the 10 January repayment is after it)", p.Paid)
	}
}

func TestApiLoans10_RepaymentsAllocateOldestFirst(t *testing.T) {
	w := newLoanReads(t)
	got := []string{}
	for _, in := range w.position(t, w.L1, "2026-12-31").Instalments {
		got = append(got, in.State+":"+in.Paid)
	}
	want := "paid:100000 missed:50000 due:0"
	if strings.Join(got, " ") != want {
		t.Fatalf("ACCEPTED — instalments %v, want %s (₦1,500 covers the November instalment, then half of December's)", got, want)
	}
}

func TestApiLoans11_MissedInstalmentsAndDaysInArrears(t *testing.T) {
	w := newLoanReads(t)
	p := w.position(t, w.L1, "2026-12-31")
	if p.Missed != 1 || p.Days != 26 || p.Arrears != "50000" || p.Due != "200000" {
		t.Fatalf("ACCEPTED — missed %d, days %d, arrears %s, due %s; want 1, 26 (5 to 31 December), 50000, 200000 (December's instalment is half paid, so missed)",
			p.Missed, p.Days, p.Arrears, p.Due)
	}
}

func TestApiLoans12_TheArrearsReasonIsInWords(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L1, "2026-12-31"), "in_arrears", "",
		"1 instalment missed: ₦1,000.00 due 5 December 2026, ₦500.00 paid.")
}

func TestApiLoans13_AsOfIsRequired(t *testing.T) {
	w := newLoanReads(t)
	r := w.get(t, w.s1, "/loans/"+w.L1)
	var p struct{ Code string }
	if r.status != http.StatusBadRequest || json.Unmarshal(r.body, &p) != nil || p.Code == "" {
		t.Fatalf("ACCEPTED — a loan position with no asOf: %d %s; want 400 with a JSON problem", r.status, r.body)
	}
}

// =============================================================================
// THE CONDITIONS, each with its sentence
// =============================================================================

func TestApiLoans14_NotYetDisbursedReadsAgreed(t *testing.T) {
	w := newLoanReads(t)
	p := w.position(t, w.L3, "2026-12-31")
	at, err := time.Parse(time.RFC3339Nano, p.AgreedAt)
	fixture(t, err, "agreedAt")
	wantCondition(t, p, "agreed", "", "Agreed on "+at.Format("2 January 2006")+"; not yet disbursed. The disbursement request is approved.")
}

func TestApiLoans15_OnScheduleReadsActive(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L2, "2026-11-30"), "active", "", "On schedule. Next instalment: ₦1,000.00 due 5 December 2026.")
}

func TestApiLoans16_ScheduleEndedAndNothingOwedReadsSettled(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L4, "2027-02-01"), "settled", "", "Settled: the schedule is complete and nothing is owed.")
}

func TestApiLoans17_RepaidEarlyOnAProductThatAllowsItReadsSettledEarly(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L5, "2026-11-30"), "settled_early", "", "Settled early: nothing is owed and 2 instalments were not yet due.")
}

func TestApiLoans18_RepaidEarlyAgainstTheTermsIsADisagreement(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L6, "2026-11-30"), "disagreement", "prepaid_against_terms",
		"Disagreement: nothing is owed, but Strict loan does not allow early repayment and 2 instalments were not yet due. An officer must establish how it was repaid.")
}

// Decided 2026-10-06: the schedule complete and the ledger not at zero is a
// FACT that needs a person, stated as plainly as the rest.
func TestApiLoans19_ScheduleEndedWithMoneyOwedIsADisagreement(t *testing.T) {
	w := newLoanReads(t)
	wantCondition(t, w.position(t, w.L7, "2027-02-01"), "disagreement", "underpaid",
		"Disagreement: the schedule ended on 5 January 2027, but the ledger shows ₦1,000.00 still owed. An officer must establish why; the loan is never edited to agree.")
}

// And the loan is never edited to agree: the agreement stands as made.
func TestApiLoans20_OverpaidIsADisagreementNotAZero(t *testing.T) {
	w := newLoanReads(t)
	p := w.position(t, w.L8, "2027-02-01")
	if p.Outstanding != "-50000" {
		t.Fatalf("ACCEPTED — outstanding %s, want -50000 (₦500 owed to the member, never clamped to zero)", p.Outstanding)
	}
	wantCondition(t, p, "disagreement", "overpaid",
		"Disagreement: the ledger shows ₦500.00 repaid beyond the loan, owed to the member. An officer must refund or correct it; the loan is never edited to agree.")
	var principal int64
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT l.principal_kobo, (SELECT count(*) FROM loan_instalments WHERE loan_id = l.id) FROM loans l WHERE l.id = $1`,
		w.L8).Scan(&principal, &n), "the agreement")
	if principal != lrPrincipal || n != 3 {
		t.Fatalf("ACCEPTED — the agreement was edited to agree with the ledger: principal %d, %d instalments", principal, n)
	}
}

// The officers' list: every loan whose schedule and ledger disagree, each with
// its reason. As at 1 February 2027 the schedules have all ended: L1 and L2
// were never paid up, L7 short, L8 over.
func TestApiLoans21_OfficersListTheDisagreements(t *testing.T) {
	w := newLoanReads(t)
	b := ok200[lBook](t, w.get(t, w.s1, "/societies/"+w.c.s.id+"/loans?asOf=2027-02-01&condition=disagreement"))
	got := []string{}
	for _, p := range b.Loans {
		got = append(got, p.LoanId)
		if !strings.HasPrefix(p.Reason, "Disagreement: ") {
			t.Fatalf("ACCEPTED — a listed disagreement without its reason: %q", p.Reason)
		}
	}
	want := []string{w.L1, w.L2, w.L7, w.L8}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ACCEPTED — the disagreements listed are %v, want L1, L2, L7, L8 in order of agreement (%v)", got, want)
	}
}

// =============================================================================
// THE WIRE
// =============================================================================

func TestApiLoans22_MoneyIsAStringOnTheWire(t *testing.T) {
	w := newLoanReads(t)
	r := w.get(t, w.s1, "/loans/"+w.L1+"?asOf=2026-12-31")
	ms := koboField.FindAllSubmatch(r.body, -1)
	if len(ms) == 0 {
		t.Fatalf("WRONG SHAPE — no *Kobo field: %s", r.body)
	}
	for _, m := range ms {
		if m[2][0] != '"' {
			t.Fatalf("ACCEPTED — %s sent as a JSON number (%s): a float on the wire", m[1], m[2])
		}
	}
}

func TestApiLoans23_EmptyListsAreArraysNotNull(t *testing.T) {
	w := newLoanReads(t)
	r := w.get(t, w.s1, "/members/"+w.g2Mem+"/loans?asOf=2026-12-31")
	if !strings.Contains(string(r.body), `"loans":[]`) {
		t.Fatalf("ACCEPTED — a member with no loans: %s; want \"loans\":[]", r.body)
	}
}
