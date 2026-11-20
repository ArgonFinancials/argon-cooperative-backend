//go:build tier1

// ACCEPTANCE TESTS — OFFICERS ENROL BY EMAIL, OVER HTTP (0034, 2026-11-20).
// The decision: an officer's enrolment code is emailed to an address recorded
// on the member, so the issuing officer never sees it. One-time, expiring,
// voided on reissue and after five wrong tries, a first PIN only: the rules
// of every code (0024, 0026). The slip stays for members with no email.
//
//	PUT /societies/{s}/members/{m}/email: the current secretary, on their own
//	step-up (member_email); recorded with who and when.
//	POST .../enrolment-codes {delivery: email}: the code goes to the member's
//	address and NOT into the answer; the officer who set that address may not
//	email it; a signatory's halves both go by email or both by slip.
//	A send that fails issues nothing (502) and the failure is recorded; a
//	server with no sender says so (503), and the history says email is off.
//	Every refusal is checked before anything is written, so the officer's
//	proof is not spent by one.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func putJSON(t *testing.T, srv *httptest.Server, path, token string, body any) resp {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "PUT "+path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b}
}

func (w *enrolAPI) setEmail(t *testing.T, as signer, member string, addr any) resp {
	t.Helper()
	return putJSON(t, w.srv, "/societies/"+w.s.id+"/members/"+member+"/email", signIn(t, as.user),
		map[string]any{"email": addr, "pinProofId": stepUp(t, as.user, "member_email")})
}

// issueBy: `as` issues `member` a code by `delivery` on the proof given ("" takes a fresh one).
func (w *enrolAPI) issueBy(t *testing.T, as signer, member, delivery, proof string) resp {
	t.Helper()
	if proof == "" {
		proof = stepUp(t, as.user, "enrolment_issue")
	}
	return postJSON(t, w.srv, w.codesPath(w.s, member), signIn(t, as.user), map[string]string{"pinProofId": proof, "delivery": delivery})
}

type emailedOut struct {
	CodeID   string  `json:"codeId"`
	Kind     string  `json:"kind"`
	Delivery string  `json:"delivery"`
	Slip     *string `json:"slip"`
	SentTo   *string `json:"sentTo"`
	Complete bool    `json:"complete"`
}

func emailedOf(t *testing.T, r resp, what string) emailedOut {
	t.Helper()
	var o emailedOut
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &o) != nil {
		t.Fatalf("REFUSED — %s: %d %s", what, r.status, r.body)
	}
	return o
}

var codeInMail = regexp.MustCompile(`[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4})?`)

// mailedCode: the code in the newest message to addr; fails if none arrived.
func mailedCode(t *testing.T, addr string, want int) string {
	t.Helper()
	got := testMail.to(addr)
	if len(got) != want {
		t.Fatalf("NOT SENT — %d message(s) to the member's address; want %d", len(got), want)
	}
	c := codeInMail.FindString(got[len(got)-1].Text)
	if c == "" {
		t.Fatalf("NO CODE — the message to the member carries no code: %q", got[len(got)-1].Text)
	}
	return c
}

func sends(t *testing.T, member, outcome string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM email_sends WHERE member_id = $1 AND outcome = $2`, member, outcome).Scan(&n), "the sends")
	return n
}

func codesFor(t *testing.T, member string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM enrolment_codes WHERE member_id = $1`, member).Scan(&n), "the codes")
	return n
}

// withEmail: the secretary records an address for member; returns it.
func (w *enrolAPI) withEmail(t *testing.T, member string) string {
	t.Helper()
	addr := uniqueEmail("officer")
	if r := w.setEmail(t, w.sec, member, addr); r.status != http.StatusOK {
		t.Fatalf("FIXTURE: the secretary setting the email: %d %s", r.status, r.body)
	}
	return addr
}

// =============================================================================
// THE ADDRESS
// =============================================================================

