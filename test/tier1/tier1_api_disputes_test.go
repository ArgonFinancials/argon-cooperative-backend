//go:build tier1

// ACCEPTANCE TESTS — DISPUTES OVER HTTP (0042, stage 1, 2026-11-25; D-13).
// The member queries an entry of their own; an officer answers; the member
// accepts or reopens, saying why; a correction is one officer's proposal and
// a SECOND officer's decision, each on their own PIN; confirmed, the reversal
// is posted and named. Every refusal is a word the screen shows, never a 500.
package tier1

import (
	"encoding/json"
	"net/http"
	"testing"
)

type dOut struct {
	DisputeId, MemberId, EntryId, EntryKind, Question, State string
	Correctable                                              bool
	PendingCorrectionId                                      *string
	Events                                                   []struct {
		Kind, ByUserId                string
		Text, Reply                   *string
		Confirmed                     *bool
		CorrectionId, ReversalEntryId *string
	}
}

func dFrom(t *testing.T, r resp, status int, what string) dOut {
	t.Helper()
	var d dOut
	if r.status != status || json.Unmarshal(r.body, &d) != nil {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, status)
	}
	return d
}

func (w *mapi) raiseDisputeVia(as, member, entry, question string, t *testing.T) resp {
	return w.call(t, http.MethodPost, as, "/members/"+member+"/disputes", map[string]string{"entryId": entry, "question": question})
}

func (w *mapi) answerVia(t *testing.T, as, id, text string) resp {
	return w.call(t, http.MethodPost, as, "/disputes/"+id+"/answers", map[string]string{"answer": text})
}

func (w *mapi) replyVia(t *testing.T, as, id, reply, note string) resp {
	body := map[string]string{"reply": reply}
	if note != "" {
		body["note"] = note
	}
	return w.call(t, http.MethodPost, as, "/disputes/"+id+"/replies", body)
}

func (w *mapi) proposeVia(t *testing.T, as, id, reason, proof string) resp {
	return w.call(t, http.MethodPost, as, "/disputes/"+id+"/corrections", map[string]string{"reason": reason, "pinProofId": proof})
}

func (w *mapi) decideVia(t *testing.T, as, id, correction string, confirmed bool, reason string) resp {
	body := map[string]any{"confirmed": confirmed, "pinProofId": stepUp(t, as, "dispute_correction_decision")}
	if reason != "" {
		body["reason"] = reason
	}
	return w.call(t, http.MethodPost, as, "/disputes/"+id+"/corrections/"+correction+"/decisions", body)
}

// disputedEntry: the borrower's own contribution, recorded by hand, and their query on it.
func disputedEntry(t *testing.T, w *mapi) (entry string, d dOut) {
	t.Helper()
	w.saved(t, w.bMem, 250000)
	entry = w.lastEntry
	d = dFrom(t, w.raiseDisputeVia(w.borrower, w.bMem, entry, "I paid 3,000, not 2,500", t), http.StatusCreated, "the borrower's query")
	return
}

func TestApiDisputes01_TheMemberQueriesTheirOwnEntry(t *testing.T) {
	w := newMoneyAPI(t)
	entry, d := disputedEntry(t, w)
	if d.State != "open" || d.EntryId != entry || d.EntryKind != "contribution_in" || !d.Correctable || len(d.Events) != 0 || d.Question != "I paid 3,000, not 2,500" {
		t.Fatalf("WRONG QUERY — %+v; want open, on the contribution, correctable, the question as written, no steps yet", d)
	}
	wantProblem(t, w.raiseDisputeVia(w.borrower, w.bMem, entry, "Still not what I paid", t), http.StatusConflict, "dispute_once_open", "a second query while the first is open")
}

func TestApiDisputes02_AQueryIsInWordsAndAboutTheMembersOwnEntry(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 250000)
	entry := w.lastEntry
	wantProblem(t, w.raiseDisputeVia(w.borrower, w.bMem, entry, "Why?", t), http.StatusUnprocessableEntity, "question_required", "a query that does not say what is wrong")
	w.saved(t, w.g1Mem, 100000)
	wantProblem(t, w.raiseDisputeVia(w.borrower, w.bMem, w.lastEntry, "This is not what I paid", t), http.StatusUnprocessableEntity, "not_own_entry", "a query on an entry that does not name the member")
	if r := w.raiseDisputeVia(w.g1, w.bMem, entry, "This is not what I paid", t); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another member querying the borrower's entry: %d %s; want 404", r.status, r.body)
	}
}

