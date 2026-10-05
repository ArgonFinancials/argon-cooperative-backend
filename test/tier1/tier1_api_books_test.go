//go:build tier1

// Acceptance tests for the society's books (LEDGER_CONTRACT §5) — written
// before the handlers, for review. Every TestApiBooks* name is in this suite.
//
//	GET /societies/{id}/accounts                   chart of accounts
//	GET /societies/{id}/trial-balance?asOf=        trial balance
//	GET /societies/{id}/accounts/{code}/ledger     general ledger
//	GET /societies/{id}/journal?reference=&memberId=&actorUserId=&from=&to=
//
// Decided 2026-10-05:
//   - DATE BASIS is the VALUE DATE (what happened in the society's books, as
//     period close uses), not the posting time. A NULL value date (an opening
//     balance "accumulated before migration") is before every date. Rows are
//     ordered by value date (NULL first), then posting time, then entry id.
//   - Who may read: an officer of the society holding a CURRENT post, the
//     read-only internal auditor included. Anyone else, a plain member of the
//     society included, gets 404 byte-identical to a society that does not
//     exist.
//   - Figures are §0-signed (positive = debit). A trial balance row carries
//     turnover (debits >= 0, credits <= 0) AND balance; the totals sum to zero.
//
// NOT TESTED, AND WHY: "if the trial balance does not sum to zero, refuse
// loudly (500 ledger_does_not_balance)". Every entry balances at commit, and
// 0006 keeps every line in its entry's society, so no fixture can build books
// that do not sum to zero. The check stays in the handler (as the 0002 suite
// says of period close).
//
// Which handler runs: default api.NewServer (internal/api, the real one);
// ARGON_TEST_API_FAKE=<break> runs the mutation vehicle in apifake_books_test.go.
package tier1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ---- the fixture: one society's books, told as a short story ------------------
//
// Society S (chart: newChart's seven role accounts, newSociety's 1000 and 2000,
// and a non-postable grouping account 2900). Member A and member B.
//
//   eOpening   opening_balance, NO as-at date (batch R)  3901 +48,000,000 / 2001 −48,000,000 (A)
//   eContrib   contribution_in, value 2026-03-02         1001 +2,500,000 / 2001 −2,491,250 (A) / 2101 −5,000 / 2111 −3,750
//   eReversal  reverses eContrib, value 2026-03-05       the mirror of eContrib (D-02: charges refunded)
//   eLate      payroll_remittance, value 2026-09-01      1001 +200,000 / 2001 −200,000 (B)
//   eBackdated contribution_in, value 2026-01-15, POSTED LAST, by another actor
//                                                        1001 +1,000,000 / 2001 −996,500 (A) / 2101 −2,000 / 2111 −1,500
//
// Every entry is posted today, in that order, so posting order and value-date
// order differ: eBackdated is posted last but happened first.
// Society T (another chart, same codes) has an entry of its own.

const booksAsOf = "2026-06-30" // after eBackdated, eContrib, eReversal; before eLate

type books struct {
	srv                                                      *httptest.Server
	c, t                                                     *chart
	officer, auditor, plain, former, abroad                  string // users
	otherActor                                               string // user who posted eBackdated
	memberA, memberB                                         string // member ids
	openingRef                                               string
	eOpening, eContrib, eReversal, eLate, eBackdated, eOther string
}

