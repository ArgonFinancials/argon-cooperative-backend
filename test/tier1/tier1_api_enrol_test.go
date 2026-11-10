//go:build tier1

// ACCEPTANCE TESTS — ENROLMENT OVER HTTP (stage 1, 2026-11-10). Written before
// the handlers (internal/api/enrolment.go: stubs until these are red).
//
//   POST /auth/enrol: public. The auth service's rules (Enrol, suite enrol)
//   carried to HTTP without undoing any: one refusal for an unknown number,
//   a wrong PIN and a wrong code; the used slip told to its holder in the
//   approved words; a browser's session and new device in cookies.
//
//   POST /societies/{s}/members/{m}/enrolment-codes: an officer issues a
//   code on their own step-up (enrolment_issue). The slip is SHOWN ONCE, to
//   its issuer. A signatory's code comes in two halves from two other
//   current signatories (0026), and neither ever sees the other's half.
//
//   GET the same path: who issued each code, when, and the device it
//   trusted, for an officer of the society. Never a code or a digest. This
//   is what makes the accepted residual (a slip is a bearer credential,
//   §12) recoverable.
package tier1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

type enrolAPI struct {
	srv                  *httptest.Server
	s                    *soc
	chair, treas, sec    signer // two signatories and an officer who does not sign
	auditor              signer
	branch               signer // a signatory who needs a code (no device yet)
	newcomer             enrollee
	elsewhere            signer // the secretary of another society
}

func newEnrolAPI(t *testing.T) *enrolAPI {
	t.Helper()
	s, o := newSociety(t), newSociety(t)
	w := &enrolAPI{srv: apiServer(t), s: s,
		chair:     newSigner(t, s, "Chairman", "chairman", true),
		treas:     newSigner(t, s, "Treasurer", "treasurer", true),
		sec:       newSigner(t, s, "Secretary", "secretary", false),
		auditor:   newSigner(t, s, "Auditor", "internal_auditor", false),
		elsewhere: newSigner(t, o, "Secretary elsewhere", "secretary", false),
	}
	// A signatory who needs a code: a member record and a signing post, no
	// PIN and no device yet.
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Branch officer') RETURNING id`).Scan(&w.branch.user), "the branch officer")
	w.branch.member = s.member(w.branch.user)
	fixture(t, db.QueryRow(bg(), `SELECT member_number FROM members WHERE id = $1`, w.branch.member).Scan(&w.branch.number), "member number")
	fixture(t, s.appoint(w.branch.member, "branch_officer", true, "2020-01-01", ""), "a signing post")
	var u, m, n string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Member (new)') RETURNING id`).Scan(&u), "a newcomer")
	m = s.member(u)
	fixture(t, db.QueryRow(bg(), `SELECT member_number FROM members WHERE id = $1`, m).Scan(&n), "member number")
	w.newcomer = enrollee{user: u, member: m, number: n}
	return w
}

func (w *enrolAPI) codesPath(society *soc, member string) string {
	return "/societies/" + society.id + "/members/" + member + "/enrolment-codes"
}

// issue: `as` takes their own step-up for enrolment_issue and issues `member` a code.
func (w *enrolAPI) issue(t *testing.T, as signer, society *soc, member string) resp {
	t.Helper()
	return postJSON(t, w.srv, w.codesPath(society, member), signIn(t, as.user), map[string]string{"pinProofId": stepUp(t, as.user, "enrolment_issue")})
}

type slipOut struct {
	CodeID    string `json:"codeId"`
	Kind      string `json:"kind"`
	Slip      string `json:"slip"`
	ExpiresAt string `json:"expiresAt"`
	Complete  bool   `json:"complete"`
}

func slipOf(t *testing.T, r resp, what string) slipOut {
	t.Helper()
	if r.status != http.StatusCreated {
		t.Fatalf("REFUSED — %s: %d %s", what, r.status, r.body)
	}
	var s slipOut
	if err := json.Unmarshal(r.body, &s); err != nil {
		t.Fatalf("WRONG SHAPE — %s: %s", what, r.body)
	}
	return s
}

func problemCode(t *testing.T, r resp) string {
	t.Helper()
	var p struct{ Code, Message string }
	json.Unmarshal(r.body, &p)
	return p.Code
}

func (w *enrolAPI) enrol(t *testing.T, body map[string]string) resp {
	t.Helper()
	return postJSON(t, w.srv, "/auth/enrol", "", body)
}

var wholeSlip = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)
var halfSlip = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

// =============================================================================
// ISSUING
// =============================================================================

