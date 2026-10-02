//go:build tier1

// Acceptance tests for GET /members/{memberId}/statement — written before the
// handler, for review. Every TestApiStatement* name is in this suite.
//
// Decided 2026-10-01:
//   - ONE handler, ONE query, two projections. The member and an officer of
//     the member's society see identical figures; officers additionally see
//     who posted each entry. TestApiStatement3 is the cross-surface test the
//     frontend believed it had.
//   - Charges are their own lines, partner and margin separately (D-03). A
//     refund is a debit, so it can never read as a second charge.
//   - A NULL value date is sent as null, never filled with the posting date.
//   - Balances are from the member's side (positive = owed to the member).
//   - Anyone else gets 404, byte-identical to a member that does not exist.
//
// Which handler runs: default api.NewServer (internal/api, the real one);
// ARGON_TEST_API_FAKE=<break> runs the mutation vehicle in apifake_test.go.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

// ---- harness ----------------------------------------------------------------

func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		h = newFakeAPI(db, brk)
	} else {
		var err error
		// The real server, with the real auth service for the step-up (its
		// SMS and notifier are the service tests' fakes; nothing here sends).
		svc, serr := auth.New(auth.Deps{DB: db, SMS: &fakeSMS{}, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon})
		if serr != nil {
			t.Fatalf("FIXTURE: cannot construct the auth service: %v", serr)
		}
		h, err = api.NewServer(api.Deps{DB: db, Auth: svc})
		if err != nil {
			t.Fatalf("FIXTURE: cannot construct the API: %v", err)
		}
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type resp struct {
	status int
	body   []byte
}

func get(t *testing.T, srv *httptest.Server, path, token string) resp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "GET "+path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b}
}

