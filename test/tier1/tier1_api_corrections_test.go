//go:build tier1

// ACCEPTANCE TESTS — REVERSALS AND ADJUSTMENTS OVER HTTP (0043, stage 3,
// 2026-11-26; LEDGER_CONTRACT §2). One officer proposes, on their own PIN; a
// DIFFERENT officer confirms (the database posts it, and the decision names
// what it posted) or rejects; every refusal in words, never a 500.
package tier1

import (
	"encoding/json"
	"net/http"
	"testing"
)

type cOut struct {
	CorrectionId, EntryId, Kind, Reason, State string
	Lines                                      []struct {
		AccountCode, AmountKobo string
		MemberId                *string
	}
	ProposedBy struct{ UserId, Name string }
	Decision   *struct {
		Confirmed     bool
		PostedEntryId *string
	}
}

func cFrom(t *testing.T, r resp, status int, what string) cOut {
	t.Helper()
	var c cOut
	if r.status != status || json.Unmarshal(r.body, &c) != nil {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, status)
	}
	return c
}

func (w *mapi) proposeCorrection(t *testing.T, as, entry, kind, reason string, lines []map[string]any, proof string) resp {
	t.Helper()
	if proof == "" {
		proof = stepUp(t, as, "entry_correction")
	}
	body := map[string]any{"kind": kind, "reason": reason, "pinProofId": proof}
	if lines != nil {
		body["lines"] = lines
	}
	return w.call(t, http.MethodPost, as, "/entries/"+entry+"/corrections", body)
}

func (w *mapi) decideCorrection(t *testing.T, as, id string, confirmed bool, reason string) resp {
	t.Helper()
	body := map[string]any{"confirmed": confirmed, "pinProofId": stepUp(t, as, "entry_correction_decision")}
	if reason != "" {
		body["reason"] = reason
	}
	return w.call(t, http.MethodPost, as, "/corrections/"+id+"/decisions", body)
}

// codesOf: the codes of an entry's savings and bank accounts, and its member.
func codesOf(t *testing.T, entry string) (savings, bank, member string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT (SELECT a.code FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = $1 AND l.member_id IS NOT NULL),
		(SELECT a.code FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = $1 AND l.member_id IS NULL ORDER BY abs(l.amount_kobo) DESC LIMIT 1),
		(SELECT l.member_id::text FROM ledger_lines l WHERE l.entry_id = $1 AND l.member_id IS NOT NULL)`, entry).Scan(&savings, &bank, &member), "the entry's accounts")
	return
}

func TestApiCorrections01_OneOfficerProposesAReversal(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 250000)
	c := cFrom(t, w.proposeCorrection(t, w.s1, w.lastEntry, "reversal", "Recorded twice from the same receipt", nil, ""), http.StatusCreated, "the treasurer's proposal")
	if c.State != "waiting" || c.Kind != "reversal" || c.EntryId != w.lastEntry || len(c.Lines) != 0 || c.ProposedBy.UserId != w.s1 || c.Decision != nil {
		t.Fatalf("WRONG PROPOSAL — %+v; want a reversal of the entry, waiting, by the treasurer, undecided", c)
	}
}

func TestApiCorrections02_ASecondOfficerConfirmsAndItNamesWhatPosted(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 250000)
	entry := w.lastEntry
	wantProblem(t, w.proposeCorrection(t, w.s1, entry, "reversal", "Recorded twice from the same receipt", nil, stepUp(t, w.s1, "member_status")),
		http.StatusForbidden, "step_up_required", "a proposal on a proof for another act")
	c := cFrom(t, w.proposeCorrection(t, w.s1, entry, "reversal", "Recorded twice from the same receipt", nil, ""), http.StatusCreated, "the proposal")
	wantProblem(t, w.decideCorrection(t, w.s1, c.CorrectionId, true, ""), http.StatusForbidden, "second_officer_required", "the proposer confirming")
	d := cFrom(t, w.decideCorrection(t, w.s2, c.CorrectionId, true, ""), http.StatusCreated, "the chairman confirming")
	if d.State != "confirmed" || d.Decision == nil || !d.Decision.Confirmed || d.Decision.PostedEntryId == nil {
		t.Fatalf("WRONG DECISION — %+v; want confirmed, naming the entry posted", d)
	}
	var reverses string
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(reverses_id::text, '') FROM ledger_entries WHERE id = $1`, *d.Decision.PostedEntryId).Scan(&reverses), "the reversal")
	if reverses != entry {
		t.Fatalf("WRONG REVERSAL — the entry named reverses %q; want %s", reverses, entry)
	}
}

func TestApiCorrections03_NeverAnEntryThatNamesTheOfficer(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.s2Mem, 100000)
	wantProblem(t, w.proposeCorrection(t, w.s2, w.lastEntry, "reversal", "My own contribution was recorded twice", nil, ""),
		http.StatusForbidden, "conflict_of_interest", "the chairman proposing to reverse their own contribution")
}

