//go:build tier1

// Acceptance tests for the approval endpoints (PRD 8.4; 0007, 0010) — written
// before the handlers, for review. Every TestApiApprovals* name is here.
//
//	GET  /approvals/{id}               one request, in the caller's projection
//	POST /approvals/{id}/signatures    sign, with the signer's own PIN step-up
//	POST /approvals/{id}/rejections    reject with one of four reasons, with a step-up
//
// Decided: the status is the server's (approval_status), never stored; a
// signature and a rejection each rest on the person's own step-up (0010);
// officers (the auditor included) see who signed and who rejected; the member
// sees progress and why, never who; on the FRAUD PATH (looks_wrong) the member
// sees "referred" and nothing else, because it does not go back to the
// requester, who may be part of it.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type aapi struct {
	*aworld
	srv    *httptest.Server
	other  string // a member of S with no post
	abroad string // an officer of another society
}

func newApprovalAPI(t *testing.T) *aapi {
	t.Helper()
	w := &aapi{aworld: newApprovals(t), srv: apiServer(t)}
	w.other = w.s.user("Another member")
	w.s.member(w.other)
	elsewhere := newSociety(t)
	w.abroad = elsewhere.user("Treasurer elsewhere")
	elsewhere.signatory(w.abroad, "treasurer")
	return w
}

