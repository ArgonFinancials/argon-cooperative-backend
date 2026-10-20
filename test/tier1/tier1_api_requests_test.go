//go:build tier1

// Acceptance tests for raising and listing withdrawal requests (0016) —
// written before the handlers, for review. Every TestApiRequests* name is here.
//
//	POST /societies/{id}/withdrawal-requests   raise one
//	GET  /members/{id}/withdrawal-requests     the member's own, member projection
//	GET  /societies/{id}/approval-requests     the society's, officer projection
//
// Decided 2026-10-18:
//   - Raised by the member it pays, on their own step-up, or by an officer on
//     theirs for a member without a phone (proposed: the officers who record
//     money, the treasurer and the secretary). The raiser is recorded and never
//     signs it (§10, "raises").
//   - The server sets the expiry from the society's window (seven days shipped);
//     a client's expiry is ignored.
//   - Within withdrawable when raised, refused with the figures; the hold checks
//     again before money moves.
//   - The Idempotency-Key is the request's reference: a retry answers 200 with
//     the first request, writes nothing, needs no step-up.
//   - A resubmission points at the same member's rejected request.
//   - The member's list: progress and outcome, never who signed or raised it;
//     the fraud path reads "referred". The officers' list: who raised, who
//     signed, filterable by status.
package tier1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (w *mapi) raiseVia(t *testing.T, as, member string, amount any, proof, key string, extra map[string]any) resp {
	t.Helper()
	body := map[string]any{"memberId": member, "amountKobo": amount, "pinProofId": proof}
	for k, v := range extra {
		body[k] = v
	}
	h := map[string]string{}
	switch key {
	case "":
		h["Idempotency-Key"] = "REQ-" + digest()[:16]
	case "none":
	default:
		h["Idempotency-Key"] = key
	}
	return w.callH(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/withdrawal-requests", body, h)
}

type reqView struct {
	RequestId, MemberId, Projection, Status, AmountKobo, RaisedAt, ExpiresAt string
	Required, SignedCount                                                    int
	RaisedBy, ResubmitsId, NextStep                                          *string
	Signatures                                                               *[]json.RawMessage
	Rejection                                                                *json.RawMessage
}

type reqList struct {
	Items []reqView `json:"items"`
}

// raisedVia: the borrower's own request for `amount`, raised through the API (fixture).
func (w *mapi) raisedVia(t *testing.T, amount int64) reqView {
	t.Helper()
	r := w.raiseVia(t, w.borrower, w.bMem, k(amount), stepUp(t, w.borrower, "withdrawal_request"), "", nil)
	if r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the borrower raising ₦%d: %d %s", amount/100, r.status, r.body)
	}
	var v reqView
	fixture(t, json.Unmarshal(r.body, &v), "the request")
	return v
}

