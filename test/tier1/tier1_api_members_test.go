//go:build tier1

// ACCEPTANCE TESTS — THE REGISTER OVER HTTP (2026-11-10). Members before the
// wallet (the reviewer): a society founded this week can activate its officers
// and then stop, because nothing lets an officer reach a member. The spec was
// agreed with the frontend before this file.
//
//	FIND: officers of the society only (the auditor included); by number,
//	phone (any Nigerian form) or part of a name; what the issuing screen
//	needs (posts and signing, PIN, device, enrolment, opening balance).
//	BRANCHES: readable by officers, so an import's preview catches an
//	unknown branch before any PIN.
//	REGISTER ONE: the secretary, on their step-up; the phone put in +234
//	form; refused with why.
//	IMPORT: ONE CALL, all or nothing, EVERY refused row at once, the client's
//	reference once (a repeat answers the first commit's line → member map).
//	BALANCES: the treasurer's (suite batch), by member number from the file.
package tier1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type aMember struct {
	MemberId, MemberNumber, FullName, Phone, BranchCode, Status, Enrolment string
	HasPin, HasTrustedDevice, OpeningBalancePosted                         bool
	Posts                                                                  []struct {
		Post        string
		IsSignatory bool
	}
}

func (w *mapi) find(t *testing.T, as, q string) (resp, []aMember) {
	t.Helper()
	r := w.call(t, http.MethodGet, as, "/societies/"+w.c.s.id+"/members?q="+url.QueryEscape(q), nil)
	var page struct{ Items []aMember }
	if r.status == http.StatusOK {
		fixture(t, json.Unmarshal(r.body, &page), "the member page")
	}
	return r, page.Items
}

func memberByNumber(t *testing.T, id string) string {
	t.Helper()
	var n string
	fixture(t, db.QueryRow(bg(), `SELECT member_number FROM members WHERE id = $1`, id).Scan(&n), "a member's number")
	return n
}

type regBody map[string]any

func aRegistrant(n int64) regBody {
	return regBody{"memberNumber": fmt.Sprintf("NEW-%d", n), "fullName": "Chiamaka Obi", "phone": fmt.Sprintf("0803%07d", n%10000000),
		"branchCode": "HQ", "joinedOn": "2018-06-01", "collectionChannel": "payroll",
		"nextOfKin": map[string]string{"fullName": "Obinna Obi", "phone": fmt.Sprintf("0805%07d", n%10000000), "relationship": "brother"}}
}

