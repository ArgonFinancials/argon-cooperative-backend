//go:build tier1

// ACCEPTANCE TESTS — 0042, A MEMBER QUERIES AN ENTRY; AN OFFICER ANSWERS; A
// CORRECTION NEEDS A SECOND OFFICER (stage 1, 2026-11-25; D-13).
//
//   - the member queries an entry of their own society that names them, with
//     a question; one open query per entry;
//   - a current officer, not the auditor and not the member, answers a query
//     waiting for one; the member accepts (closed) or reopens, saying why;
//   - a correction is proposed by one officer on their own act and decided
//     once by a DIFFERENT officer on theirs; confirmed, the database posts
//     the full reversal; rejected, with a reason, the query is open again;
//   - only an entry an officer recorded by hand is corrected this way: never
//     one VFD confirmed;
//   - every reversal mirrors its original, once, and is never reversed;
//   - nothing is edited.
package tier1

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

type dqWorld struct {
	*mworld
	entry   string // the borrower's own contribution, recorded by hand
	auditor string
}

func newDisputeWorld(t *testing.T) *dqWorld {
	t.Helper()
	w := &dqWorld{mworld: newMoneyWorld(t)}
	w.saved(t, w.bMem, 250000)
	w.entry = w.lastEntry
	var auditorMem string
	w.auditor, auditorMem = bankMember(t, w.mworld, "Ngozi Audit", true)
	fixture(t, w.c.s.appoint(auditorMem, "internal_auditor", false, "2020-01-01", ""), "the auditor")
	return w
}

func dqRaise(user, member, entry, question string) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO entry_disputes (entry_id, member_id, raised_by, question) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		entry, member, user, question).Scan(&id)
	return id, err
}

func dqMustRaise(t *testing.T, w *dqWorld) string {
	t.Helper()
	id, err := dqRaise(w.borrower, w.bMem, w.entry, "I paid 3,000, not 2,500")
	fixture(t, err, "the member's query")
	return id
}

func dqAnswer(user, dispute, text string) error {
	return one(`INSERT INTO dispute_answers (dispute_id, answered_by, answer) VALUES ($1, $2, $3)`, dispute, user, text)
}

func dqReply(user, dispute, reply, note string) error {
	return one(`INSERT INTO dispute_replies (dispute_id, replied_by, reply, note) VALUES ($1, $2, $3, $4)`, dispute, user, reply, nz(note))
}

// dqPropose: `actor` takes a step-up for `kind` ("" = dispute_correction) and
// proposes the correction in that transaction.
func dqPropose(t *testing.T, actor, dispute, reason, kind string) (string, error) {
	t.Helper()
	if kind == "" {
		kind = "dispute_correction"
	}
	proof := stepUp(t, actor, kind)
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO dispute_corrections (dispute_id, proposed_by, proposed_act, reason) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			dispute, actor, act, reason).Scan(&id)
	})
	return id, err
}

func dqDecide(t *testing.T, actor, correction string, confirmed bool, reason, kind string) error {
	t.Helper()
	if kind == "" {
		kind = "dispute_correction_decision"
	}
	proof := stepUp(t, actor, kind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO dispute_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			correction, confirmed, actor, act, nz(reason))
		return err
	})
}

func dqState(t *testing.T, dispute string) string {
	t.Helper()
	var s string
	fixture(t, db.QueryRow(bg(), `SELECT state FROM dispute_states WHERE dispute_id = $1`, dispute).Scan(&s), "the query's state")
	return s
}