func requestsFor(t *testing.T, member string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM approval_requests WHERE member_id = $1`, member).Scan(&n), "the member's requests")
	return n
}

func (w *mapi) signVia(t *testing.T, as, req string) resp {
	return w.call(t, http.MethodPost, as, "/approvals/"+req+"/signatures", map[string]string{"pinProofId": stepUp(t, as, "approval_signature")})
}

func (w *mapi) rejectVia(t *testing.T, as, req, reason, note string) resp {
	return w.call(t, http.MethodPost, as, "/approvals/"+req+"/rejections",
		map[string]string{"pinProofId": stepUp(t, as, "approval_rejection"), "reason": reason, "note": note})
}

func days(t *testing.T, v reqView) float64 {
	t.Helper()
	a, err1 := time.Parse(time.RFC3339Nano, v.RaisedAt)
	b, err2 := time.Parse(time.RFC3339Nano, v.ExpiresAt)
	if err1 != nil || err2 != nil {
		t.Fatalf("WRONG SHAPE — raisedAt %q, expiresAt %q", v.RaisedAt, v.ExpiresAt)
	}
	return b.Sub(a).Hours() / 24
}

// =============================================================================
// RAISING
// =============================================================================

func TestApiRequests01_AMemberRaisesTheirOwn(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	if v.Projection != "member" || v.Status != "awaiting" || v.MemberId != w.bMem || v.Required != 2 || v.AmountKobo != k(payKobo) || v.RaisedBy != nil {
		t.Fatalf("WRONG REQUEST — %+v; want the member's projection, awaiting, the society's quorum of 2, no raiser shown", v)
	}
	if d := days(t, v); d != 7 {
		t.Fatalf("WRONG EXPIRY — %v days; want the society's 7", d)
	}
}

func TestApiRequests02_RaisingNeedsAStepUp(t *testing.T) {
	w := newMoneyAPI(t)
	n := requestsFor(t, w.bMem)
	wantProblem(t, w.raiseVia(t, w.borrower, w.bMem, k(payKobo), noSuchRow, "", nil), http.StatusForbidden, "step_up_required", "raising with a proof that does not exist")
	if requestsFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — a request raised without a step-up")
	}
}

// For a member without a phone: the officer is recorded as the raiser.
func TestApiRequests03_AnOfficerRaisesForAMember(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.raiseVia(t, w.poster, w.bMem, k(payKobo), stepUp(t, w.poster, "withdrawal_request"), "", nil)
	v := decoded[reqView](t, r, http.StatusCreated, "the treasurer raising for the borrower")
	if v.Projection != "officer" || v.RaisedBy == nil || *v.RaisedBy != w.poster || v.MemberId != w.bMem {
		t.Fatalf("UNATTRIBUTED — %+v; want the officer projection, raised by the treasurer, for the borrower", v)
	}
}

func TestApiRequests04_AnotherMemberCannotRaiseForYou(t *testing.T) {
	w := newMoneyAPI(t)
	n := requestsFor(t, w.bMem)
	wantProblem(t, w.raiseVia(t, w.g1, w.bMem, k(payKobo), stepUp(t, w.g1, "withdrawal_request"), "", nil), http.StatusForbidden, "not_permitted",
		"a guarantor (no post) raising the borrower's withdrawal")
	if requestsFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — another member raised the borrower's withdrawal")
	}
}

func TestApiRequests05_TheAuditorAndTheChairmanDoNotRaise(t *testing.T) {
	w := newMoneyAPI(t)
	n := requestsFor(t, w.bMem)
	for who, u := range map[string]string{"the internal auditor": w.auditor, "the chairman (authorises; does not record)": w.s2} {
		wantProblem(t, w.raiseVia(t, u, w.bMem, k(payKobo), stepUp(t, u, "withdrawal_request"), "", nil), http.StatusForbidden, "not_permitted", who+" raising a withdrawal")
	}
	if requestsFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — an officer without the duty raised a withdrawal")
	}
}

func TestApiRequests06_AnyoneElseGetsNotFound(t *testing.T) {
	w := newMoneyAPI(t)
	none := w.callH(t, http.MethodPost, w.abroad, "/societies/"+noSuchRow+"/withdrawal-requests",
		map[string]any{"memberId": w.bMem, "amountKobo": k(payKobo), "pinProofId": noSuchRow}, map[string]string{"Idempotency-Key": "REQ-" + digest()[:16]})
	r := w.raiseVia(t, w.abroad, w.bMem, k(payKobo), stepUp(t, w.abroad, "withdrawal_request"), "", nil)
	if r.status != http.StatusNotFound || string(r.body) != string(none.body) {
		t.Fatalf("ACCEPTED — the treasurer of another society: %d %s (no such society: %d %s)", r.status, r.body, none.status, none.body)
	}
}

// The database's figures, which only it has.
func TestApiRequests07_BeyondWithdrawableIsRefusedWithTheFigures(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.raiseVia(t, w.borrower, w.bMem, k(borrowerWithdrawable), stepUp(t, w.borrower, "withdrawal_request"), "", nil)
	wantProblem(t, r, http.StatusUnprocessableEntity, "exceeds_withdrawable", "asking for everything withdrawable, the charges on top")
	if !strings.Contains(string(r.body), "held for guarantees") {
		t.Fatalf("UNEXPLAINED — the refusal does not carry the figures: %s", r.body)
	}
}

// A dropped connection: the same key, first with a fresh step-up (only the key
// stands between it and a second request), then resent as it was.
func TestApiRequests08_ARetryIsRaisedOnce(t *testing.T) {
	w := newMoneyAPI(t)
	key := "REQ-" + digest()[:16]
	proof := stepUp(t, w.borrower, "withdrawal_request")
	first := decoded[reqView](t, w.raiseVia(t, w.borrower, w.bMem, k(payKobo), proof, key, nil), http.StatusCreated, "the request")
	n := requestsFor(t, w.bMem)
	for _, rt := range []struct{ what, proof string }{
		{"with a fresh step-up", stepUp(t, w.borrower, "withdrawal_request")},
		{"resent as it was (its proof spent)", proof},
	} {
		r := w.raiseVia(t, w.borrower, w.bMem, k(payKobo), rt.proof, key, nil)
		if r.status == http.StatusCreated {
			t.Fatalf("RAISED TWICE — the same key, retried %s, raised another request: %s", rt.what, r.body)
		}
		again := decoded[reqView](t, r, http.StatusOK, "the request retried "+rt.what)
		if again.RequestId != first.RequestId || requestsFor(t, w.bMem) != n {
			t.Fatalf("RAISED TWICE — retried %s: request %s (first %s), %d requests (were %d)", rt.what, again.RequestId, first.RequestId, requestsFor(t, w.bMem), n)
		}
	}
}

func TestApiRequests09_TheKeyIsRequired(t *testing.T) {
	w := newMoneyAPI(t)
	n := requestsFor(t, w.bMem)
	r := w.raiseVia(t, w.borrower, w.bMem, k(payKobo), stepUp(t, w.borrower, "withdrawal_request"), "none", nil)
	if r.status != http.StatusBadRequest || requestsFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — a request with no Idempotency-Key: %d %s; want 400 and nothing raised", r.status, r.body)
	}
}

// (TestApiRequests10 was "the client cannot choose the expiry". Removed before
// review: the body has no expiry field, so the strict decoder drops one before
// any handler and no API-layer break can make it red: decoration, not a test.
// The rule is the database's, proven by TestRaise05; test 01 checks the
// seven days through the API.)

func TestApiRequests11_AResubmissionPointsAtTheRejected(t *testing.T) {
	w := newMoneyAPI(t)
	original := w.raisedVia(t, payKobo)
	if r := w.rejectVia(t, w.s2, original.RequestId, "documents_missing", "The payslip is not attached"); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the rejection: %d %s", r.status, r.body)
	}
	v := decoded[reqView](t, w.raiseVia(t, w.borrower, w.bMem, k(payKobo), stepUp(t, w.borrower, "withdrawal_request"), "", map[string]any{"resubmitsId": original.RequestId}),
		http.StatusCreated, "the resubmission")
	if v.ResubmitsId == nil || *v.ResubmitsId != original.RequestId {
		t.Fatalf("UNLINKED — the resubmission reads %+v; want it to point at %s", v, original.RequestId)
	}
	open := w.raisedVia(t, payKobo)
	wantProblem(t, w.raiseVia(t, w.borrower, w.bMem, k(payKobo), stepUp(t, w.borrower, "withdrawal_request"), "", map[string]any{"resubmitsId": open.RequestId}),
		http.StatusConflict, "not_resubmittable", "resubmitting a request that was never rejected")
}

// §10, "raises": the officer who raised it never signs it.
func TestApiRequests12_TheRaiserNeverSigns(t *testing.T) {
	w := newMoneyAPI(t)
	v := decoded[reqView](t, w.raiseVia(t, w.poster, w.bMem, k(payKobo), stepUp(t, w.poster, "withdrawal_request"), "", nil), http.StatusCreated, "the treasurer raising")
	wantProblem(t, w.signVia(t, w.poster, v.RequestId), http.StatusForbidden, "conflict_of_interest", "the treasurer signing the request they raised")
}

// =============================================================================
// THE MEMBER'S LIST
// =============================================================================

func TestApiRequests13_TheMemberListsTheirOwnNewestFirst(t *testing.T) {
	w := newMoneyAPI(t)
	first := w.raisedVia(t, 100000)
	second := w.raisedVia(t, 200000)
	l := decoded[reqList](t, w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil), http.StatusOK, "the borrower's list")
	if len(l.Items) != 2 || l.Items[0].RequestId != second.RequestId || l.Items[1].RequestId != first.RequestId {
		t.Fatalf("WRONG LIST — %+v; want the two requests, newest first", l.Items)
	}
	for _, v := range l.Items {
		if v.Projection != "member" {
			t.Fatalf("WRONG PROJECTION — %q in the member's own list", v.Projection)
		}
	}
	none := w.call(t, http.MethodGet, w.g1, "/members/"+noSuchRow+"/withdrawal-requests", nil)
	other := w.call(t, http.MethodGet, w.g1, "/members/"+w.bMem+"/withdrawal-requests", nil)
	if other.status != http.StatusNotFound || string(other.body) != string(none.body) {
		t.Fatalf("ACCEPTED — another member read the borrower's requests: %d %s", other.status, other.body)
	}
}

// Progress, never who: not who signed, not who raised it for them.
func TestApiRequests14_TheMembersListNamesNobody(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.raiseVia(t, w.poster, w.bMem, k(payKobo), stepUp(t, w.poster, "withdrawal_request"), "", nil)
	v := decoded[reqView](t, r, http.StatusCreated, "the treasurer raising for the borrower")
	if s := w.signVia(t, w.s2, v.RequestId); s.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the chairman's signature: %d %s", s.status, s.body)
	}
	l := decoded[reqList](t, w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil), http.StatusOK, "the borrower's list")
	if len(l.Items) != 1 || l.Items[0].SignedCount != 1 || l.Items[0].Signatures != nil || l.Items[0].RaisedBy != nil {
		t.Fatalf("NAMED — the member's list: %+v; want the progress (1 signed) and no names", l.Items)
	}
}

func TestApiRequests15_TheFraudPathInTheMembersList(t *testing.T) {
	w := newMoneyAPI(t)
	v := w.raisedVia(t, payKobo)
	if r := w.rejectVia(t, w.s2, v.RequestId, "looks_wrong", "The payslip employer does not exist"); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the rejection: %d %s", r.status, r.body)
	}
	l := decoded[reqList](t, w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil), http.StatusOK, "the borrower's list")
	if len(l.Items) != 1 || l.Items[0].Status != "referred" || l.Items[0].Rejection != nil || l.Items[0].NextStep == nil || *l.Items[0].NextStep != referredNextStep {
		t.Fatalf("SHOWN — the fraud path in the member's list: %+v; want referred, no rejection, the next step", l.Items)
	}
}

func TestApiRequests16_AnEmptyListIsEmpty(t *testing.T) {
	w := newMoneyAPI(t)
	r := w.call(t, http.MethodGet, w.g1, "/members/"+w.g1Mem+"/withdrawal-requests", nil)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"items":[]`) {
		t.Fatalf("WRONG — a member with no requests: %d %s; want items []", r.status, r.body)
	}
}