// memberRequest: a 2-of-3 withdrawal for the plain member, expiring in a day
// (or `expires` as SQL).
func (w *aapi) memberRequest(t *testing.T, expires string) string {
	t.Helper()
	if expires == "" {
		expires = `now() + interval '1 day'`
	}
	var id string
	fixture(t, db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at)
		VALUES ($1, $2, $3, 'withdrawal_out', 5000000, 2, `+expires+`) RETURNING id`, w.s.id, "APR-"+digest()[:12], w.pMem).Scan(&id), "a withdrawal request")
	return id
}

func (w *aapi) call(t *testing.T, method, as, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+signIn(t, as))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, method+" "+path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b}
}

func (w *aapi) sign(t *testing.T, as, req, proof string) resp {
	return w.call(t, http.MethodPost, as, "/approvals/"+req+"/signatures", map[string]string{"pinProofId": proof})
}

func (w *aapi) rejectAs(t *testing.T, as, req, proof, reason, note string) resp {
	body := map[string]string{"pinProofId": proof, "reason": reason}
	if note != "" {
		body["note"] = note
	}
	return w.call(t, http.MethodPost, as, "/approvals/"+req+"/rejections", body)
}

type aView struct {
	Projection  string `json:"projection"`
	Status      string `json:"status"`
	SignedCount int    `json:"signedCount"`
	Signatures  *[]struct {
		UserId string `json:"userId"`
		Post   string `json:"post"`
	} `json:"signatures"`
	Rejection *struct {
		Reason   string  `json:"reason"`
		Note     *string `json:"note"`
		ByUserId *string `json:"byUserId"`
	} `json:"rejection"`
	Escalated bool    `json:"escalated"`
	NextStep  *string `json:"nextStep"`
}

func (w *aapi) view(t *testing.T, as, req string) aView {
	t.Helper()
	return ok200[aView](t, w.call(t, http.MethodGet, as, "/approvals/"+req, nil))
}

func wantProblem(t *testing.T, r resp, status int, code, what string) {
	t.Helper()
	var p struct{ Code string }
	if r.status != status || json.Unmarshal(r.body, &p) != nil || p.Code != code {
		t.Fatalf("ACCEPTED — %s: %d %s; want %d %s", what, r.status, r.body, status, code)
	}
}

// signedBy: the request signed by the treasurer through the API (fixture).
func (w *aapi) signedBy(t *testing.T, as, req string) {
	t.Helper()
	if r := w.sign(t, as, req, stepUp(t, as, "approval_signature")); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: signing as a signatory: %d %s", r.status, r.body)
	}
}

// =============================================================================
// WHO SEES WHAT
// =============================================================================

func TestApiApprovals1_AnOfficerSeesWhoSigned(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	w.signedBy(t, w.treasurer, req)
	v := w.view(t, w.chairman, req)
	if v.Projection != "officer" || v.Signatures == nil || len(*v.Signatures) != 1 || (*v.Signatures)[0].UserId != w.treasurer || (*v.Signatures)[0].Post != "treasurer" {
		t.Fatalf("ACCEPTED — the chairman's view does not show the treasurer's signature: %+v", v)
	}
}

func TestApiApprovals2_TheAuditorReadsTheOfficerView(t *testing.T) {
	w := newApprovalAPI(t)
	r := w.call(t, http.MethodGet, w.auditor, "/approvals/"+w.memberRequest(t, ""), nil)
	if v := ok200[aView](t, r); v.Projection != "officer" {
		t.Fatalf("REFUSED — the internal auditor (reads everything) was served %q", v.Projection)
	}
}

func TestApiApprovals3_TheMemberSeesProgressNotNames(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	w.signedBy(t, w.treasurer, req)
	v := w.view(t, w.plain, req)
	if v.Projection != "member" || v.SignedCount != 1 || v.Signatures != nil {
		t.Fatalf("ACCEPTED — the member's view: projection %q, signedCount %d, signatures %v; want member, 1, and no names", v.Projection, v.SignedCount, v.Signatures)
	}
}

func TestApiApprovals4_AnyoneElseGetsNotFound(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	for who, u := range map[string]string{"another member of the society": w.other, "an officer of another society": w.abroad} {
		mine := w.call(t, http.MethodGet, u, "/approvals/"+req, nil)
		none := w.call(t, http.MethodGet, u, "/approvals/"+noSuchRow, nil)
		if mine.status != http.StatusNotFound || string(mine.body) != string(none.body) {
			t.Fatalf("ACCEPTED — %s: %d %s (no such request: %s)", who, mine.status, mine.body, none.body)
		}
	}
}

// =============================================================================
// SIGNING: the signer's own step-up
// =============================================================================

func TestApiApprovals5_SigningNeedsAStepUp(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	wantProblem(t, w.sign(t, w.treasurer, req, noSuchRow), http.StatusForbidden, "step_up_required", "signing with a proof that does not exist")
	if v := w.view(t, w.chairman, req); v.SignedCount != 0 {
		t.Fatalf("ACCEPTED — a signature was recorded without a step-up: %d", v.SignedCount)
	}
}

func TestApiApprovals6_TheAnswerCarriesTheNewStatus(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	w.signedBy(t, w.treasurer, req)
	r := w.sign(t, w.chairman, req, stepUp(t, w.chairman, "approval_signature"))
	if v := ok200or201(t, r); v.Status != "approved" || v.SignedCount != 2 {
		t.Fatalf("ACCEPTED — the second signature's answer reads %q with %d signed; want approved, 2", v.Status, v.SignedCount)
	}
}

func TestApiApprovals7_AProofSignsOnce(t *testing.T) {
	w := newApprovalAPI(t)
	a, b := w.memberRequest(t, ""), w.memberRequest(t, "")
	proof := stepUp(t, w.treasurer, "approval_signature")
	if r := w.sign(t, w.treasurer, a, proof); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the first signature: %d %s", r.status, r.body)
	}
	wantProblem(t, w.sign(t, w.treasurer, b, proof), http.StatusForbidden, "step_up_required", "signing a second request with a spent proof")
	if v := w.view(t, w.chairman, b); v.SignedCount != 0 {
		t.Fatalf("ACCEPTED — a spent proof signed a second request")
	}
}

func TestApiApprovals8_TheAuditorCannotSign(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	wantProblem(t, w.sign(t, w.auditor, req, stepUp(t, w.auditor, "approval_signature")), http.StatusForbidden, "not_a_signatory",
		"the internal auditor signing")
}

func TestApiApprovals9_SigningAnExpiredRequestIsRefused(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, `now() - interval '1 day'`)
	wantProblem(t, w.sign(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_signature")), http.StatusConflict, "request_closed",
		"signing a request that expired yesterday")
}

func TestApiApprovals10_SigningTwiceIsRefused(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	w.signedBy(t, w.treasurer, req)
	wantProblem(t, w.sign(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_signature")), http.StatusConflict, "already_signed",
		"the treasurer signing the same request again")
}

// =============================================================================
// REJECTING: a step-up, one of four reasons, and the fraud path
// =============================================================================

func TestApiApprovals11_RejectingNeedsAStepUp(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	wantProblem(t, w.rejectAs(t, w.treasurer, req, noSuchRow, "amount_wrong", ""), http.StatusForbidden, "step_up_required",
		"rejecting with a proof that does not exist")
	if v := w.view(t, w.chairman, req); v.Status != "awaiting" {
		t.Fatalf("ACCEPTED — a rejection was recorded without a step-up: %q", v.Status)
	}
}

func TestApiApprovals12_ARejectionShowsOfficersItsReasonNoteAndAuthor(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	r := w.rejectAs(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_rejection"), "documents_missing", "The payslip is not attached")
	v := ok200or201(t, r)
	if v.Status != "rejected" || v.Rejection == nil || v.Rejection.Reason != "documents_missing" || v.Rejection.Note == nil ||
		*v.Rejection.Note != "The payslip is not attached" || v.Rejection.ByUserId == nil || *v.Rejection.ByUserId != w.treasurer {
		t.Fatalf("ACCEPTED — the officers' view of a rejection: %s", r.body)
	}
}

func TestApiApprovals13_LooksWrongNeedsANote(t *testing.T) {
	w := newApprovalAPI(t)
	wantProblem(t, w.rejectAs(t, w.treasurer, w.memberRequest(t, ""), stepUp(t, w.treasurer, "approval_rejection"), "looks_wrong", ""),
		http.StatusUnprocessableEntity, "note_required", "'looks wrong' with no note")
}

func TestApiApprovals14_ASignerCannotThenReject(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	w.signedBy(t, w.treasurer, req)
	wantProblem(t, w.rejectAs(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_rejection"), "amount_wrong", ""),
		http.StatusConflict, "signed_cannot_reject", "the treasurer rejecting a request they signed")
}

// The member is told why, never by whom.
func TestApiApprovals15_TheMemberSeesWhyNotWho(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	ok200or201(t, w.rejectAs(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_rejection"), "documents_missing", "The payslip is not attached"))
	v := w.view(t, w.plain, req)
	if v.Status != "rejected" || v.Rejection == nil || v.Rejection.Reason != "documents_missing" || v.Rejection.ByUserId != nil {
		t.Fatalf("ACCEPTED — the member's view of a rejection: status %q, rejection %+v; want the reason and no rejecter", v.Status, v.Rejection)
	}
}

const referredNextStep = "Speak to your secretary."

// The fraud path does not go back to the requester: the member sees
// "referred" and what to do next, nothing else; officers and the auditor see
// it escalated. "Referred" alone invites a guess; the next step routes the
// member to ask (decided 2026-10-07).
func TestApiApprovals16_TheFraudPathIsNotShownToTheMember(t *testing.T) {
	w := newApprovalAPI(t)
	req := w.memberRequest(t, "")
	ok200or201(t, w.rejectAs(t, w.treasurer, req, stepUp(t, w.treasurer, "approval_rejection"), "looks_wrong", "The payslip employer does not exist"))
	m := w.view(t, w.plain, req)
	if m.Status != "referred" || m.Rejection != nil || m.Escalated {
		t.Fatalf("ACCEPTED — the member's view of a looks_wrong rejection: status %q, rejection %+v, escalated %v; want referred, none, false",
			m.Status, m.Rejection, m.Escalated)
	}
	if m.NextStep == nil || *m.NextStep != referredNextStep {
		t.Fatalf("ACCEPTED — the member is told a request was referred and not what to do: nextStep %v; want %q", m.NextStep, referredNextStep)
	}
	a := w.view(t, w.auditor, req)
	if a.NextStep != nil {
		t.Fatalf("ACCEPTED — the officers' view carries the member's next step %q; it is the member's, and NULL elsewhere", *a.NextStep)
	}
	if a.Status != "rejected" || !a.Escalated || a.Rejection == nil || a.Rejection.Reason != "looks_wrong" {
		t.Fatalf("ACCEPTED — the auditor's view of the fraud path: %+v; want rejected, escalated, looks_wrong", a)
	}
}

func TestApiApprovals17_ARefusedSignerIsNotToldTheRequestIsMissing(t *testing.T) {
	w := newApprovalAPI(t)
	r := w.sign(t, w.auditor, w.memberRequest(t, ""), stepUp(t, w.auditor, "approval_signature"))
	if r.status == http.StatusNotFound {
		t.Fatalf("ACCEPTED — the auditor, who may read the request, was told it does not exist when refused a signature: %s", r.body)
	}
}

func TestApiApprovals18_MoneyIsAStringOnTheWire(t *testing.T) {
	w := newApprovalAPI(t)
	r := w.call(t, http.MethodGet, w.chairman, "/approvals/"+w.memberRequest(t, ""), nil)
	if !strings.Contains(string(r.body), `"amountKobo":"5000000"`) {
		t.Fatalf("ACCEPTED — amountKobo is not the string \"5000000\": %s", r.body)
	}
}

func ok200or201(t *testing.T, r resp) aView {
	t.Helper()
	if r.status != http.StatusOK && r.status != http.StatusCreated {
		t.Fatalf("REFUSED — %d %s", r.status, r.body)
	}
	var v aView
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("WRONG SHAPE — %v: %s", err, r.body)
	}
	return v
}