// signIn gives a user a live session the way the auth service would, and
// returns the bearer token.
func signIn(t *testing.T, user string) string {
	t.Helper()
	tok := randHex()
	var dev string
	fixture(t, db.QueryRow(bg(), `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id`, user, auth.TokenDigest(randHex())).Scan(&dev), "device")
	fixture(t, one(`INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, user, dev, auth.TokenDigest(tok)), "session")
	return tok
}

// randHex: a token unique across PROCESSES, not only within one. It used to be
// a per-process counter plus the leading digits of a zero-padded timestamp
// (always zeros), so every test process minted the same sequence; the drill,
// which runs every suite as its own process in ONE restored database, made
// two of them collide. Random, as a real token is.
func randHex() string { return digest() }

// ---- the statement's wire shape, decoded loosely so the TESTS read the JSON
// as a client would, not through the generated types the handler uses -----------

type stRow struct {
	EntryId            string            `json:"entryId"`
	PostedAt           string            `json:"postedAt"`
	ValueDate          *string           `json:"valueDate"`
	Kind               string            `json:"kind"`
	Reference          *string           `json:"reference"`
	Direction          string            `json:"direction"`
	GrossKobo          string            `json:"grossKobo"`
	SavingsLineKobo    string            `json:"savingsLineKobo"`
	Charges            []json.RawMessage `json:"charges"`
	SavingsBalanceKobo string            `json:"savingsBalanceKobo"`
	ReversesEntryId    *string           `json:"reversesEntryId"`
	Reason             *string           `json:"reason"`
	PostedBy           json.RawMessage   `json:"postedBy"`
}

type stmt struct {
	MemberId                  string  `json:"memberId"`
	Projection                string  `json:"projection"`
	OpeningSavingsBalanceKobo string  `json:"openingSavingsBalanceKobo"`
	ClosingSavingsBalanceKobo string  `json:"closingSavingsBalanceKobo"`
	ChargedInPeriod           struct {
		Partner string `json:"partnerChargeKobo"`
		Margin  string `json:"argonMarginKobo"`
	} `json:"chargedInPeriod"`
	Rows []stRow `json:"rows"`
}

func decode(t *testing.T, r resp) stmt {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("REFUSED — want 200, got %d: %s", r.status, r.body)
	}
	var s stmt
	if err := json.Unmarshal(r.body, &s); err != nil {
		t.Fatalf("WRONG SHAPE — the body is not a statement: %v: %s", err, r.body)
	}
	return s
}

type charge struct {
	Role   string `json:"role"`
	Amount string `json:"amountKobo"`
}

func charges(t *testing.T, row stRow) []charge {
	t.Helper()
	var out []charge
	for _, c := range row.Charges {
		var x charge
		if err := json.Unmarshal(c, &x); err != nil {
			t.Fatalf("WRONG SHAPE — charge line: %v", err)
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role > out[j].Role }) // partner_charge, argon_margin
	return out
}

// ---- the fixture: one society, the §7 story for one member --------------------

type world struct {
	srv                                     *httptest.Server
	c                                       *chart
	memberUser, member                      string
	other, otherUser                        string
	officer, auditor, former, elsewhere     string // users
	eOpening, eContrib, eReversal, eWallet, eToOther string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	wd := &world{srv: apiServer(t), c: newChart(t)}
	s := wd.c.s
	mk := func(name string) (user, member string) {
		fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, name).Scan(&user), "user")
		return user, s.member(user)
	}
	wd.memberUser, wd.member = mk("Adaeze Okafor")
	wd.otherUser, wd.other = mk("Chidi Okoro")
	var om, am, fm string
	wd.officer, om = mk("Treasurer")
	fixture(t, s.appoint(om, "treasurer", true, "2020-01-01", ""), "treasurer")
	wd.auditor, am = mk("Auditor")
	fixture(t, s.appoint(am, "internal_auditor", false, "2020-01-01", ""), "auditor")
	wd.former, fm = mk("Former secretary")
	fixture(t, s.appoint(fm, "secretary", false, "2020-01-01", "2020-12-31"), "ended term")
	elsewhere := newSociety(t)
	var em string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Officer elsewhere') RETURNING id`).Scan(&wd.elsewhere), "user")
	em = elsewhere.member(wd.elsewhere)
	fixture(t, elsewhere.appoint(em, "chairman", true, "2020-01-01", ""), "officer of another society")

	post := func(e entry, ls ...line) string {
		id, err := s.postEntry(e, ls...)
		fixture(t, err, "entry "+e.kind)
		return id
	}
	c := wd.c
	// §7b: opening balance, the society's records carry NO as-at date.
	wd.eOpening = post(entry{kind: "opening_balance", batch: s.openingBatch(), unknownDate: true},
		line{acct: c.migration, kobo: 48000000}, line{acct: c.savings, kobo: -48000000, member: wd.member})
	// §7a: ₦25,000 contribution, ₦87.50 charge split partner/margin.
	wd.eContrib = post(entry{kind: "contribution_in"},
		line{acct: c.bank, kobo: 2500000}, line{acct: c.savings, kobo: -2491250, member: wd.member},
		line{acct: c.partner, kobo: -5000, role: "partner_charge"}, line{acct: c.margin, kobo: -3750, role: "argon_margin"})
	// §7c: its reversal, refunding the charge (D-02).
	wd.eReversal = post(entry{kind: "contribution_in", reverses: wd.eContrib, reason: "Posted twice in error by the treasurer"},
		line{acct: c.savings, kobo: 2491250, member: wd.member},
		line{acct: c.partner, kobo: 5000, role: "partner_charge"}, line{acct: c.margin, kobo: 3750, role: "argon_margin"},
		line{acct: c.bank, kobo: -2500000})
	// Wallet → savings: no bank line at all.
	wd.eWallet = post(entry{kind: "transfer_out"},
		line{acct: c.wallet, kobo: 100000, member: wd.member}, line{acct: c.savings, kobo: -100000, member: wd.member})
	// Savings → another member's savings, in one entry.
	wd.eToOther = post(entry{kind: "transfer_out"},
		line{acct: c.savings, kobo: 50000, member: wd.member}, line{acct: c.savings, kobo: -50000, member: wd.other})
	return wd
}

func (wd *world) statement(t *testing.T, as, query string) resp {
	return get(t, wd.srv, "/members/"+wd.member+"/statement"+query, signIn(t, as))
}

func rowOf(t *testing.T, s stmt, entryID string) stRow {
	t.Helper()
	for _, r := range s.Rows {
		if r.EntryId == entryID {
			return r
		}
	}
	t.Fatalf("WRONG SHAPE — entry %s is missing from the statement", entryID)
	return stRow{}
}

// =============================================================================
// PROJECTIONS — one record
// =============================================================================

func TestApiStatement1_MemberReadsOwnStatementWithoutAttribution(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	if s.Projection != "member" {
		t.Fatalf("WRONG PROJECTION — the member was served %q", s.Projection)
	}
	for _, r := range s.Rows {
		if string(r.PostedBy) != "null" {
			t.Fatalf("ACCEPTED — the member projection carries attribution: %s", r.PostedBy)
		}
	}
}