// =============================================================================
// THE OFFICERS' LIST
// =============================================================================

func TestApiRequests17_OfficersListByStatus(t *testing.T) {
	w := newMoneyAPI(t)
	awaiting := w.raisedVia(t, 100000)
	approved := w.raisedVia(t, 200000)
	for _, s := range []string{w.s2, w.secretary} {
		if r := w.signVia(t, s, approved.RequestId); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: a signature: %d %s", r.status, r.body)
		}
	}
	l := decoded[reqList](t, w.call(t, http.MethodGet, w.poster, "/societies/"+w.c.s.id+"/approval-requests?status=awaiting", nil), http.StatusOK, "the awaiting list")
	if len(l.Items) != 1 || l.Items[0].RequestId != awaiting.RequestId || l.Items[0].Projection != "officer" ||
		l.Items[0].RaisedBy == nil || *l.Items[0].RaisedBy != w.borrower {
		t.Fatalf("WRONG LIST — awaiting: %+v; want only %s, in the officer projection, raised by the borrower", l.Items, awaiting.RequestId)
	}
	all := decoded[reqList](t, w.call(t, http.MethodGet, w.poster, "/societies/"+w.c.s.id+"/approval-requests", nil), http.StatusOK, "the whole list")
	if len(all.Items) != 2 {
		t.Fatalf("WRONG LIST — unfiltered: %d items; want 2", len(all.Items))
	}
}