// A member who does not sign: one officer, one whole slip, and the member
// enrols with it and the PIN they choose.
func TestApiEnrol01_AnOfficerIssuesAWholeSlipAndTheMemberEnrols(t *testing.T) {
	w := newEnrolAPI(t)
	s := slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the secretary issuing a member's code")
	if s.Kind != "whole" || !s.Complete || !wholeSlip.MatchString(s.Slip) {
		t.Fatalf("WRONG SLIP — %+v; want a complete whole code, XXXX-XXXX-XXXX-XXXX", s)
	}
	r := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": s.Slip, "newPin": enrolNewPIN})
	if r.status != http.StatusOK {
		t.Fatalf("REFUSED — the member enrolling with the slip they were handed: %d %s", r.status, r.body)
	}
	var out signInOut
	json.Unmarshal(r.body, &out)
	if out.Outcome != "signed_in" || out.SessionToken == nil || out.DeviceToken == nil {
		t.Fatalf("NOT SIGNED IN — %s", r.body)
	}
}

// The slip is shown once: nothing else ever returns it.
func TestApiEnrol02_TheSlipIsShownOnce(t *testing.T) {
	w := newEnrolAPI(t)
	s := slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the secretary issuing a member's code")
	h := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.sec.user))
	if h.status != http.StatusOK {
		t.Fatalf("REFUSED — the officer reading the code history: %d %s", h.status, h.body)
	}
	plain := strings.ReplaceAll(s.Slip, "-", "")
	if strings.Contains(string(h.body), s.Slip) || strings.Contains(string(h.body), plain) || strings.Contains(string(h.body), s.Slip[:9]) {
		t.Fatalf("SHOWN TWICE — the code history carries the slip: %s", h.body)
	}
	if regexp.MustCompile(`[0-9a-f]{64}`).Match(h.body) {
		t.Fatalf("DIGEST SHOWN — the code history carries a digest: %s", h.body)
	}
}

// A signatory's code: two signatories, one half each; neither half is in the
// other's answer; the member types both and enrols.
func TestApiEnrol03_ASignatorysCodeIsTwoHalvesFromTwoSignatories(t *testing.T) {
	w := newEnrolAPI(t)
	a := slipOf(t, w.issue(t, w.chair, w.s, w.branch.member), "the chairman's half")
	if a.Kind != "first_half" || a.Complete || !halfSlip.MatchString(a.Slip) {
		t.Fatalf("WRONG HALF — the first signatory got %+v; want first_half, incomplete, XXXX-XXXX", a)
	}
	rb := w.issue(t, w.treas, w.s, w.branch.member)
	b := slipOf(t, rb, "the treasurer's half")
	if b.Kind != "second_half" || !b.Complete || !halfSlip.MatchString(b.Slip) || b.CodeID != a.CodeID {
		t.Fatalf("WRONG HALF — the second signatory got %+v; want second_half of the same code, complete", b)
	}
	if strings.Contains(string(rb.body), a.Slip) || strings.Contains(string(rb.body), strings.ReplaceAll(a.Slip, "-", "")) {
		t.Fatalf("SEEN — the second signatory's answer carries the first signatory's half")
	}
	r := w.enrol(t, map[string]string{"credential": w.branch.number, "code": b.Slip + " " + a.Slip, "newPin": enrolNewPIN})
	if r.status != http.StatusOK {
		t.Fatalf("REFUSED — the signatory enrolling with both halves: %d %s", r.status, r.body)
	}
}

// One signatory cannot issue both halves; they are told a second one must.
func TestApiEnrol04_TheSameSignatoryCannotIssueTheOtherHalf(t *testing.T) {
	w := newEnrolAPI(t)
	slipOf(t, w.issue(t, w.chair, w.s, w.branch.member), "the chairman's half")
	r := w.issue(t, w.chair, w.s, w.branch.member)
	if r.status != http.StatusConflict || problemCode(t, r) != "second_signatory_needed" {
		t.Fatalf("WRONG ANSWER — the chairman asking for the other half: %d %s; want 409 second_signatory_needed", r.status, r.body)
	}
}