func (w *mapi) registerOne(t *testing.T, as string, b regBody) resp {
	t.Helper()
	b["pinProofId"] = stepUp(t, as, "member_register")
	return w.call(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/members", b)
}

func (w *mapi) importRegister(t *testing.T, as, key string, rows []regBody) resp {
	t.Helper()
	for i := range rows {
		rows[i]["line"] = i + 2
	}
	return w.callH(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/members/batches",
		map[string]any{"rows": rows, "pinProofId": stepUp(t, as, "member_register")}, map[string]string{"Idempotency-Key": key})
}

func countMembers(t *testing.T, society string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM members WHERE society_id = $1`, society).Scan(&n), "members")
	return n
}

// =============================================================================
// FIND
// =============================================================================

func TestApiMembers01_TheRegisterIsForTheSocietysOfficers(t *testing.T) {
	w := newMoneyAPI(t)
	for who, as := range map[string]string{"the treasurer": w.poster, "the internal auditor": w.auditor} {
		if r, _ := w.find(t, as, ""); r.status != http.StatusOK {
			t.Fatalf("REFUSED — %s reading the register: %d %s", who, r.status, r.body)
		}
	}
	for who, as := range map[string]string{"a member with no post": w.borrower, "another society's treasurer": w.abroad} {
		if r, _ := w.find(t, as, ""); r.status != http.StatusNotFound {
			t.Fatalf("READ — %s read the register: %d %s; want 404, as for no society", who, r.status, r.body)
		}
	}
}

func TestApiMembers02_FoundByNumberPhoneOrName(t *testing.T) {
	w := newMoneyAPI(t)
	b := aRegistrant(seq.Add(1))
	r := w.registerOne(t, w.secretary, b)
	if r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: a member to find: %d %s", r.status, r.body)
	}
	for _, q := range []string{strings.ToLower(b["memberNumber"].(string)), b["phone"].(string), "234" + b["phone"].(string)[1:], "amaka ob"} {
		_, got := w.find(t, w.poster, q)
		if len(got) != 1 || got[0].MemberNumber != b["memberNumber"] {
			t.Fatalf("NOT FOUND — q %q found %+v; want %s", q, got, b["memberNumber"])
		}
	}
}

// What the issuing screen needs: a signatory's posts, and where enrolment stands.
func TestApiMembers03_TheSummarySaysPostsEnrolmentAndBalance(t *testing.T) {
	w := newMoneyAPI(t)
	_, got := w.find(t, w.poster, memberByNumber(t, w.s1Mem))
	if len(got) != 1 || len(got[0].Posts) != 1 || got[0].Posts[0].Post != "treasurer" || !got[0].Posts[0].IsSignatory {
		t.Fatalf("WRONG — the treasurer reads %+v; want one post, treasurer, signing", got)
	}
	_, got = w.find(t, w.poster, memberByNumber(t, w.bMem))
	if len(got) != 1 || got[0].Enrolment != "none" {
		t.Fatalf("WRONG — a member with no code reads %+v; want enrolment none", got)
	}
	// A signatory's code with one half issued (by another signatory): pending.
	if r := w.call(t, http.MethodPost, w.poster, "/societies/"+w.c.s.id+"/members/"+w.s2Mem+"/enrolment-codes",
		map[string]any{"pinProofId": stepUp(t, w.poster, "enrolment_issue")}); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the first half of the chairman's code: %d %s", r.status, r.body)
	}
	_, got = w.find(t, w.poster, memberByNumber(t, w.s2Mem))
	if len(got) != 1 || got[0].Enrolment != "code_pending" {
		t.Fatalf("UNSEEN — a signatory with one half issued reads %+v; want code_pending", got)
	}
	// The opening balance: the treasurer's screen sees who has one.
	_, got = w.find(t, w.poster, memberByNumber(t, w.g1Mem))
	if len(got) != 1 || got[0].OpeningBalancePosted {
		t.Fatalf("WRONG — before any balance: %+v", got)
	}
	if r := w.postBalances(t, w.poster, []map[string]any{{"line": 2, "memberNumber": memberByNumber(t, w.g1Mem), "amountKobo": "500000"}}); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: an opening balance: %d %s", r.status, r.body)
	}
	_, got = w.find(t, w.poster, memberByNumber(t, w.g1Mem))
	if len(got) != 1 || !got[0].OpeningBalancePosted {
		t.Fatalf("UNSEEN — after the balance: %+v; want openingBalancePosted", got)
	}
}

func (w *mapi) postBalances(t *testing.T, as string, rows []map[string]any) resp {
	t.Helper()
	var total int64
	for _, r := range rows {
		var n int64
		fmt.Sscan(r["amountKobo"].(string), &n)
		total += n
	}
	return w.callH(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/ledger/batches",
		map[string]any{"kind": "opening_balance", "asOf": nil, "confirmedTotalKobo": fmt.Sprint(total), "rows": rows, "pinProofId": stepUp(t, as, "ledger_batch")},
		map[string]string{"Idempotency-Key": "BAL-" + digest()[:16]})
}

func TestApiMembers04_BranchesAreReadByOfficers(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.call(t, http.MethodGet, w.secretary, "/societies/"+w.c.s.id+"/branches", nil)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"code":"HQ"`) {
		t.Fatalf("UNREAD — the secretary reading the branches: %d %s", r.status, r.body)
	}
	if r := w.call(t, http.MethodGet, w.borrower, "/societies/"+w.c.s.id+"/branches", nil); r.status != http.StatusNotFound {
		t.Fatalf("READ — a member with no post read the branches: %d", r.status)
	}
}

// =============================================================================
// REGISTER ONE
// =============================================================================

func TestApiMembers05_TheSecretaryRegistersOneInPlusTwoThreeFourForm(t *testing.T) {
	w := newMoneyAPI(t)
	b := aRegistrant(seq.Add(1))
	r := w.registerOne(t, w.secretary, b)
	var m aMember
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &m) != nil {
		t.Fatalf("REFUSED — the secretary registering a member: %d %s", r.status, r.body)
	}
	if m.Phone != "+234"+b["phone"].(string)[1:] || m.Status != "active" || m.HasPin || m.HasTrustedDevice || m.Enrolment != "none" {
		t.Fatalf("WRONG — %+v; want the phone in +234 form, active, no PIN, no device", m)
	}
	wantProblem(t, w.registerOne(t, w.poster, aRegistrant(seq.Add(1))), http.StatusForbidden, "not_permitted", "the treasurer registering a member")
	b2 := aRegistrant(seq.Add(1))
	b2["pinProofId"] = "00000000-0000-4000-8000-000000000000"
	wantProblem(t, w.call(t, http.MethodPost, w.secretary, "/societies/"+w.c.s.id+"/members", b2), http.StatusForbidden, "step_up_required", "a registration on no real proof")
}

func TestApiMembers06_OneMemberRefusedSaysWhy(t *testing.T) {
	w := newMoneyAPI(t)
	b := aRegistrant(seq.Add(1))
	b["branchCode"] = "NOWHERE"
	wantProblem(t, w.registerOne(t, w.secretary, b), http.StatusUnprocessableEntity, "branch_unknown", "a member in a branch the society does not have")
	b = aRegistrant(seq.Add(1))
	b["memberNumber"] = memberByNumber(t, w.bMem)
	wantProblem(t, w.registerOne(t, w.secretary, b), http.StatusUnprocessableEntity, "number_already_registered", "a number already in the register")
}