func TestApiStatement2_OfficerSeesWhoPosted(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.officer, ""))
	if s.Projection != "officer" {
		t.Fatalf("WRONG PROJECTION — an officer was served %q", s.Projection)
	}
	r := rowOf(t, s, wd.eContrib)
	var pb struct {
		UserId    string `json:"userId"`
		PostLabel string `json:"postLabel"`
	}
	if json.Unmarshal(r.PostedBy, &pb) != nil || pb.UserId == "" || pb.PostLabel == "" {
		t.Fatalf("REFUSED — the officer projection lacks attribution: %s", r.PostedBy)
	}
}

// The cross-surface test: the member's statement and the officer's are the
// same record. Strip what the projection is allowed to differ in, and the
// two bodies must be identical, byte for byte.
func TestApiStatement3_ProjectionsAreOneRecord(t *testing.T) {
	wd := newWorld(t)
	strip := func(b []byte) string {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("WRONG SHAPE — %v", err)
		}
		delete(m, "projection")
		for _, r := range m["rows"].([]any) {
			delete(r.(map[string]any), "postedBy")
		}
		out, _ := json.Marshal(m)
		return string(out)
	}
	mine := wd.statement(t, wd.memberUser, "")
	theirs := wd.statement(t, wd.officer, "")
	decode(t, mine)
	decode(t, theirs)
	if a, b := strip(mine.body), strip(theirs.body); a != b {
		t.Fatalf("ACCEPTED — the member's statement and the officer's disagree:\n  member:  %s\n  officer: %s", a, b)
	}
}

func TestApiStatement4_AuditorReadsTheOfficerProjection(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.auditor, ""))
	if s.Projection != "officer" {
		t.Fatalf("WRONG PROJECTION — the internal auditor (read-only, reads everything) was served %q", s.Projection)
	}
}

// =============================================================================
// FIGURES
// =============================================================================

func TestApiStatement5_ChargesAreTheirOwnLinesPartnerAndMarginApart(t *testing.T) {
	wd := newWorld(t)
	r := rowOf(t, decode(t, wd.statement(t, wd.memberUser, "")), wd.eContrib)
	got := charges(t, r)
	if len(got) != 2 || got[0] != (charge{"partner_charge", "-5000"}) || got[1] != (charge{"argon_margin", "-3750"}) {
		t.Fatalf("ACCEPTED — the ₦87.50 is not shown as partner ₦50 and margin ₦37.50 on separate lines: %+v", got)
	}
	if r.GrossKobo != "2500000" || r.SavingsLineKobo != "-2491250" || r.Direction != "in" {
		t.Fatalf("WRONG FIGURES — gross %s, savings line %s, direction %s; want 2500000, -2491250, in", r.GrossKobo, r.SavingsLineKobo, r.Direction)
	}
}

// A refund is a debit: it nets the charge to zero for the period, and is
// never counted as a second charge.
func TestApiStatement6_RefundIsNotASecondCharge(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	got := charges(t, rowOf(t, s, wd.eReversal))
	if len(got) != 2 || got[0].Amount != "5000" || got[1].Amount != "3750" {
		t.Fatalf("ACCEPTED — the reversal's refunded charges are not positive (refund) lines: %+v", got)
	}
	if s.ChargedInPeriod.Partner != "0" || s.ChargedInPeriod.Margin != "0" {
		t.Fatalf("ACCEPTED — a charge and its refund were counted as charged: partner %s, margin %s (want 0, 0)",
			s.ChargedInPeriod.Partner, s.ChargedInPeriod.Margin)
	}
}

func TestApiStatement7_UnknownValueDateIsSentAsNull(t *testing.T) {
	wd := newWorld(t)
	r := wd.statement(t, wd.memberUser, "")
	row := rowOf(t, decode(t, r), wd.eOpening)
	if row.ValueDate != nil {
		t.Fatalf("ACCEPTED — an unknown as-at date was sent as %q (the import date was invented)", *row.ValueDate)
	}
	var raw struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	fixture(t, json.Unmarshal(r.body, &raw), "decode rows as maps")
	for _, m := range raw.Rows {
		if string(m["entryId"]) != `"`+wd.eOpening+`"` {
			continue
		}
		if v, ok := m["valueDate"]; !ok || string(v) != "null" {
			t.Fatalf("ACCEPTED — the opening balance's valueDate key is absent rather than null: a blank, not a stated unknown")
		}
	}
}