func TestApiCorrections04_AnAdjustmentsLinesInWordsAndShown(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 250000)
	entry := w.lastEntry
	sv, bk, member := codesOf(t, entry)
	line := func(code string, kobo string, m bool) map[string]any {
		l := map[string]any{"accountCode": code, "amountKobo": kobo}
		if m {
			l["memberId"] = member
		}
		return l
	}
	wantProblem(t, w.proposeCorrection(t, w.s1, entry, "adjustment", "2,000 was paid, not 2,500", []map[string]any{line(sv, "50000", true), line(bk, "-40000", false)}, ""),
		http.StatusUnprocessableEntity, "bad_lines", "an adjustment that does not balance")
	wantProblem(t, w.proposeCorrection(t, w.s1, entry, "adjustment", "2,000 was paid, not 2,500", []map[string]any{line(sv, "50000", true), line("9999", "-50000", false)}, ""),
		http.StatusUnprocessableEntity, "bad_lines", "an adjustment naming an account the society does not have")
	c := cFrom(t, w.proposeCorrection(t, w.s1, entry, "adjustment", "2,000 was paid, not 2,500", []map[string]any{line(sv, "50000", true), line(bk, "-50000", false)}, ""),
		http.StatusCreated, "the adjustment")
	if c.Kind != "adjustment" || len(c.Lines) != 2 || c.Lines[0].AccountCode != sv || c.Lines[0].AmountKobo != "50000" || c.Lines[1].AccountCode != bk || c.Lines[1].AmountKobo != "-50000" {
		t.Fatalf("UNSHOWN — %+v; want the two lines as proposed, by account code", c)
	}
}

func TestApiCorrections05_WhatIsNotCorrectedAndOneAtATime(t *testing.T) {
	w := newMoneyAPI(t)
	acct := vfdNumber()
	_, err := recordSocietyAccount(w.c.s.id, acct, opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	inflow, err := inflowFrom(w.c.s.id, acct, "", "", 300000)
	fixture(t, err, "VFD's confirmed payment")
	fixture(t, one(`SELECT contribution_post_inflow($1, $2, NULL, 'claim')`, inflow, w.bMem), "credited on VFD's word")
	var credited string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM ledger_entries WHERE vfd_inflow_id = $1`, inflow).Scan(&credited), "the credited entry")
	wantProblem(t, w.proposeCorrection(t, w.s1, credited, "reversal", "The member says the amount is wrong", nil, ""),
		http.StatusConflict, "not_correctable", "a reversal of a payment VFD confirmed")
	w.saved(t, w.bMem, 250000)
	cFrom(t, w.proposeCorrection(t, w.s1, w.lastEntry, "reversal", "Recorded twice from the same receipt", nil, ""), http.StatusCreated, "the first proposal")
	wantProblem(t, w.proposeCorrection(t, w.s2, w.lastEntry, "reversal", "A second proposal on the same entry", nil, ""),
		http.StatusConflict, "already_waiting", "a second proposal while one waits")
}

func TestApiCorrections06_TheQueueAndWhoReadsIt(t *testing.T) {
	w := newMoneyAPI(t)
	w.saved(t, w.bMem, 250000)
	waiting := cFrom(t, w.proposeCorrection(t, w.s1, w.lastEntry, "reversal", "Recorded twice from the same receipt", nil, ""), http.StatusCreated, "a waiting proposal")
	w.saved(t, w.bMem, 120000)
	done := cFrom(t, w.proposeCorrection(t, w.s1, w.lastEntry, "reversal", "Recorded against the wrong member", nil, ""), http.StatusCreated, "another proposal")
	cFrom(t, w.decideCorrection(t, w.s2, done.CorrectionId, true, ""), http.StatusCreated, "confirmed")
	r := w.call(t, http.MethodGet, w.secretary, "/societies/"+w.c.s.id+"/corrections?state=waiting", nil)
	var q struct{ Items []cOut }
	if r.status != http.StatusOK || json.Unmarshal(r.body, &q) != nil {
		t.Fatalf("NOT READ — the queue: %d %s", r.status, r.body)
	}
	if len(q.Items) != 1 || q.Items[0].CorrectionId != waiting.CorrectionId {
		t.Fatalf("WRONG QUEUE — %d items; want only the waiting proposal", len(q.Items))
	}
	if r := w.call(t, http.MethodGet, w.auditor, "/corrections/"+waiting.CorrectionId, nil); r.status != http.StatusOK {
		t.Fatalf("NOT READ — the internal auditor reading a correction: %d %s", r.status, r.body)
	}
	if r := w.call(t, http.MethodGet, userOf(t, w.stranger), "/corrections/"+waiting.CorrectionId, nil); r.status != http.StatusNotFound {
		t.Fatalf("SHOWN — someone outside the society read a correction: %d %s; want 404", r.status, r.body)
	}
}