// =============================================================================
// THE IMPORT
// =============================================================================

type refusedLine struct {
	Line int
	Code string
}

func TestApiMembers07_AnImportIsAllOrNothingAndSaysEveryRefusedLine(t *testing.T) {
	w := newMoneyAPI(t)
	before := countMembers(t, w.c.s.id)
	good, badPhone, badBranch, dup := aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1))
	badPhone["phone"] = "12345"
	badBranch["branchCode"] = "NOWHERE"
	dup["memberNumber"] = good["memberNumber"]
	r := w.importRegister(t, w.secretary, "REG-"+digest()[:16], []regBody{good, badPhone, badBranch, dup})
	var ref struct {
		Code string
		Rows []refusedLine
	}
	if r.status != http.StatusUnprocessableEntity || json.Unmarshal(r.body, &ref) != nil || ref.Code != "rows_refused" {
		t.Fatalf("WRONG — an import with three bad lines: %d %s; want 422 rows_refused", r.status, r.body)
	}
	want := map[int]string{3: "phone_not_nigerian", 4: "branch_unknown", 5: "number_repeated_in_file"}
	got := map[int]string{}
	for _, l := range ref.Rows {
		got[l.Line] = l.Code
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("NOT EVERY LINE — refused %v; want %v, every bad line at once", got, want)
	}
	if n := countMembers(t, w.c.s.id); n != before {
		t.Fatalf("PARTIAL — a refused import registered %d member(s): all or nothing", n-before)
	}
}

type registered struct {
	Members []struct {
		Line         int
		MemberId     string
		MemberNumber string
	}
}

func TestApiMembers08_AnImportRegistersEveryRowOnceAndAnswersTheirIds(t *testing.T) {
	w := newMoneyAPI(t)
	key := "REG-" + digest()[:16]
	rows := []regBody{aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1))}
	delete(rows[2], "nextOfKin") // an old ledger book: no next of kin
	before := countMembers(t, w.c.s.id)
	r := w.importRegister(t, w.secretary, key, rows)
	var first registered
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &first) != nil || len(first.Members) != 3 {
		t.Fatalf("REFUSED — a clean register of three: %d %s", r.status, r.body)
	}
	for i, m := range first.Members {
		if m.Line != i+2 || m.MemberNumber != rows[i]["memberNumber"] || memberByNumber(t, m.MemberId) != m.MemberNumber {
			t.Fatalf("WRONG MAP — %+v; want line %d → %s", m, i+2, rows[i]["memberNumber"])
		}
	}
	again := w.importRegister(t, w.secretary, key, []regBody{aRegistrant(seq.Add(1))})
	var second registered
	if again.status != http.StatusOK || json.Unmarshal(again.body, &second) != nil || fmt.Sprint(second.Members) != fmt.Sprint(first.Members) {
		t.Fatalf("NOT THE FIRST COMMIT — a repeat answered %d %s; want 200 with the first commit's map", again.status, again.body)
	}
	if n := countMembers(t, w.c.s.id); n != before+3 {
		t.Fatalf("TWICE — %d members registered by one reference sent twice; want 3", n-before)
	}
}

// From one file: the secretary's register, then the treasurer's balances by
// the members' own numbers.
func TestApiMembers09_TheTreasurerPostsBalancesByMemberNumber(t *testing.T) {
	w := newMoneyAPI(t)
	rows := []regBody{aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1))}
	if r := w.importRegister(t, w.secretary, "REG-"+digest()[:16], rows); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the register: %d %s", r.status, r.body)
	}
	bal := []map[string]any{{"line": 2, "memberNumber": rows[0]["memberNumber"], "amountKobo": "4800000"}, {"line": 3, "memberNumber": rows[1]["memberNumber"], "amountKobo": "250000"}}
	if r := w.postBalances(t, w.poster, bal); r.status != http.StatusCreated {
		t.Fatalf("REFUSED — the treasurer's balances by member number: %d %s", r.status, r.body)
	}
	_, got := w.find(t, w.poster, rows[0]["memberNumber"].(string))
	if len(got) != 1 || !got[0].OpeningBalancePosted {
		t.Fatalf("NOT POSTED — %+v", got)
	}
	wantProblem(t, w.postBalances(t, w.secretary, []map[string]any{{"line": 2, "memberNumber": memberByNumber(t, w.g2Mem), "amountKobo": "100"}}),
		http.StatusForbidden, "not_permitted", "the secretary posting opening balances")
}

// A number is this society's: another society's member with the same number
// is not credited here.
func TestApiMembers10_AMemberNumberIsResolvedInThisSocietyOnly(t *testing.T) {
	w := newMoneyAPI(t)
	theirs := memberByNumber(t, w.stranger)
	r := w.postBalances(t, w.poster, []map[string]any{{"line": 2, "memberNumber": theirs, "amountKobo": "100000"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), `"unknown_member"`) {
		t.Fatalf("CREDITED ELSEWHERE — another society's member number: %d %s; want 422 unknown_member", r.status, r.body)
	}
}