func dqReversalOf(t *testing.T, entry string) (id string, mirrored bool) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT coalesce((SELECT id::text FROM ledger_entries WHERE reverses_id = $1), ''),
		NOT EXISTS (SELECT account_id, member_id, -amount_kobo FROM ledger_lines WHERE entry_id = $1
		            EXCEPT ALL SELECT account_id, member_id, amount_kobo FROM ledger_lines WHERE entry_id = (SELECT id FROM ledger_entries WHERE reverses_id = $1))`,
		entry).Scan(&id, &mirrored), "the reversal")
	return
}

func TestDisputes01_AMemberQueriesTheirOwnEntry(t *testing.T) {
	w := newDisputeWorld(t)
	_, err := dqRaise(w.g1, w.bMem, w.entry, "This is not what I paid")
	refused(t, err, "someone else querying the borrower's entry for them", refusedAs("dispute_by_member"))
	w.saved(t, w.g1Mem, 100000)
	_, err = dqRaise(w.borrower, w.bMem, w.lastEntry, "This is not what I paid")
	refused(t, err, "a member querying an entry that does not name them", refusedAs("dispute_on_own_entry"))
	_, err = dqRaise(w.borrower, w.bMem, w.entry, "Why?")
	refused(t, err, "a query that does not say what is wrong", refusedAs("dispute_question_said"))
	id := dqMustRaise(t, w)
	if s := dqState(t, id); s != "open" {
		t.Fatalf("WRONG STATE — a new query is %q; want open", s)
	}
	_, err = dqRaise(w.borrower, w.bMem, w.entry, "Still not what I paid")
	refused(t, err, "a second query while the first is open", refusedAs("dispute_once_open"))
	fixture(t, dqAnswer(w.s1, id, "The receipt shows 2,500 on 5 October"), "the answer")
	fixture(t, dqReply(w.borrower, id, "accepted", ""), "accepted")
	_, err = dqRaise(w.borrower, w.bMem, w.entry, "Another question about it")
	accepted(t, err, "a new query once the first is closed")
}

func TestDisputes02_AnOfficerAnswersAQueryWaitingForOne(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	refused(t, dqAnswer(w.auditor, id, "The receipt shows 2,500"), "the internal auditor answering", refusedAs("dispute_answered_by_officer"))
	refused(t, dqAnswer(w.g1, id, "The receipt shows 2,500"), "a member who holds no post answering", refusedAs("dispute_answered_by_officer"))
	refused(t, dqAnswer(w.s1, id, "short"), "an answer that says nothing", refusedAs("dispute_answer_said"))
	accepted(t, dqAnswer(w.s1, id, "The receipt shows 2,500 on 5 October"), "the treasurer answering")
	if s := dqState(t, id); s != "answered" {
		t.Fatalf("WRONG STATE — answered, the query is %q", s)
	}
	refused(t, dqAnswer(w.s2, id, "And the bank statement agrees"), "a second answer before the member replies", refusedAs("dispute_awaits_answer"))
	// An officer never answers a query about their own entry.
	w.saved(t, w.s2Mem, 100000)
	own, err := dqRaise(w.s2, w.s2Mem, w.lastEntry, "My own contribution looks short")
	fixture(t, err, "the chairman's own query")
	refused(t, dqAnswer(w.s2, own, "It is right, I checked myself"), "the chairman answering their own query", refusedAs("dispute_not_own"))
}

func TestDisputes03_TheMemberAcceptsOrReopensSayingWhy(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	refused(t, dqReply(w.borrower, id, "accepted", ""), "a reply before any answer", refusedAs("dispute_awaits_reply"))
	fixture(t, dqAnswer(w.s1, id, "The receipt shows 2,500 on 5 October"), "the answer")
	refused(t, dqReply(w.g1, id, "accepted", ""), "someone else replying for the member", refusedAs("dispute_reply_by_member"))
	refused(t, dqReply(w.borrower, id, "reopened", ""), "reopened without saying why", refusedAs("dispute_reopened_says_why"))
	accepted(t, dqReply(w.borrower, id, "reopened", "My bank statement shows 3,000 left my account"), "reopened, saying why")
	if s := dqState(t, id); s != "reopened" {
		t.Fatalf("WRONG STATE — reopened, the query is %q", s)
	}
	fixture(t, dqAnswer(w.s2, id, "The other 500 was a charge, shown on its own line"), "the second answer")
	accepted(t, dqReply(w.borrower, id, "accepted", ""), "accepted")
	if s := dqState(t, id); s != "accepted" {
		t.Fatalf("WRONG STATE — accepted, the query is %q", s)
	}
}

func TestDisputes04_ACorrectionNeedsASecondOfficerAndPostsTheReversal(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	c, err := dqPropose(t, w.s1, id, "Recorded against the wrong member", "")
	fixture(t, err, "the treasurer's proposal")
	if s := dqState(t, id); s != "correction_proposed" {
		t.Fatalf("WRONG STATE — proposed, the query is %q", s)
	}
	refused(t, dqDecide(t, w.s1, c, true, "", ""), "the proposer confirming their own correction", refusedAs("dispute_second_officer"))
	if r, _ := dqReversalOf(t, w.entry); r != "" {
		t.Fatalf("POSTED — one officer's proposal reversed the entry (%s); D-13 needs a second officer", r)
	}
	accepted(t, dqDecide(t, w.s2, c, true, "", ""), "the chairman confirming")
	r, mirrored := dqReversalOf(t, w.entry)
	if r == "" || !mirrored {
		t.Fatalf("NOT POSTED — confirmed by a second officer, the reversal is %q (mirrors: %v); want the entry's lines, negated", r, mirrored)
	}
	if s := dqState(t, id); s != "corrected" {
		t.Fatalf("WRONG STATE — confirmed, the query is %q; want corrected", s)
	}
}

func TestDisputes05_EachHalfIsAnOfficersOwnAct(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	_, err := dqPropose(t, w.s1, id, "Recorded against the wrong member", "member_status")
	refused(t, err, "a proposal on another kind of act", refusedAs("dispute_correction_act"))
	_, err = dqPropose(t, w.auditor, id, "Recorded against the wrong member", "")
	refused(t, err, "the internal auditor proposing a correction", refusedAs("dispute_correction_by_officer"))
	c, err := dqPropose(t, w.s1, id, "Recorded against the wrong member", "")
	fixture(t, err, "the treasurer's proposal")
	refused(t, dqDecide(t, w.s2, c, true, "", "member_status"), "a decision on another kind of act", refusedAs("dispute_decision_act"))
	refused(t, dqDecide(t, w.auditor, c, true, "", ""), "the internal auditor deciding a correction", refusedAs("dispute_decision_by_officer"))
	// The member is never either half, though an officer.
	w.saved(t, w.s2Mem, 100000)
	own, err := dqRaise(w.s2, w.s2Mem, w.lastEntry, "My own contribution looks short")
	fixture(t, err, "the chairman's own query")
	_, err = dqPropose(t, w.s2, own, "I recorded it wrongly myself", "")
	refused(t, err, "the chairman proposing a correction of their own entry", refusedAs("dispute_not_own"))
	oc, err := dqPropose(t, w.s1, own, "Recorded against the wrong member", "")
	fixture(t, err, "the treasurer's proposal on the chairman's query")
	refused(t, dqDecide(t, w.s2, oc, true, "", ""), "the chairman confirming a correction of their own entry", refusedAs("dispute_not_own"))
}

func TestDisputes06_ARejectionSaysWhyAndTheQueryIsOpenAgain(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	c, err := dqPropose(t, w.s1, id, "Recorded against the wrong member", "")
	fixture(t, err, "the proposal")
	_, err = dqPropose(t, w.s2, id, "A second proposal for the same query", "")
	refused(t, err, "a second proposal while one waits for a decision", refusedAs("dispute_correction_open"))
	refused(t, dqDecide(t, w.s2, c, false, "", ""), "a rejection that does not say why", refusedAs("dispute_rejection_says_why"))
	accepted(t, dqDecide(t, w.s2, c, false, "The receipt book agrees with the entry", ""), "the chairman rejecting, saying why")
	if r, _ := dqReversalOf(t, w.entry); r != "" {
		t.Fatalf("POSTED — a rejected correction reversed the entry (%s)", r)
	}
	if s := dqState(t, id); s != "reopened" {
		t.Fatalf("WRONG STATE — rejected, the query is %q; want reopened (an officer answers it again)", s)
	}
	refused(t, dqDecide(t, w.s2, c, true, "", ""), "a second decision on one correction", want{codes: []string{"23505"}})
}

func TestDisputes07_AnEntryVFDConfirmedIsNotCorrectedByOfficers(t *testing.T) {
	w := newWalletWorld(t)
	inflow, err := inflowFrom(w.c.s.id, w.socAcct, "", "", 300000)
	fixture(t, err, "VFD's confirmed payment")
	fixture(t, one(`SELECT contribution_post_inflow($1, $2, NULL, 'claim')`, inflow, w.bMem), "the member credited, on VFD's word")
	var entry string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM ledger_entries WHERE vfd_inflow_id = $1`, inflow).Scan(&entry), "the credited entry")
	id, err := dqRaise(w.borrower, w.bMem, entry, "I paid 3,500, not 3,000")
	fixture(t, err, "the query")
	_, err = dqPropose(t, w.s1, id, "Reverse it, the member says otherwise", "")
	refused(t, err, "officers reversing a payment VFD confirmed (the money moved at the bank)", refusedAs("dispute_correctable"))
}