// The secretary sets it on their own step-up; the summary says who and when;
// any other officer is refused; a bad address is told, not a 500.
func TestApiEmail01_TheSecretarySetsTheAddressAndTheSummarySaysWho(t *testing.T) {
	w := newEnrolAPI(t)
	addr := uniqueEmail("Officer")
	r := w.setEmail(t, w.sec, w.newcomer.member, addr)
	var m struct {
		Email      *string
		EmailSetBy *struct{ UserId, Name string }
		EmailSetAt *string
	}
	if r.status != http.StatusOK || json.Unmarshal(r.body, &m) != nil {
		t.Fatalf("REFUSED — the secretary setting a member's email: %d %s", r.status, r.body)
	}
	switch {
	case m.Email == nil || *m.Email != strings.ToLower(addr):
		t.Fatalf("WRONG ADDRESS — the summary reads %v; want %q, lower-cased", m.Email, strings.ToLower(addr))
	case m.EmailSetBy == nil || m.EmailSetBy.UserId != w.sec.user || m.EmailSetBy.Name != "Secretary" || m.EmailSetAt == nil:
		t.Fatalf("UNSAID — who set the email: %s; want the secretary, and when", r.body)
	}
	wantProblem(t, w.setEmail(t, w.treas, w.newcomer.member, uniqueEmail("x")), http.StatusForbidden, "not_permitted", "the treasurer setting a member's email")
	wantProblem(t, w.setEmail(t, w.sec, w.newcomer.member, "two@example.com, three@example.com"), http.StatusUnprocessableEntity, "bad_email", "two addresses in one")
	wantProblem(t, putJSON(t, w.srv, "/societies/"+w.s.id+"/members/"+w.newcomer.member+"/email", signIn(t, w.sec.user),
		map[string]any{"email": uniqueEmail("x"), "pinProofId": stepUp(t, w.sec.user, "enrolment_issue")}), http.StatusForbidden, "step_up_required", "an email set on a proof for another act")
	if r := w.setEmail(t, w.elsewhere, w.newcomer.member, uniqueEmail("x")); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's secretary setting an email here: %d %s; want 404", r.status, r.body)
	}
}

// =============================================================================
// ISSUING BY EMAIL
// =============================================================================

// A whole code by email: the answer carries no code and a masked address; the
// member's inbox has the code, and it enrols them; the send is recorded.
func TestApiEmail02_TheCodeGoesToTheMemberAndNotToTheOfficer(t *testing.T) {
	w := newEnrolAPI(t)
	addr := w.withEmail(t, w.newcomer.member)
	r := w.issueBy(t, w.treas, w.newcomer.member, "email", "")
	o := emailedOf(t, r, "the treasurer emailing a member's code")
	code := mailedCode(t, strings.ToLower(addr), 1)
	switch {
	case o.Slip != nil || strings.Contains(string(r.body), code) || strings.Contains(string(r.body), strings.ReplaceAll(code, "-", "")):
		t.Fatalf("SEEN — the issuing officer's answer carries the emailed code: %s", r.body)
	case o.Delivery != "email" || o.Kind != "whole" || !o.Complete:
		t.Fatalf("WRONG — %+v; want a complete whole code, delivered by email", o)
	case o.SentTo == nil || strings.Contains(*o.SentTo, strings.Split(strings.ToLower(addr), "@")[0]) || !strings.HasSuffix(*o.SentTo, "@example.com"):
		t.Fatalf("UNMASKED — sentTo reads %v; want the domain and no more than a letter of the name", o.SentTo)
	}
	if n := sends(t, w.newcomer.member, "sent"); n != 1 {
		t.Fatalf("UNRECORDED — %d sent row(s); want one", n)
	}
	if e := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": code, "newPin": enrolNewPIN}); e.status != http.StatusOK {
		t.Fatalf("REFUSED — the member enrolling with the code from their inbox: %d %s", e.status, e.body)
	}
}

// Separation of duties: whoever set the address does not email the code to it.
// The refusal spends nothing: the same proof then issues a slip.
func TestApiEmail03_TheOfficerWhoSetTheAddressDoesNotEmailTheCode(t *testing.T) {
	w := newEnrolAPI(t)
	addr := w.withEmail(t, w.newcomer.member)
	proof := stepUp(t, w.sec.user, "enrolment_issue")
	wantProblem(t, w.issueBy(t, w.sec, w.newcomer.member, "email", proof), http.StatusConflict, "email_set_by_you", "the secretary emailing to the address they set")
	if n := len(testMail.to(strings.ToLower(addr))); n != 0 || codesFor(t, w.newcomer.member) != 0 {
		t.Fatalf("ISSUED — the refused request sent %d message(s) or left a code", n)
	}
	if r := w.issueBy(t, w.sec, w.newcomer.member, "slip", proof); r.status != http.StatusCreated {
		t.Fatalf("SPENT — the same proof could not then issue a slip: %d %s; a refusal must spend nothing", r.status, r.body)
	}
}

