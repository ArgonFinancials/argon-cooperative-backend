//go:build tier1

// ACCEPTANCE TESTS — A MEMBER'S STATUS OVER HTTP (0038, 2026-11-24). The
// secretary suspends, reinstates, or records a member leaving, on their own
// PIN, with a reason: POST .../members/{m}/status-changes. Every refusal that
// can be known is answered in words before anything is written, so the
// secretary's proof is not spent by one. GET the same path: the changes,
// newest first, naming who, for the society's officers.
package tier1

import (
	"encoding/json"
	"net/http"
	"testing"
)

func (w *enrolAPI) statusPath(member string) string {
	return "/societies/" + w.s.id + "/members/" + member + "/status-changes"
}

// setStatusVia: `as` changes member's status, on `proof` ("" = a fresh one for member_status).
func (w *enrolAPI) setStatusVia(t *testing.T, as signer, member, to, reason, proof string) resp {
	t.Helper()
	if proof == "" {
		proof = stepUp(t, as.user, "member_status")
	}
	return postJSON(t, w.srv, w.statusPath(member), signIn(t, as.user), map[string]string{"status": to, "reason": reason, "pinProofId": proof})
}

type statusChange struct {
	ChangeID, MemberID, From, To, Reason, ByUserID, ByName, At string
}

func activeNewcomer(t *testing.T, w *enrolAPI) string {
	t.Helper()
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.newcomer.member), "an active member")
	return w.newcomer.member
}

func TestApiStatus01_TheSecretarySuspendsAndReinstatesWithAReason(t *testing.T) {
	w := newEnrolAPI(t)
	m := activeNewcomer(t, w)
	r := w.setStatusVia(t, w.sec, m, "suspended", "Three months' contributions unpaid", "")
	var c statusChange
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &c) != nil {
		t.Fatalf("REFUSED — the secretary suspending a member: %d %s", r.status, r.body)
	}
	if c.From != "active" || c.To != "suspended" || c.Reason != "Three months' contributions unpaid" || c.ByUserID != w.sec.user || c.ByName != "Secretary" {
		t.Fatalf("WRONG RECORD — %+v; want active -> suspended, the reason as written, by the secretary", c)
	}
	if s := statusOf(t, m); s != "suspended" {
		t.Fatalf("NOT MOVED — the member is %q; want suspended", s)
	}
	if r := w.setStatusVia(t, w.sec, m, "active", "Arrears paid", ""); r.status != http.StatusCreated || statusOf(t, m) != "active" {
		t.Fatalf("NOT REINSTATED — %d %s; the member is %q", r.status, r.body, statusOf(t, m))
	}
}

func TestApiStatus02_OnlyTheSecretary(t *testing.T) {
	w := newEnrolAPI(t)
	m := activeNewcomer(t, w)
	wantProblem(t, w.setStatusVia(t, w.treas, m, "suspended", "x", ""), http.StatusForbidden, "not_permitted", "the treasurer suspending a member")
	if r := w.setStatusVia(t, w.elsewhere, m, "suspended", "x", ""); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's secretary: %d %s; want 404", r.status, r.body)
	}
	if s := statusOf(t, m); s != "active" {
		t.Fatalf("MOVED — a refused request left the member %q", s)
	}
}

func TestApiStatus03_TheSecretaryNeverChangesTheirOwn(t *testing.T) {
	w := newEnrolAPI(t)
	fixture(t, one(`UPDATE members SET status = 'suspended' WHERE id = $1`, w.sec.member), "a suspended secretary record")
	wantProblem(t, w.setStatusVia(t, w.sec, w.sec.member, "active", "Reinstating myself", ""), http.StatusForbidden, "conflict_of_interest", "the secretary reinstating their own record")
}

// A refusal spends nothing: the same proof then records the change.
func TestApiStatus04_AReasonIsRequiredAndTheRefusalSpendsNothing(t *testing.T) {
	w := newEnrolAPI(t)
	m := activeNewcomer(t, w)
	proof := stepUp(t, w.sec.user, "member_status")
	wantProblem(t, w.setStatusVia(t, w.sec, m, "suspended", "  ", proof), http.StatusUnprocessableEntity, "reason_required", "a suspension with no reason")
	if r := w.setStatusVia(t, w.sec, m, "suspended", "Arrears", proof); r.status != http.StatusCreated {
		t.Fatalf("SPENT — the same proof could not then record the suspension: %d %s", r.status, r.body)
	}
}

func TestApiStatus05_LeavingIsFinal(t *testing.T) {
	w := newEnrolAPI(t)
	m := activeNewcomer(t, w)
	wantProblem(t, w.setStatusVia(t, w.sec, m, "active", "Already", ""), http.StatusConflict, "not_allowed", "an active member made active")
	if r := w.setStatusVia(t, w.sec, m, "exited", "Resigned by letter", ""); r.status != http.StatusCreated {
		t.Fatalf("REFUSED — a member leaving: %d %s", r.status, r.body)
	}
	wantProblem(t, w.setStatusVia(t, w.sec, m, "active", "Came back", ""), http.StatusConflict, "not_allowed", "a member who left made active again")
}

func TestApiStatus06_AnOfficerInPostIsNotSuspended(t *testing.T) {
	w := newEnrolAPI(t)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.treas.member), "an active treasurer")
	wantProblem(t, w.setStatusVia(t, w.sec, w.treas.member, "suspended", "Absent", ""), http.StatusConflict, "holds_a_post", "the treasurer suspended in post")
}

func TestApiStatus07_TheHistoryIsNewestFirstAndSaysWho(t *testing.T) {
	w := newEnrolAPI(t)
	m := activeNewcomer(t, w)
	for _, c := range []struct{ to, why string }{{"suspended", "Arrears"}, {"active", "Paid"}} {
		if r := w.setStatusVia(t, w.sec, m, c.to, c.why, ""); r.status != http.StatusCreated {
			t.Fatalf("FIXTURE: %s: %d %s", c.to, r.status, r.body)
		}
	}
	h := get(t, w.srv, w.statusPath(m), signIn(t, w.auditor.user))
	var out struct{ Changes []statusChange }
	if h.status != http.StatusOK || json.Unmarshal(h.body, &out) != nil || len(out.Changes) != 2 {
		t.Fatalf("WRONG HISTORY — the auditor reads %d %s; want two changes", h.status, h.body)
	}
	if out.Changes[0].To != "active" || out.Changes[1].To != "suspended" || out.Changes[0].ByName != "Secretary" || out.Changes[1].Reason != "Arrears" {
		t.Fatalf("UNSAID — %+v; want the reinstatement first, then the suspension, each naming the secretary and the reason", out.Changes)
	}
	if r := get(t, w.srv, w.statusPath(m), signIn(t, w.elsewhere.user)); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's secretary read the history: %d; want 404", r.status)
	}
}