func TestApiRequests18_TheAuditorReadsTheSocietysList(t *testing.T) {
	w := newMoneyAPI(t)
	w.raisedVia(t, payKobo)
	l := decoded[reqList](t, w.call(t, http.MethodGet, w.auditor, "/societies/"+w.c.s.id+"/approval-requests", nil), http.StatusOK, "the auditor's list")
	if len(l.Items) != 1 || l.Items[0].Projection != "officer" {
		t.Fatalf("REFUSED — the internal auditor (reads everything): %+v", l.Items)
	}
}

func TestApiRequests19_AMemberCannotListTheSociety(t *testing.T) {
	w := newMoneyAPI(t)
	w.raisedVia(t, payKobo)
	none := w.call(t, http.MethodGet, w.borrower, "/societies/"+noSuchRow+"/approval-requests", nil)
	r := w.call(t, http.MethodGet, w.borrower, "/societies/"+w.c.s.id+"/approval-requests", nil)
	if r.status != http.StatusNotFound || string(r.body) != string(none.body) {
		t.Fatalf("ACCEPTED — a member with no post read the society's requests: %d %s", r.status, r.body)
	}
}

// Raw bodies only: the request's own answer is not decoded, so a break that
// sends numbers fails this test's check, not its fixture.
func TestApiRequests20_MoneyIsAStringOnTheWire(t *testing.T) {
	w := newMoneyAPI(t)
	if r := w.raiseVia(t, w.borrower, w.bMem, k(payKobo), stepUp(t, w.borrower, "withdrawal_request"), "", nil); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the request: %d %s", r.status, r.body)
	}
	r := w.call(t, http.MethodGet, w.borrower, "/members/"+w.bMem+"/withdrawal-requests", nil)
	if !strings.Contains(string(r.body), `"amountKobo":"`+k(payKobo)+`"`) {
		t.Fatalf("ACCEPTED — amountKobo is not a string: %s", r.body)
	}
}