func TestApiStatement8_BalancesAreFromTheMembersSide(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	want := map[string]string{wd.eOpening: "48000000", wd.eContrib: "50491250", wd.eReversal: "48000000", wd.eWallet: "48100000", wd.eToOther: "48050000"}
	for id, bal := range want {
		if got := rowOf(t, s, id).SavingsBalanceKobo; got != bal {
			t.Fatalf("WRONG FIGURES — balance after %s is %s, want %s (positive = the society owes the member)", id, got, bal)
		}
	}
	if s.OpeningSavingsBalanceKobo != "0" || s.ClosingSavingsBalanceKobo != "48050000" {
		t.Fatalf("WRONG FIGURES — opening %s, closing %s; want 0, 48050000", s.OpeningSavingsBalanceKobo, s.ClosingSavingsBalanceKobo)
	}
}

// An internal transfer has no bank line. Direction comes from the member's
// savings line, or a wallet→savings move reads as money going out.
func TestApiStatement9_DirectionComesFromTheSavingsLine(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	if r := rowOf(t, s, wd.eWallet); r.Direction != "in" || r.GrossKobo != "100000" {
		t.Fatalf("WRONG FIGURES — wallet→savings: direction %s, gross %s; want in, 100000", r.Direction, r.GrossKobo)
	}
	if r := rowOf(t, s, wd.eToOther); r.Direction != "out" || r.SavingsLineKobo != "50000" {
		t.Fatalf("WRONG FIGURES — savings→another member: direction %s, savings line %s; want out, 50000", r.Direction, r.SavingsLineKobo)
	}
}

func TestApiStatement10_RowsInPostingOrder(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	got := []string{}
	for _, r := range s.Rows {
		got = append(got, r.EntryId)
	}
	want := []string{wd.eOpening, wd.eContrib, wd.eReversal, wd.eWallet, wd.eToOther}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("WRONG ORDER — rows are not in posting order:\n  got  %v\n  want %v", got, want)
	}
}

// =============================================================================
// WIRE
// =============================================================================

// Every money field is a STRING (§0): a JSON number would be a float.
func TestApiStatement11_MoneyIsAStringOnTheWire(t *testing.T) {
	wd := newWorld(t)
	r := wd.statement(t, wd.memberUser, "")
	decode(t, r)
	if m := regexp.MustCompile(`"[A-Za-z]*Kobo":\s*-?[0-9]`).Find(r.body); m != nil {
		t.Fatalf("ACCEPTED — a money field is a JSON number: %s", m)
	}
}

// Go marshals an empty nil slice as null. A free movement must still say
// "no charges" as [], and an empty statement as rows: [].
func TestApiStatement12_EmptyListsAreArraysNotNull(t *testing.T) {
	wd := newWorld(t)
	r := wd.statement(t, wd.memberUser, "")
	s := decode(t, r)
	if rowOf(t, s, wd.eOpening).Charges == nil || bytes.Contains(r.body, []byte(`"charges":null`)) {
		t.Fatalf("ACCEPTED — a free movement's charges are null, not []")
	}
	empty := get(t, wd.srv, "/members/"+wd.other+"/statement?to=2000-01-01", signIn(t, wd.otherUser))
	decode(t, empty)
	if !bytes.Contains(empty.body, []byte(`"rows":[]`)) {
		t.Fatalf("ACCEPTED — an empty statement's rows are not []: %s", empty.body)
	}
}

func TestApiStatement13_PeriodBoundsRowsAndCarriesTheBalance(t *testing.T) {
	wd := newWorld(t)
	s := decode(t, wd.statement(t, wd.memberUser, "?from=2999-01-01"))
	if len(s.Rows) != 0 || s.OpeningSavingsBalanceKobo != "48050000" || s.ClosingSavingsBalanceKobo != "48050000" {
		t.Fatalf("WRONG FIGURES — a period after every entry: %d rows, opening %s, closing %s; want 0, 48050000, 48050000",
			len(s.Rows), s.OpeningSavingsBalanceKobo, s.ClosingSavingsBalanceKobo)
	}
}