func newBooks(t *testing.T) *books {
	t.Helper()
	b := &books{srv: apiServer(t), c: newChart(t), t: newChart(t)}
	s := b.c.s
	fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role, postable)
		VALUES ($1, '2900', 'Liabilities (group)', 'liability', 'general', false) RETURNING id`, s.id).Scan(new(string)), "grouping account")

	person := func(name string) (user, member string) {
		user = s.user(name)
		return user, s.member(user)
	}
	var m string
	b.officer, m = person("Treasurer")
	fixture(t, s.appoint(m, "treasurer", true, "2020-01-01", ""), "treasurer")
	b.auditor, m = person("Internal auditor")
	fixture(t, s.appoint(m, "internal_auditor", false, "2020-01-01", ""), "auditor")
	b.former, m = person("Former secretary")
	fixture(t, s.appoint(m, "secretary", false, "2020-01-01", "2020-12-31"), "ended term")
	var userA, userB string
	userA, b.memberA = person("Adaeze Okafor")
	userB, b.memberB = person("Chidi Okoro")
	b.plain = userA // a member of the society with no post
	_ = userB
	b.abroad = b.t.s.user("Chairman of another society")
	fixture(t, b.t.s.appoint(b.t.s.member(b.abroad), "chairman", true, "2020-01-01", ""), "officer elsewhere")
	b.otherActor = s.user("Secretary (actor)")

	post := func(e entry, ls ...line) string {
		id, err := s.postEntry(e, ls...)
		fixture(t, err, "entry "+e.kind)
		return id
	}
	c := b.c
	batch := s.openingBatch()
	fixture(t, db.QueryRow(bg(), `SELECT reference FROM ledger_batches WHERE id = $1`, batch).Scan(&b.openingRef), "batch reference")
	b.eOpening = post(entry{kind: "opening_balance", batch: batch, unknownDate: true},
		line{acct: c.migration, kobo: 48000000}, line{acct: c.savings, kobo: -48000000, member: b.memberA})
	b.eContrib = post(entry{kind: "contribution_in", valueDate: "2026-03-02"},
		line{acct: c.bank, kobo: 2500000}, line{acct: c.savings, kobo: -2491250, member: b.memberA},
		line{acct: c.partner, kobo: -5000, role: "partner_charge"}, line{acct: c.margin, kobo: -3750, role: "argon_margin"})
	b.eReversal = post(entry{kind: "contribution_in", valueDate: "2026-03-05", reverses: b.eContrib, reason: "Posted twice in error by the treasurer"},
		line{acct: c.savings, kobo: 2491250, member: b.memberA},
		line{acct: c.partner, kobo: 5000, role: "partner_charge"}, line{acct: c.margin, kobo: 3750, role: "argon_margin"},
		line{acct: c.bank, kobo: -2500000})
	b.eLate = post(entry{kind: "payroll_remittance", valueDate: "2026-09-01"},
		line{acct: c.bank, kobo: 200000}, line{acct: c.savings, kobo: -200000, member: b.memberB})
	b.eBackdated = post(entry{kind: "contribution_in", valueDate: "2026-01-15", actor: b.otherActor},
		line{acct: c.bank, kobo: 1000000}, line{acct: c.savings, kobo: -996500, member: b.memberA},
		line{acct: c.partner, kobo: -2000, role: "partner_charge"}, line{acct: c.margin, kobo: -1500, role: "argon_margin"})
	var err error
	b.eOther, err = b.t.s.postEntry(entry{kind: "contribution_in", valueDate: "2026-03-02"},
		line{acct: b.t.bank, kobo: 700000}, line{acct: b.t.savings, kobo: -700000, member: b.t.member})
	fixture(t, err, "another society's entry")
	return b
}

// The chart's codes, in code order: newSociety's 1000/2000, newChart's seven, and 2900.
var booksCodes = []string{"1000", "1001", "1201", "2000", "2001", "2101", "2111", "2201", "2900", "3901"}

func (b *books) path(society, rest string) string { return "/societies/" + society + rest }
func (b *books) read(t *testing.T, as, rest string) resp {
	return get(t, b.srv, b.path(b.c.s.id, rest), signIn(t, as))
}

// ---- wire shapes, decoded loosely, as a client reads them ----------------------

type bAccount struct {
	Code, Name, Kind, Role string
	Postable               bool
}
type bChart struct {
	Accounts []bAccount `json:"accounts"`
}
type bTBRow struct {
	Code    string `json:"code"`
	Debits  string `json:"debitsKobo"`
	Credits string `json:"creditsKobo"`
	Balance string `json:"balanceKobo"`
}
type bTB struct {
	Rows         []bTBRow `json:"rows"`
	TotalDebits  string   `json:"totalDebitsKobo"`
	TotalCredits string   `json:"totalCreditsKobo"`
}
type bGLRow struct {
	EntryId   string  `json:"entryId"`
	ValueDate *string `json:"valueDate"`
	Amount    string  `json:"amountKobo"`
	Balance   string  `json:"balanceKobo"`
}
type bGL struct {
	Opening string   `json:"openingBalanceKobo"`
	Closing string   `json:"closingBalanceKobo"`
	Rows    []bGLRow `json:"rows"`
}
type bLine struct {
	AccountCode string  `json:"accountCode"`
	Amount      string  `json:"amountKobo"`
	ChargeRole  *string `json:"chargeRole"`
}
type bEntry struct {
	EntryId    string   `json:"entryId"`
	ReversedBy []string `json:"reversedBy"`
	Lines      []bLine  `json:"lines"`
}
type bJournal struct {
	Entries []bEntry `json:"entries"`
}

func ok200[T any](t *testing.T, r resp) T {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("REFUSED — want 200, got %d: %s", r.status, r.body)
	}
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("WRONG SHAPE — %v: %s", err, r.body)
	}
	return v
}

func kobo(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("WRONG SHAPE — %q is not a kobo string", s)
	}
	return v
}

func (b *books) tb(t *testing.T, asOf string) bTB {
	return ok200[bTB](t, b.read(t, b.officer, "/trial-balance?asOf="+asOf))
}
func tbRow(t *testing.T, tb bTB, code string) bTBRow {
	t.Helper()
	for _, r := range tb.Rows {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("WRONG SHAPE — account %s is missing from the trial balance", code)
	return bTBRow{}
}
func (b *books) gl(t *testing.T, code, query string) bGL {
	return ok200[bGL](t, b.read(t, b.officer, "/accounts/"+code+"/ledger"+query))
}
func (b *books) journal(t *testing.T, query string) bJournal {
	return ok200[bJournal](t, b.read(t, b.officer, "/journal"+query))
}
func entryIDs(j bJournal) []string {
	out := []string{}
	for _, e := range j.Entries {
		out = append(out, e.EntryId)
	}
	return out
}
func entryOf(t *testing.T, j bJournal, id string) bEntry {
	t.Helper()
	for _, e := range j.Entries {
		if e.EntryId == id {
			return e
		}
	}
	t.Fatalf("WRONG SHAPE — entry %s is missing from the journal", id)
	return bEntry{}
}

// names makes a failure readable: entry ids become the story's names.
func (b *books) names(ids []string) string {
	n := map[string]string{b.eOpening: "eOpening", b.eContrib: "eContrib", b.eReversal: "eReversal",
		b.eLate: "eLate", b.eBackdated: "eBackdated", b.eOther: "eOther(another society)"}
	out := []string{}
	for _, id := range ids {
		if v, ok := n[id]; ok {
			out = append(out, v)
		} else {
			out = append(out, id)
		}
	}
	return "[" + strings.Join(out, " ") + "]"
}

func (b *books) wantEntries(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ACCEPTED — the journal returned %s, want %s", b.names(got), b.names(want))
	}
}

// =============================================================================
// WHO MAY READ THE BOOKS
// =============================================================================

var booksEndpoints = []string{"/accounts", "/trial-balance?asOf=" + booksAsOf, "/accounts/2001/ledger", "/journal"}

func TestApiBooks1_AuditorReadsTheBooks(t *testing.T) {
	b := newBooks(t)
	for _, ep := range booksEndpoints {
		if r := b.read(t, b.auditor, ep); r.status != http.StatusOK {
			t.Fatalf("REFUSED — the internal auditor (read-only, reads everything) was refused %s: %d %s", ep, r.status, r.body)
		}
	}
}

// Every refusal is byte-for-byte the answer for a society that does not exist.
func (b *books) wantRefusedLikeNoSociety(t *testing.T, as, who string) {
	t.Helper()
	for _, ep := range booksEndpoints {
		mine := b.read(t, as, ep)
		none := get(t, b.srv, b.path(noSuchRow, ep), signIn(t, as))
		if mine.status != http.StatusNotFound {
			t.Fatalf("ACCEPTED — %s read %s: %d %s", who, ep, mine.status, mine.body)
		}
		if string(mine.body) != string(none.body) || none.status != http.StatusNotFound {
			t.Fatalf("ACCEPTED — the refusal tells %s the society exists (%s):\n  this society: %s\n  no society:   %d %s", who, ep, mine.body, none.status, none.body)
		}
	}
}

func TestApiBooks2_MemberOfTheSocietyGetsNotFound(t *testing.T) {
	b := newBooks(t)
	b.wantRefusedLikeNoSociety(t, b.plain, "a member of the society holding no post")
}

func TestApiBooks3_OfficerOfAnotherSocietyGetsNotFound(t *testing.T) {
	b := newBooks(t)
	b.wantRefusedLikeNoSociety(t, b.abroad, "an officer of another society")
}

func TestApiBooks4_FormerOfficerGetsNotFound(t *testing.T) {
	b := newBooks(t)
	b.wantRefusedLikeNoSociety(t, b.former, "an officer whose term ended")
}

// =============================================================================
// CHART OF ACCOUNTS
// =============================================================================

func (b *books) chart(t *testing.T) bChart {
	return ok200[bChart](t, b.read(t, b.officer, "/accounts"))
}

func TestApiBooks5_ChartIsOrderedByCode(t *testing.T) {
	b := newBooks(t)
	got := []string{}
	for _, a := range b.chart(t).Accounts {
		got = append(got, a.Code)
	}
	if strings.Join(got, " ") != strings.Join(booksCodes, " ") {
		t.Fatalf("ACCEPTED — chart order %v, want by code %v", got, booksCodes)
	}
}

func TestApiBooks6_ChartSaysWhichAccountsArePostable(t *testing.T) {
	b := newBooks(t)
	for _, a := range b.chart(t).Accounts {
		if want := a.Code != "2900"; a.Postable != want {
			t.Fatalf("ACCEPTED — account %s reported postable=%v, want %v (2900 is a grouping account)", a.Code, a.Postable, want)
		}
	}
}

func TestApiBooks7_ChartIsTheSocietysOwn(t *testing.T) {
	b := newBooks(t)
	if n := len(b.chart(t).Accounts); n != len(booksCodes) {
		t.Fatalf("ACCEPTED — the chart lists %d accounts, want this society's %d (another society uses the same codes)", n, len(booksCodes))
	}
}

// =============================================================================
// TRIAL BALANCE — as at a VALUE date
// =============================================================================

func wantRow(t *testing.T, tb bTB, code string, debits, credits, balance int64, why string) {
	t.Helper()
	r := tbRow(t, tb, code)
	if kobo(t, r.Debits) != debits || kobo(t, r.Credits) != credits || kobo(t, r.Balance) != balance {
		t.Fatalf("ACCEPTED — account %s: debits %s credits %s balance %s; want %d %d %d (%s)",
			code, r.Debits, r.Credits, r.Balance, debits, credits, balance, why)
	}
}

// Everything is POSTED today, after asOf; three entries HAPPENED before it.
func TestApiBooks8_TrialBalanceIsAsAtTheValueDate(t *testing.T) {
	b := newBooks(t)
	wantRow(t, b.tb(t, booksAsOf), "1001", 3500000, -2500000, 1000000,
		"eContrib, eReversal and eBackdated happened before 30 June though all were posted after it")
}

// An opening balance with no as-at date was accumulated before migration:
// it is in the books at every date, the earliest included.
func TestApiBooks9_UndatedOpeningBalanceCountsAtEveryDate(t *testing.T) {
	b := newBooks(t)
	wantRow(t, b.tb(t, "2000-01-01"), "3901", 48000000, 0, 48000000, "the opening balance, as at 2000")
	wantRow(t, b.tb(t, "2000-01-01"), "2001", 0, -48000000, -48000000, "the member's opening savings, as at 2000")
}

func TestApiBooks10_EntriesAfterAsOfAreLeftOut(t *testing.T) {
	b := newBooks(t)
	tb := b.tb(t, booksAsOf)
	wantRow(t, tb, "1001", 3500000, -2500000, 1000000, "eLate (1 September) is after 30 June")
	if late := b.tb(t, "2026-09-01"); kobo(t, tbRow(t, late, "1001").Balance) != 1200000 {
		t.Fatalf("FIXTURE: eLate should count as at its own value date; bank balance %s", tbRow(t, late, "1001").Balance)
	}
}

// The trial balance and the chart are two reads of one set of accounts.
func TestApiBooks11_EveryAccountOfTheChartIsListed(t *testing.T) {
	b := newBooks(t)
	tb := b.tb(t, booksAsOf)
	got := []string{}
	for _, r := range tb.Rows {
		got = append(got, r.Code)
	}
	if strings.Join(got, " ") != strings.Join(booksCodes, " ") {
		t.Fatalf("ACCEPTED — trial balance rows %v, want every account of the chart in code order %v", got, booksCodes)
	}
	wantRow(t, tb, "2201", 0, 0, 0, "the wallet account has no lines: listed, at zero")
}

// A reversed contribution is a debit AND a credit on the savings account, not nothing.
func TestApiBooks12_DebitsAndCreditsAreTurnoverNotNetted(t *testing.T) {
	b := newBooks(t)
	tb := b.tb(t, booksAsOf)
	wantRow(t, tb, "2001", 2491250, -51487750, -48996500, "opening, contribution, its reversal, the backdated contribution")
	wantRow(t, tb, "2101", 5000, -7000, -2000, "partner charge taken twice, refunded once")
	wantRow(t, tb, "2111", 3750, -5250, -1500, "Argon margin taken twice, refunded once")
}

func TestApiBooks13_CreditsAreSignedAndTotalsSumToZero(t *testing.T) {
	b := newBooks(t)
	tb := b.tb(t, booksAsOf)
	for _, r := range tb.Rows {
		d, c, bal := kobo(t, r.Debits), kobo(t, r.Credits), kobo(t, r.Balance)
		if d < 0 || c > 0 || d+c != bal {
			t.Fatalf("ACCEPTED — account %s: debits %d credits %d balance %d; want debits >= 0, credits <= 0, debits + credits = balance", r.Code, d, c, bal)
		}
	}
	if d, c := kobo(t, tb.TotalDebits), kobo(t, tb.TotalCredits); d != 54000000 || c != -54000000 {
		t.Fatalf("ACCEPTED — totals debits %d credits %d; want 54000000 and -54000000, summing to zero", d, c)
	}
}

// The server never assumes "today": the as-at date is the reader's to state.
func TestApiBooks14_AsOfIsRequired(t *testing.T) {
	b := newBooks(t)
	r := b.read(t, b.officer, "/trial-balance")
	var p struct{ Code string }
	if r.status != http.StatusBadRequest || json.Unmarshal(r.body, &p) != nil || p.Code == "" {
		t.Fatalf("ACCEPTED — a trial balance with no asOf: %d %s; want 400 with a JSON problem", r.status, r.body)
	}
}

// =============================================================================
// GENERAL LEDGER — one account, running balance, value-date order
// =============================================================================

func (b *books) wantGL(t *testing.T, g bGL, ids []string, balances []int64) {
	t.Helper()
	got, bals := []string{}, []int64{}
	for _, r := range g.Rows {
		got = append(got, r.EntryId)
		bals = append(bals, kobo(t, r.Balance))
	}
	if strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("ACCEPTED — ledger rows %s, want %s", b.names(got), b.names(ids))
	}
	for i := range balances {
		if bals[i] != balances[i] {
			t.Fatalf("ACCEPTED — running balances %v, want %v", bals, balances)
		}
	}
}

func TestApiBooks15_LedgerIsInValueDateOrder(t *testing.T) {
	b := newBooks(t)
	b.wantGL(t, b.gl(t, "2001", ""),
		[]string{b.eOpening, b.eBackdated, b.eContrib, b.eReversal, b.eLate},
		[]int64{-48000000, -48996500, -51487750, -48996500, -49196500})
}

func TestApiBooks16_UndatedRowsComeFirst(t *testing.T) {
	b := newBooks(t)
	g := b.gl(t, "3901", "")
	if len(g.Rows) == 0 || g.Rows[0].EntryId != b.eOpening || g.Rows[0].ValueDate != nil {
		t.Fatalf("ACCEPTED — the migration account's first row should be the undated opening balance with valueDate null; got %+v", g.Rows)
	}
	g = b.gl(t, "2001", "")
	if len(g.Rows) == 0 || g.Rows[0].EntryId != b.eOpening {
		t.Fatalf("ACCEPTED — on the savings account the undated opening balance should come first, before everything dated; got %s", b.names([]string{g.Rows[0].EntryId}))
	}
}

func TestApiBooks17_OpeningBalanceCarriesWhatCameBeforeFrom(t *testing.T) {
	b := newBooks(t)
	g := b.gl(t, "2001", "?from=2026-02-01&to="+booksAsOf)
	if kobo(t, g.Opening) != -48996500 {
		t.Fatalf("ACCEPTED — opening balance %s, want -48996500 (the undated opening balance and eBackdated, both before 1 February)", g.Opening)
	}
	b.wantGL(t, g, []string{b.eContrib, b.eReversal}, []int64{-51487750, -48996500})
	if kobo(t, g.Closing) != -48996500 {
		t.Fatalf("ACCEPTED — closing balance %s, want -48996500", g.Closing)
	}
}

func TestApiBooks18_AmountIsThisAccountsLinesOnly(t *testing.T) {
	b := newBooks(t)
	for _, c := range []struct {
		code string
		want int64
	}{{"2001", -2491250}, {"1001", 2500000}, {"2101", -5000}} {
		for _, r := range b.gl(t, c.code, "").Rows {
			if r.EntryId == b.eContrib && kobo(t, r.Amount) != c.want {
				t.Fatalf("ACCEPTED — eContrib on %s reads %s, want %d (this account's line, not the whole entry)", c.code, r.Amount, c.want)
			}
		}
	}
}

// Two reads, one record: an account's ledger closes, at a date, on exactly
// the balance the trial balance gives it at that date.
func TestApiBooks19_ClosingBalanceAgreesWithTheTrialBalance(t *testing.T) {
	b := newBooks(t)
	tb := b.tb(t, booksAsOf)
	for _, code := range booksCodes {
		g := b.gl(t, code, "?to="+booksAsOf)
		if g.Closing != tbRow(t, tb, code).Balance {
			t.Fatalf("ACCEPTED — account %s: the ledger closes on %s as at %s, the trial balance says %s",
				code, g.Closing, booksAsOf, tbRow(t, tb, code).Balance)
		}
	}
}

func TestApiBooks20_UnknownAccountIsNotFound(t *testing.T) {
	b := newBooks(t)
	if r := b.read(t, b.officer, "/accounts/9999/ledger"); r.status != http.StatusNotFound {
		t.Fatalf("ACCEPTED — an account code the society does not have: %d %s; want 404", r.status, r.body)
	}
}

// =============================================================================
// JOURNAL — every line, and the filters an audit needs
// =============================================================================

func TestApiBooks21_JournalIsTheSocietysOwnInValueDateOrder(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "")), b.eOpening, b.eBackdated, b.eContrib, b.eReversal, b.eLate)
}

// The reference is how an import is audited after the fact.
func TestApiBooks22_ReferenceFindsAnImport(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "?reference="+b.openingRef)), b.eOpening)
}

func TestApiBooks23_MemberFilterFindsTheirEntries(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "?memberId="+b.memberB)), b.eLate)
}

func TestApiBooks24_ActorFilterFindsWhoPosted(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "?actorUserId="+b.otherActor)), b.eBackdated)
}

func TestApiBooks25_FiltersCombine(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "?memberId="+b.memberA+"&from=2026-03-01")), b.eContrib, b.eReversal)
}

func TestApiBooks26_EveryEntrysLinesSumToZero(t *testing.T) {
	b := newBooks(t)
	j := b.journal(t, "")
	for _, e := range j.Entries {
		var sum int64
		for _, l := range e.Lines {
			sum += kobo(t, l.Amount)
		}
		if sum != 0 {
			t.Fatalf("ACCEPTED — %s's lines sum to %d: a line is missing from the journal", b.names([]string{e.EntryId}), sum)
		}
	}
	if n := len(entryOf(t, j, b.eContrib).Lines); n != 4 {
		t.Fatalf("ACCEPTED — eContrib shows %d lines, want 4 (gross, savings, partner charge, Argon margin)", n)
	}
}

// reversedBy is derived (§2): nothing is written back to the original.
func TestApiBooks27_ReversedByIsDerived(t *testing.T) {
	b := newBooks(t)
	j := b.journal(t, "")
	if got := entryOf(t, j, b.eContrib).ReversedBy; len(got) != 1 || got[0] != b.eReversal {
		t.Fatalf("ACCEPTED — eContrib.reversedBy is %v, want [eReversal]", got)
	}
	if got := entryOf(t, j, b.eReversal).ReversedBy; len(got) != 0 {
		t.Fatalf("ACCEPTED — eReversal.reversedBy is %v, want empty", got)
	}
}

func TestApiBooks28_LinesDebitsFirstThenByCode(t *testing.T) {
	b := newBooks(t)
	got := []string{}
	for _, l := range entryOf(t, b.journal(t, ""), b.eContrib).Lines {
		got = append(got, l.AccountCode+" "+l.Amount)
	}
	want := []string{"1001 2500000", "2001 -2491250", "2101 -5000", "2111 -3750"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("ACCEPTED — eContrib's lines read %v, want debits first, then by code: %v", got, want)
	}
}

// With `from` given, an undated opening balance is BEFORE it, and left out.
func TestApiBooks29_FromLeavesOutUndatedEntries(t *testing.T) {
	b := newBooks(t)
	b.wantEntries(t, entryIDs(b.journal(t, "?from=2026-01-01")), b.eBackdated, b.eContrib, b.eReversal, b.eLate)
}

// =============================================================================
// THE WIRE
// =============================================================================

var koboField = regexp.MustCompile(`"([A-Za-z]*Kobo)":\s*([^,}\]]+)`)

func TestApiBooks30_MoneyIsAStringOnTheWire(t *testing.T) {
	b := newBooks(t)
	for _, ep := range []string{"/trial-balance?asOf=" + booksAsOf, "/accounts/2001/ledger", "/journal"} {
		r := b.read(t, b.officer, ep)
		ms := koboField.FindAllSubmatch(r.body, -1)
		if len(ms) == 0 {
			t.Fatalf("WRONG SHAPE — %s carries no *Kobo field: %s", ep, r.body)
		}
		for _, m := range ms {
			if m[2][0] != '"' {
				t.Fatalf("ACCEPTED — %s sends %s as a JSON number (%s): a float on the wire", ep, m[1], m[2])
			}
		}
	}
}

func TestApiBooks31_EmptyListsAreArraysNotNull(t *testing.T) {
	b := newBooks(t)
	r := b.read(t, b.officer, "/journal?reference=no-such-import")
	if !strings.Contains(string(r.body), `"entries":[]`) {
		t.Fatalf("ACCEPTED — a journal filter matching nothing: %s; want \"entries\":[]", r.body)
	}
	r = b.read(t, b.officer, "/journal?memberId="+b.memberB)
	if !strings.Contains(string(r.body), `"reversedBy":[]`) {
		t.Fatalf("ACCEPTED — an entry nobody reversed: %s; want \"reversedBy\":[]", r.body)
	}
}