// No address recorded: told so (the slip is the way), nothing issued.
func TestApiEmail04_NoAddressIsToldAndNothingIsIssued(t *testing.T) {
	w := newEnrolAPI(t)
	wantProblem(t, w.issueBy(t, w.treas, w.newcomer.member, "email", ""), http.StatusUnprocessableEntity, "no_email", "emailing a member with no address")
	if n := codesFor(t, w.newcomer.member); n != 0 {
		t.Fatalf("ISSUED — %d code(s) for a member with no address", n)
	}
}

// A send that fails issues nothing: no code exists that nobody received, and
// the failure is on record.
func TestApiEmail05_ASendThatFailsIssuesNothingAndIsRecorded(t *testing.T) {
	w := newEnrolAPI(t)
	w.withEmail(t, w.newcomer.member)
	testMail.failing(t)
	wantProblem(t, w.issueBy(t, w.treas, w.newcomer.member, "email", ""), http.StatusBadGateway, "email_not_sent", "a send the provider refused")
	if n := codesFor(t, w.newcomer.member); n != 0 {
		t.Fatalf("ISSUED — %d code(s) exist that nobody was sent", n)
	}
	if n := sends(t, w.newcomer.member, "failed"); n != 1 {
		t.Fatalf("UNRECORDED — %d failed row(s); want one", n)
	}
}

// A server with no sender: issuing by email says so (503), the slip still
// works, and the history tells the screen email is off.
func TestApiEmail06_NoSenderSaysSoAndTheSlipStillWorks(t *testing.T) {
	w := newEnrolAPI(t)
	w.withEmail(t, w.newcomer.member)
	on := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.treas.user))
	w.srv = apiServerNoEmail(t)
	off := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.treas.user))
	var a, b struct{ EmailDelivery *bool }
	json.Unmarshal(on.body, &a)
	json.Unmarshal(off.body, &b)
	if a.EmailDelivery == nil || !*a.EmailDelivery || b.EmailDelivery == nil || *b.EmailDelivery {
		t.Fatalf("UNSAID — emailDelivery reads %s with a sender and %s without; want true, then false", on.body, off.body)
	}
	wantProblem(t, w.issueBy(t, w.treas, w.newcomer.member, "email", ""), http.StatusServiceUnavailable, "email_unavailable", "emailing on a server with no sender")
	slipOf(t, w.issueBy(t, w.treas, w.newcomer.member, "slip", ""), "a slip on a server with no sender")
}

// A signatory's code by email: two signatories, a half each, both to the
// member's inbox; neither answer carries a half; a second half by the other
// way is refused; the two halves enrol the member.
func TestApiEmail07_ASignatorysHalvesBothGoByEmail(t *testing.T) {
	w := newEnrolAPI(t)
	addr := strings.ToLower(w.withEmail(t, w.branch.member))
	a := emailedOf(t, w.issueBy(t, w.chair, w.branch.member, "email", ""), "the chairman's half by email")
	first := mailedCode(t, addr, 1)
	wantProblem(t, w.issueBy(t, w.treas, w.branch.member, "slip", ""), http.StatusConflict, "delivery_mismatch", "a second half by slip after an emailed first")
	rb := w.issueBy(t, w.treas, w.branch.member, "email", "")
	b := emailedOf(t, rb, "the treasurer's half by email")
	second := mailedCode(t, addr, 2)
	switch {
	case a.Kind != "first_half" || b.Kind != "second_half" || b.CodeID != a.CodeID || !b.Complete:
		t.Fatalf("WRONG HALVES — %+v / %+v", a, b)
	case a.Slip != nil || b.Slip != nil || strings.Contains(string(rb.body), first) || strings.Contains(string(rb.body), second):
		t.Fatalf("SEEN — a signatory's answer carries a half")
	}
	if r := w.enrol(t, map[string]string{"credential": w.branch.number, "code": second + " " + first, "newPin": enrolNewPIN}); r.status != http.StatusOK {
		t.Fatalf("REFUSED — the signatory enrolling with both emailed halves: %d %s", r.status, r.body)
	}
}