// A request no quorum can approve, refused when raised with the arithmetic and
// the remedy (decided 2026-10-19). The secretary's appointment has ended: the
// treasurer raising for a member leaves only the chairman to sign, at two.
//
// The arithmetic is asserted as well as the remedy: the API passes the
// database's words through, and a handler writing its own sentence that
// happens to name the remedy must fail here (break req_unreachable_rewritten).
func TestApiRequests21_ARequestNoQuorumCanApproveIsRefused(t *testing.T) {
	w := newMoneyAPI(t)
	// The secretary's term, held since 2020-01-01, ended yesterday: an end
	// after its start (range_sane, 0001, refuses any other), read back so a
	// missing appointment stops here rather than building another world.
	var from string
	fixture(t, db.QueryRow(bg(), `UPDATE appointments SET held_until = current_date - 1
		WHERE member_id = $1 AND held_until IS NULL RETURNING held_from::text`, w.secMem).Scan(&from), "the secretary's appointment ended")
	n := requestsFor(t, w.bMem)
	r := w.raiseVia(t, w.poster, w.bMem, k(payKobo), stepUp(t, w.poster, "withdrawal_request"), "", nil)
	wantProblem(t, r, http.StatusUnprocessableEntity, "quorum_unreachable", "a request only the chairman may sign, at a quorum of two")
	if b := string(r.body); !strings.Contains(b, "1 signatories may sign it") || !strings.Contains(b, "needs 2") || !strings.Contains(b, "Appoint another signatory") {
		t.Fatalf("UNEXPLAINED — the refusal must carry the database's arithmetic (1 may sign, 2 needed) and the remedy: %s", r.body)
	}
	if requestsFor(t, w.bMem) != n {
		t.Fatalf("ACCEPTED — a request no quorum can approve was raised")
	}
}