// Who may not: the auditor; an officer who does not sign, for a signatory;
// anyone for themselves; anyone without their own step-up.
func TestApiEnrol05_WhoMayNotIssue(t *testing.T) {
	w := newEnrolAPI(t)
	for _, c := range []struct {
		as     signer
		member string
		status int
		code   string
		what   string
	}{
		{w.auditor, w.newcomer.member, http.StatusForbidden, "not_permitted", "the auditor issuing a code"},
		{w.sec, w.branch.member, http.StatusForbidden, "not_permitted", "the secretary (who does not sign) issuing a signatory's code"},
		{w.sec, w.sec.member, http.StatusForbidden, "conflict_of_interest", "the secretary issuing their own code"},
	} {
		r := w.issue(t, c.as, w.s, c.member)
		if r.status != c.status || problemCode(t, r) != c.code {
			t.Fatalf("WRONG ANSWER — %s: %d %s; want %d %s", c.what, r.status, r.body, c.status, c.code)
		}
	}
	r := postJSON(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.sec.user), map[string]string{"pinProofId": stepUp(t, w.sec.user, "lock_release")})
	if r.status != http.StatusForbidden || problemCode(t, r) != "step_up_required" {
		t.Fatalf("WRONG ANSWER — a step-up for another act: %d %s; want 403 step_up_required", r.status, r.body)
	}
}