// The history says how each code went and to where, masked; never a code.
func TestApiEmail08_TheHistorySaysHowEachCodeWent(t *testing.T) {
	w := newEnrolAPI(t)
	addr := strings.ToLower(w.withEmail(t, w.newcomer.member))
	slipOf(t, w.issueBy(t, w.treas, w.newcomer.member, "slip", ""), "a slip")
	emailedOf(t, w.issueBy(t, w.chair, w.newcomer.member, "email", ""), "the reissue by email")
	code := mailedCode(t, addr, 1)
	h := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.sec.user))
	var out struct {
		Codes []struct {
			Delivery string
			SentTo   *string
		}
	}
	if h.status != http.StatusOK || json.Unmarshal(h.body, &out) != nil || len(out.Codes) != 2 {
		t.Fatalf("WRONG HISTORY — %d %s", h.status, h.body)
	}
	switch {
	case out.Codes[0].Delivery != "email" || out.Codes[0].SentTo == nil || out.Codes[1].Delivery != "slip" || out.Codes[1].SentTo != nil:
		t.Fatalf("UNSAID — the history reads %s; want the email (newest, with where) then the slip", h.body)
	case strings.Contains(string(h.body), addr):
		t.Fatalf("UNMASKED — the history carries the whole address: %s", h.body)
	case strings.Contains(string(h.body), code):
		t.Fatalf("SHOWN — the history carries the emailed code")
	}
}

// =============================================================================
// THE REGISTER
// =============================================================================

// An address given at registration is recorded, by the registering secretary;
// a bad one is that row's refusal.
func TestApiEmail09_TheRegisterTakesAnAddress(t *testing.T) {
	w := newMoneyAPI(t)
	b := aRegistrant(seq.Add(1))
	addr := uniqueEmail("Registered")
	b["email"] = addr
	r := w.registerOne(t, w.secretary, b)
	var m struct {
		Email      *string
		EmailSetBy *struct{ UserId string }
	}
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &m) != nil {
		t.Fatalf("REFUSED — registering a member with an email: %d %s", r.status, r.body)
	}
	if m.Email == nil || *m.Email != strings.ToLower(addr) || m.EmailSetBy == nil || m.EmailSetBy.UserId != w.secretary {
		t.Fatalf("DROPPED — the registered member reads %s; want the address, set by the secretary", r.body)
	}
	good, bad := aRegistrant(seq.Add(1)), aRegistrant(seq.Add(1))
	bad["email"] = "not-an-address"
	ri := w.importRegister(t, w.secretary, "REG-"+digest()[:16], []regBody{good, bad})
	var ref struct {
		Code string
		Rows []refusedLine
	}
	if ri.status != http.StatusUnprocessableEntity || json.Unmarshal(ri.body, &ref) != nil || len(ref.Rows) != 1 || ref.Rows[0].Line != 3 || ref.Rows[0].Code != "bad_email" {
		t.Fatalf("WRONG — an import with one bad address: %d %s; want line 3 refused, bad_email", ri.status, ri.body)
	}
}

// How email is sent is said, never inherited: unset refuses to start; ses
// needs its sender address and region.
func TestApiEmail10_TheConfigurationNamesTheSender(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://nobody@127.0.0.1:1/none")
	t.Setenv("ARGON_CSRF_KEY", strings.Repeat("ab", 32))
	t.Setenv("AUTH_DATABASE_URL", "")
	t.Setenv("AUTH_CODE_KEY", "")
	t.Setenv("ARGON_VFD_WEBHOOK", "off")
	t.Setenv("ARGON_SMS", "refusing")
	for _, c := range []struct{ mode, from, region, what string }{
		{"", "", "", "no ARGON_EMAIL"}, {"smtp", "", "", "an unknown sender"},
		{"ses", "", "eu-west-1", "ses with no EMAIL_FROM"}, {"ses", "codes@example.com", "", "ses with no AWS_REGION"},
	} {
		t.Setenv("ARGON_EMAIL", c.mode)
		t.Setenv("EMAIL_FROM", c.from)
		t.Setenv("AWS_REGION", c.region)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "EMAIL") && !strings.Contains(err.Error(), "AWS_REGION") {
			t.Fatalf("STARTED — %s: the configuration loaded (%v); it must refuse, naming what is missing", c.what, err)
		}
	}
	t.Setenv("ARGON_EMAIL", "ses")
	t.Setenv("EMAIL_FROM", "codes@example.com")
	t.Setenv("AWS_REGION", "eu-west-1")
	cfg, err := loadConfig()
	if err != nil || cfg.Email != "ses" || cfg.EmailFrom != "codes@example.com" || cfg.AWSRegion != "eu-west-1" {
		t.Fatalf("REFUSED — ses with its address and region: %v (%q)", err, cfg.Email)
	}
	t.Setenv("ARGON_EMAIL", "off")
	if cfg, err = loadConfig(); err != nil || cfg.Email != "off" {
		t.Fatalf("REFUSED — off: %v", err)
	}
}