func TestApiDisputes03_AnOfficerAnswersOnceWhileItWaits(t *testing.T) {
	w := newMoneyAPI(t)
	_, d := disputedEntry(t, w)
	wantProblem(t, w.answerVia(t, w.auditor, d.DisputeId, "The receipt shows 2,500 on 5 October"), http.StatusForbidden, "not_permitted", "the internal auditor answering")
	a := dFrom(t, w.answerVia(t, w.s1, d.DisputeId, "The receipt shows 2,500 on 5 October"), http.StatusCreated, "the treasurer answering")
	if a.State != "answered" || len(a.Events) != 1 || a.Events[0].Kind != "answer" || a.Events[0].ByUserId != w.s1 || a.Events[0].Text == nil || *a.Events[0].Text != "The receipt shows 2,500 on 5 October" {
		t.Fatalf("WRONG ANSWER — %+v; want answered, one step: the treasurer's words as written", a)
	}
	wantProblem(t, w.answerVia(t, w.s2, d.DisputeId, "And the bank statement agrees"), http.StatusConflict, "not_waiting", "a second answer before the member replies")
	// Never an answer to one's own query.
	w.saved(t, w.s2Mem, 100000)
	own := dFrom(t, w.raiseDisputeVia(w.s2, w.s2Mem, w.lastEntry, "My own contribution looks short", t), http.StatusCreated, "the chairman's own query")
	wantProblem(t, w.answerVia(t, w.s2, own.DisputeId, "It is right, I checked myself"), http.StatusForbidden, "conflict_of_interest", "the chairman answering their own query")
}

func TestApiDisputes04_TheMemberAcceptsOrReopensSayingWhy(t *testing.T) {
	w := newMoneyAPI(t)
	_, d := disputedEntry(t, w)
	dFrom(t, w.answerVia(t, w.s1, d.DisputeId, "The receipt shows 2,500 on 5 October"), http.StatusCreated, "the answer")
	wantProblem(t, w.replyVia(t, w.s1, d.DisputeId, "accepted", ""), http.StatusForbidden, "not_permitted", "an officer replying for the member")
	wantProblem(t, w.replyVia(t, w.borrower, d.DisputeId, "reopened", ""), http.StatusUnprocessableEntity, "note_required", "reopened without saying why")
	dFrom(t, w.replyVia(t, w.borrower, d.DisputeId, "reopened", "My bank statement shows 3,000 left my account"), http.StatusCreated, "reopened")
	dFrom(t, w.answerVia(t, w.s2, d.DisputeId, "The other 500 was a charge, shown on its own line"), http.StatusCreated, "the second answer")
	f := dFrom(t, w.replyVia(t, w.borrower, d.DisputeId, "accepted", ""), http.StatusCreated, "accepted")
	kinds := []string{}
	for _, e := range f.Events {
		kinds = append(kinds, e.Kind)
	}
	if f.State != "accepted" || len(kinds) != 4 || kinds[0] != "answer" || kinds[1] != "reply" || kinds[2] != "answer" || kinds[3] != "reply" {
		t.Fatalf("WRONG STEPS — %s, %v; want accepted, the steps oldest first: answer, reply, answer, reply", f.State, kinds)
	}
}