// In an entry that also moves another member's savings, only this member's
// line is theirs to see.
//
// A leak has two forms, and the first version of this test caught only one.
// The visible form: the other member's id or line appears in the body. The
// silent form: their line is mixed into THIS member's figures. Summing every
// savings line of the entry gives +50000 − 50000 = 0, so the member's
// movement vanishes, and nothing foreign appears anywhere in the body. That
// version passed against the break that does exactly this
// (leak_other_lines); it now asserts the member's own figures as well.
func TestApiStatement14_AnotherMembersLineNeverLeaks(t *testing.T) {
	wd := newWorld(t)
	r := wd.statement(t, wd.memberUser, "")
	s := decode(t, r)
	if bytes.Contains(r.body, []byte(wd.other)) || bytes.Contains(r.body, []byte(`"-50000"`)) {
		t.Fatalf("ACCEPTED — the statement exposes the other member's line or id")
	}
	row := rowOf(t, s, wd.eToOther)
	if row.SavingsLineKobo != "50000" || row.SavingsBalanceKobo != "48050000" {
		t.Fatalf("ACCEPTED — the other member's line is mixed into this member's figures: savings line %s, balance %s; want 50000, 48050000",
			row.SavingsLineKobo, row.SavingsBalanceKobo)
	}
}

// =============================================================================
// WHO MAY READ — everyone else gets the same 404 as "no such member"
// =============================================================================

func (wd *world) wantNotFoundLikeNoSuchMember(t *testing.T, as, what string) {
	t.Helper()
	tok := signIn(t, as)
	got := get(t, wd.srv, "/members/"+wd.member+"/statement", tok)
	none := get(t, wd.srv, "/members/00000000-0000-4000-8000-00000000beef/statement", tok)
	if none.status != http.StatusNotFound {
		t.Fatalf("FIXTURE: a member id that does not exist answered %d, not 404", none.status)
	}
	if got.status != http.StatusNotFound || !bytes.Equal(got.body, none.body) {
		t.Fatalf("ACCEPTED — %s got %d %s; a member that does not exist gets 404 %s. They must be indistinguishable",
			what, got.status, got.body, none.body)
	}
}

func TestApiStatement15_AnotherMemberGetsNotFound(t *testing.T) {
	wd := newWorld(t)
	wd.wantNotFoundLikeNoSuchMember(t, wd.otherUser, "another member of the same society")
}

func TestApiStatement16_OfficerOfAnotherSocietyGetsNotFound(t *testing.T) {
	wd := newWorld(t)
	wd.wantNotFoundLikeNoSuchMember(t, wd.elsewhere, "an officer of a different society")
}

func TestApiStatement17_FormerOfficerGetsNotFound(t *testing.T) {
	wd := newWorld(t)
	wd.wantNotFoundLikeNoSuchMember(t, wd.former, "someone whose officer term ended")
}

func TestApiStatement18_NoValidSessionGetsUnauthorized(t *testing.T) {
	wd := newWorld(t)
	path := "/members/" + wd.member + "/statement"
	tok := signIn(t, wd.memberUser)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(tok)), "end session")
	for what, token := range map[string]string{"no token": "", "an invented token": "not-a-session", "an ended session": tok} {
		if r := get(t, wd.srv, path, token); r.status != http.StatusUnauthorized {
			t.Fatalf("ACCEPTED — %s got %d, want 401", what, r.status)
		}
	}
}

// The period's charge totals, partner and margin
// apart, when they are not zero. Test 6 is the only other check on these
// totals, and there the charge and its refund cancel to 0 and 0, so swapping
// partner and margin moves 3750 − 3750 and nothing shows. The real handler
// with exactly that swap passed all 18 tests. One more contribution, never
// refunded, makes the totals 5000 and 3750: distinct, so a swap is visible.
func TestApiStatement19_ChargedInPeriodKeepsPartnerAndMarginApart(t *testing.T) {
	wd := newWorld(t)
	c := wd.c
	_, err := c.s.postEntry(entry{kind: "contribution_in"},
		line{acct: c.bank, kobo: 2500000}, line{acct: c.savings, kobo: -2491250, member: wd.member},
		line{acct: c.partner, kobo: -5000, role: "partner_charge"}, line{acct: c.margin, kobo: -3750, role: "argon_margin"})
	fixture(t, err, "an unrefunded contribution")
	s := decode(t, wd.statement(t, wd.memberUser, ""))
	if s.ChargedInPeriod.Partner != "5000" || s.ChargedInPeriod.Margin != "3750" {
		t.Fatalf("WRONG FIGURES — charged in period: partner %s, margin %s; want 5000, 3750 (one ₦87.50 charge kept, one refunded)",
			s.ChargedInPeriod.Partner, s.ChargedInPeriod.Margin)
	}
}