func TestDisputes08_EveryReversalMirrorsItsOriginalOnce(t *testing.T) {
	w := newDisputeWorld(t)
	reverse := func(of string, scale int64) error {
		return tx(func(x pgx.Tx) error {
			var id string
			if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, reverses_id, reason)
				SELECT society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, id, 'Reversing a wrong recording' FROM ledger_entries WHERE id = $1 RETURNING id`, of).Scan(&id); err != nil {
				return err
			}
			_, err := x.Exec(bg(), `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role)
				SELECT $1, account_id, -amount_kobo * $3, member_id, charge_role FROM ledger_lines WHERE entry_id = $2`, id, of, scale)
			return err
		})
	}
	refused(t, reverse(w.entry, 2), "a reversal of twice the original", refusedAs("reversal_mirrors_original"))
	accepted(t, reverse(w.entry, 1), "a reversal of exactly the original")
	refused(t, reverse(w.entry, 1), "a second reversal of the same entry", want{codes: []string{"23505"}})
	r, _ := dqReversalOf(t, w.entry)
	refused(t, reverse(r, 1), "a reversal of a reversal", refusedAs("reversal_of_a_reversal"))
}

func TestDisputes09_NothingIsEdited(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	fixture(t, dqAnswer(w.s1, id, "The receipt shows 2,500 on 5 October"), "the answer")
	fixture(t, dqReply(w.borrower, id, "reopened", "My bank statement shows 3,000"), "reopened")
	c, err := dqPropose(t, w.s1, id, "Recorded against the wrong member", "")
	fixture(t, err, "the proposal")
	fixture(t, dqDecide(t, w.s2, c, false, "The receipt book agrees with the entry", ""), "the rejection")
	for _, q := range []string{
		`UPDATE entry_disputes SET question = 'rewritten afterwards' WHERE id = $1`,
		`UPDATE dispute_answers SET answer = 'rewritten afterwards' WHERE dispute_id = $1`,
		`UPDATE dispute_replies SET note = 'rewritten afterwards' WHERE dispute_id = $1`,
		`UPDATE dispute_corrections SET reason = 'rewritten afterwards' WHERE dispute_id = $1`,
		`UPDATE dispute_correction_decisions SET reason = 'rewritten afterwards' WHERE correction_id IN (SELECT id FROM dispute_corrections WHERE dispute_id = $1)`,
	} {
		refused(t, one(q, id), "a dispute record rewritten: "+q[:40], refusedAs("vfd_records_append_only"))
	}
}

func TestDisputes_Control_AQueryAnsweredAndAccepted(t *testing.T) {
	w := newDisputeWorld(t)
	id := dqMustRaise(t, w)
	accepted(t, dqAnswer(w.s1, id, "The receipt shows 2,500 on 5 October"), "the treasurer's answer")
	accepted(t, dqReply(w.borrower, id, "accepted", ""), "the member accepting it")
}