// Someone who is not an officer here, or a member who is not this
// society's, is told nothing is there; and nothing is issued.
func TestApiEnrol06_SomeoneWhoIsNotAnOfficerHereIsToldNothing(t *testing.T) {
	w := newEnrolAPI(t)
	if r := w.issue(t, w.elsewhere, w.s, w.newcomer.member); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's secretary issuing here: %d %s; want 404", r.status, r.body)
	}
	if r := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.elsewhere.user)); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's secretary reading the code history: %d %s; want 404", r.status, r.body)
	}
	// The caller IS an officer here; the member is another society's.
	if r := w.issue(t, w.sec, w.s, w.elsewhere.member); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's member named under this society: %d %s; want 404", r.status, r.body)
	}
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM enrolment_codes WHERE user_id = $1`, w.newcomer.user).Scan(&n), "codes")
	if n != 0 {
		t.Fatalf("ISSUED — %d code(s) for the member from refused requests", n)
	}
}

// The history: who issued, when, used when and on what device; a reissue
// voids the old one and says so.
func TestApiEnrol07_TheHistoryNamesIssuerTimeAndDevice(t *testing.T) {
	w := newEnrolAPI(t)
	first := slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the first code")
	second := slipOf(t, w.issue(t, w.treas, w.s, w.newcomer.member), "the reissue")
	if r := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": second.Slip, "newPin": enrolNewPIN}); r.status != http.StatusOK {
		t.Fatalf("FIXTURE: the enrolment: %d %s", r.status, r.body)
	}
	h := get(t, w.srv, w.codesPath(w.s, w.newcomer.member), signIn(t, w.chair.user))
	var out struct {
		Codes []struct {
			CodeID, IssuedBy, State, VoidReason, UsedAt, UsedDeviceID, Replaces string
		} `json:"codes"`
	}
	if h.status != http.StatusOK || json.Unmarshal(h.body, &out) != nil || len(out.Codes) != 2 {
		t.Fatalf("WRONG HISTORY — %d %s; want two codes", h.status, h.body)
	}
	newest, old := out.Codes[0], out.Codes[1]
	switch {
	case newest.CodeID != second.CodeID || old.CodeID != first.CodeID:
		t.Fatalf("ORDER — the history is not newest first: %s", h.body)
	case newest.IssuedBy != "Treasurer" || old.IssuedBy != "Secretary":
		t.Fatalf("UNNAMED — the issuers read %q and %q; want Treasurer and Secretary", newest.IssuedBy, old.IssuedBy)
	case newest.State != "used" || newest.UsedAt == "" || newest.UsedDeviceID == "":
		t.Fatalf("NO DEVICE — the used code reads %+v; want used, with when and the device", newest)
	case old.State != "voided" || old.VoidReason != "reissued" || newest.Replaces != first.CodeID:
		t.Fatalf("REISSUE UNSAID — %+v / %+v", newest, old)
	}
}

// =============================================================================
// REDEEMING
// =============================================================================

// No session is needed; one refusal for an unknown number and a wrong code.
func TestApiEnrol08_OneRefusalForEveryWrongThing(t *testing.T) {
	w := newEnrolAPI(t)
	slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the member's code")
	wrong := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": "AAAA-AAAA-AAAA-AAAA", "newPin": enrolNewPIN})
	unknown := w.enrol(t, map[string]string{"credential": unregistered(), "code": "AAAA-AAAA-AAAA-AAAA", "newPin": enrolNewPIN})
	if wrong.status != http.StatusUnauthorized || problemCode(t, wrong) != "refused" {
		t.Fatalf("WRONG ANSWER — a wrong code: %d %s; want 401 refused", wrong.status, wrong.body)
	}
	if unknown.status != wrong.status || string(unknown.body) != string(wrong.body) {
		t.Fatalf("TELLS — an unknown number answered %d %s, a wrong code %d %s", unknown.status, unknown.body, wrong.status, wrong.body)
	}
}

// Each refusal the member can act on, in words they can act on; the code
// stays live through every one.
func TestApiEnrol09_EachRefusalSaysWhatToDo(t *testing.T) {
	w := newEnrolAPI(t)
	s := slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the member's code")
	for _, c := range []struct {
		body   map[string]string
		status int
		code   string
	}{
		{map[string]string{"credential": w.newcomer.number, "code": s.Slip}, http.StatusUnprocessableEntity, "needs_new_pin"},
		{map[string]string{"credential": w.newcomer.number, "code": s.Slip, "newPin": enrolBadPIN}, http.StatusUnprocessableEntity, "pin_not_allowed"},
	} {
		if r := w.enrol(t, c.body); r.status != c.status || problemCode(t, r) != c.code {
			t.Fatalf("WRONG ANSWER — %v: %d %s; want %d %s", c.body, r.status, r.body, c.status, c.code)
		}
	}
	holder := newSigner(t, w.s, "Member (new phone)", "", false)
	hs := slipOf(t, w.issue(t, w.sec, w.s, holder.member), "a holder's code")
	if r := w.enrol(t, map[string]string{"credential": holder.number, "code": hs.Slip, "pin": signPIN, "newPin": enrolNewPIN}); r.status != http.StatusUnprocessableEntity || problemCode(t, r) != "pin_already_set" {
		t.Fatalf("WRONG ANSWER — a holder asking for a new PIN: %d %s; want 422 pin_already_set", r.status, r.body)
	}
	if r := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": s.Slip, "newPin": enrolNewPIN}); r.status != http.StatusOK {
		t.Fatalf("SLIP LOST — after the refusals, the slip answered %d %s", r.status, r.body)
	}
	used := w.enrol(t, map[string]string{"credential": w.newcomer.number, "code": s.Slip, "pin": enrolNewPIN})
	var p struct{ Code, Message string }
	json.Unmarshal(used.body, &p)
	if used.status != http.StatusConflict || p.Code != "slip_used" || p.Message != "This code has already been used. Speak to your secretary." {
		t.Fatalf("SILENT — the used slip: %d %s; want 409 slip_used, \"This code has already been used. Speak to your secretary.\"", used.status, used.body)
	}
}

// A locked account: 429, as at sign-in.
func TestApiEnrol10_ALockedAccountIsToldToWait(t *testing.T) {
	w := newEnrolAPI(t)
	holder := newSigner(t, w.s, "Member (locked)", "", false)
	s := slipOf(t, w.issue(t, w.sec, w.s, holder.member), "a holder's code")
	for i := 0; i < 5; i++ {
		w.enrol(t, map[string]string{"credential": holder.number, "code": s.Slip, "pin": "0000"})
	}
	if r := w.enrol(t, map[string]string{"credential": holder.number, "code": s.Slip, "pin": signPIN}); r.status != http.StatusTooManyRequests || problemCode(t, r) != "try_later" {
		t.Fatalf("LOCK BYPASSED — %d %s; want 429 try_later", r.status, r.body)
	}
}

// A browser: the session and the new device in cookies, the CSRF token in
// the body, no credential in it.
func TestApiEnrol11_ABrowserEnrolsIntoCookies(t *testing.T) {
	w := newEnrolAPI(t)
	s := slipOf(t, w.issue(t, w.sec, w.s, w.newcomer.member), "the member's code")
	a := send(t, w.srv, call{method: http.MethodPost, path: "/auth/enrol", origin: true,
		body: map[string]string{"credential": w.newcomer.number, "code": s.Slip, "newPin": enrolNewPIN}})
	if a.status != http.StatusOK {
		t.Fatalf("REFUSED — a browser's enrolment: %d %s", a.status, a.body)
	}
	if c := a.cookie(sessionCookie); c == nil || c.Value == "" {
		t.Fatalf("NO SESSION COOKIE — %v", a.header.Values("Set-Cookie"))
	}
	if c := a.cookie(deviceCookie); c == nil || c.Value == "" {
		t.Fatalf("NO DEVICE COOKIE — the device this slip trusted is not the browser's: %v", a.header.Values("Set-Cookie"))
	}
	if tok, _ := a.field(t, "sessionToken"); tok != "" {
		t.Fatalf("TOKEN IN BODY — a browser's session token in the body: %s", a.body)
	}
	if tok, _ := a.field(t, "deviceToken"); tok != "" {
		t.Fatalf("TOKEN IN BODY — a browser's device token in the body: %s", a.body)
	}
	if csrf, _ := a.field(t, "csrfToken"); csrf == "" {
		t.Fatalf("NO CSRF TOKEN — %s", a.body)
	}
}