func TestApiDisputes05_ACorrectionNeedsASecondOfficerAndNamesItsReversal(t *testing.T) {
	w := newMoneyAPI(t)
	entry, d := disputedEntry(t, w)
	wantProblem(t, w.proposeVia(t, w.s1, d.DisputeId, "Recorded against the wrong member", stepUp(t, w.s1, "member_status")), http.StatusForbidden, "step_up_required", "a proposal on a proof for another act")
	p := dFrom(t, w.proposeVia(t, w.s1, d.DisputeId, "Recorded against the wrong member", stepUp(t, w.s1, "dispute_correction")), http.StatusCreated, "the treasurer's proposal")
	if p.State != "correction_proposed" || p.PendingCorrectionId == nil {
		t.Fatalf("WRONG STATE — %+v; want correction_proposed, naming the correction that waits", p)
	}
	wantProblem(t, w.decideVia(t, w.s1, d.DisputeId, *p.PendingCorrectionId, true, ""), http.StatusForbidden, "second_officer_required", "the proposer confirming their own correction")
	c := dFrom(t, w.decideVia(t, w.s2, d.DisputeId, *p.PendingCorrectionId, true, ""), http.StatusCreated, "the chairman confirming")
	last := c.Events[len(c.Events)-1]
	if c.State != "corrected" || last.Kind != "decision" || last.Confirmed == nil || !*last.Confirmed || last.ReversalEntryId == nil {
		t.Fatalf("WRONG DECISION — %s, last step %+v; want corrected, a confirmation naming the reversal posted", c.State, last)
	}
	var reverses string
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(reverses_id::text, '') FROM ledger_entries WHERE id = $1`, *last.ReversalEntryId).Scan(&reverses), "the reversal")
	if reverses != entry {
		t.Fatalf("WRONG REVERSAL — the entry named reverses %q; want the queried entry %s", reverses, entry)
	}
}

func TestApiDisputes06_TheQueueAndWhoReadsIt(t *testing.T) {
	w := newMoneyAPI(t)
	_, open := disputedEntry(t, w)
	w.saved(t, w.bMem, 120000)
	closed := dFrom(t, w.raiseDisputeVia(w.borrower, w.bMem, w.lastEntry, "Is this one right too?", t), http.StatusCreated, "a second query")
	dFrom(t, w.answerVia(t, w.s1, closed.DisputeId, "Yes: the receipt matches it exactly"), http.StatusCreated, "its answer")
	dFrom(t, w.replyVia(t, w.borrower, closed.DisputeId, "accepted", ""), http.StatusCreated, "accepted")
	r := w.call(t, http.MethodGet, w.s1, "/societies/"+w.c.s.id+"/disputes?state=waiting", nil)
	var q struct{ Items []dOut }
	if r.status != http.StatusOK || json.Unmarshal(r.body, &q) != nil {
		t.Fatalf("NOT READ — the officers' queue: %d %s", r.status, r.body)
	}
	if len(q.Items) != 1 || q.Items[0].DisputeId != open.DisputeId {
		t.Fatalf("WRONG QUEUE — %d items; want only the open query (an accepted one waits for nobody)", len(q.Items))
	}
	if r := w.call(t, http.MethodGet, w.auditor, "/disputes/"+open.DisputeId, nil); r.status != http.StatusOK {
		t.Fatalf("NOT READ — the internal auditor reading a query: %d %s; want 200", r.status, r.body)
	}
	if r := w.call(t, http.MethodGet, userOf(t, w.stranger), "/disputes/"+open.DisputeId, nil); r.status != http.StatusNotFound {
		t.Fatalf("SHOWN — a member of another society read the query: %d %s; want 404", r.status, r.body)
	}
}

func TestApiDisputes07_APaymentVFDConfirmedIsNotCorrectedByOfficers(t *testing.T) {
	w := newMoneyAPI(t)
	acct := vfdNumber()
	_, err := recordSocietyAccount(w.c.s.id, acct, opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	inflow, err := inflowFrom(w.c.s.id, acct, "", "", 300000)
	fixture(t, err, "VFD's confirmed payment")
	fixture(t, one(`SELECT contribution_post_inflow($1, $2, NULL, 'claim')`, inflow, w.bMem), "credited on VFD's word")
	var entry string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM ledger_entries WHERE vfd_inflow_id = $1`, inflow).Scan(&entry), "the credited entry")
	d := dFrom(t, w.raiseDisputeVia(w.borrower, w.bMem, entry, "I paid 3,500, not 3,000", t), http.StatusCreated, "the query")
	if d.Correctable {
		t.Fatalf("OFFERED — a payment VFD confirmed reads correctable; the money moved at the bank")
	}
	wantProblem(t, w.proposeVia(t, w.s1, d.DisputeId, "Reverse it, the member says otherwise", stepUp(t, w.s1, "dispute_correction")), http.StatusConflict, "not_correctable", "a correction of a payment VFD confirmed")
}
