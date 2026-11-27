//go:build tier1

// ACCEPTANCE TESTS — 0043, REVERSALS AND ADJUSTMENTS BY TWO OFFICERS (stage
// 3, 2026-11-26; LEDGER_CONTRACT §2).
//
//   - one current officer proposes, on their own act, with a reason; a
//     DIFFERENT one confirms or rejects, on theirs; neither the auditor nor a
//     member the entry names; confirmed, the database posts it;
//   - a reversal is the entry's lines negated; an adjustment's lines balance,
//     none is zero, and they touch only the entry's own accounts and members;
//   - only an entry recorded by hand, not already reversed; one correction
//     waits at a time; a rejection says why; nothing is edited.
package tier1

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

type adjLine struct {
	Account string `json:"account_id"`
	Member  string `json:"member_id,omitempty"`
	Kobo    int64  `json:"amount_kobo"`
}

func cxPropose(t *testing.T, actor, entry, kind, reason string, lines []adjLine, actKind string) (string, error) {
	t.Helper()
	if actKind == "" {
		actKind = "entry_correction"
	}
	var ls any
	if lines != nil {
		b, _ := json.Marshal(lines)
		ls = string(b)
	}
	proof := stepUp(t, actor, actKind)
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, actKind, actor, proof).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO entry_corrections (entry_id, kind, lines, reason, proposed_by, proposed_act) VALUES ($1, $2, $3::jsonb, $4, $5, $6) RETURNING id::text`,
			entry, kind, ls, reason, actor, act).Scan(&id)
	})
	return id, err
}

func cxDecide(t *testing.T, actor, correction string, confirmed bool, reason, actKind string) error {
	t.Helper()
	if actKind == "" {
		actKind = "entry_correction_decision"
	}
	proof := stepUp(t, actor, actKind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, actKind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO entry_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			correction, confirmed, actor, act, nz(reason))
		return err
	})
}

// cxLines: the entry's savings line (the member's) and its other line (the bank).
func cxLines(t *testing.T, entry string) (savings, bank adjLine) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT l.account_id::text, l.member_id::text, l.amount_kobo FROM ledger_lines l WHERE l.entry_id = $1 AND l.member_id IS NOT NULL`, entry).
		Scan(&savings.Account, &savings.Member, &savings.Kobo), "the savings line")
	fixture(t, db.QueryRow(bg(), `SELECT l.account_id::text, l.amount_kobo FROM ledger_lines l WHERE l.entry_id = $1 AND l.member_id IS NULL ORDER BY abs(l.amount_kobo) DESC LIMIT 1`, entry).
		Scan(&bank.Account, &bank.Kobo), "the bank line")
	return
}

const cxWhy = "Recorded twice from the same receipt"

func TestCorrections01_AReversalNeedsASecondOfficer(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	fixture(t, err, "the treasurer's proposal")
	refused(t, cxDecide(t, w.s1, c, true, "", ""), "the proposer confirming their own reversal", refusedAs("entry_second_officer"))
	if r, _ := dqReversalOf(t, w.entry); r != "" {
		t.Fatalf("POSTED — one officer reversed the entry (%s)", r)
	}
	accepted(t, cxDecide(t, w.s2, c, true, "", ""), "the chairman confirming")
	if r, mirrored := dqReversalOf(t, w.entry); r == "" || !mirrored {
		t.Fatalf("NOT POSTED — confirmed, the reversal is %q (mirrors: %v)", r, mirrored)
	}
}

func TestCorrections02_AnAdjustmentCorrectsTheEntrysOwnFigures(t *testing.T) {
	w := newDisputeWorld(t)
	sv, bk := cxLines(t, w.entry)
	// 2,500 recorded where 2,000 was paid: 500 back off the savings and the bank.
	right := []adjLine{{Account: sv.Account, Member: sv.Member, Kobo: 50000}, {Account: bk.Account, Kobo: -50000}}
	_, err := cxPropose(t, w.s1, w.entry, "adjustment", cxWhy, []adjLine{{Account: sv.Account, Member: sv.Member, Kobo: 50000}, {Account: bk.Account, Kobo: -40000}}, "")
	refused(t, err, "an adjustment whose lines do not balance", refusedAs("adjustment_lines_balance"))
	_, err = cxPropose(t, w.s1, w.entry, "adjustment", cxWhy, []adjLine{{Account: sv.Account, Member: sv.Member, Kobo: 0}, {Account: bk.Account, Kobo: 0}}, "")
	refused(t, err, "an adjustment of nothing", refusedAs("adjustment_lines_balance"))
	_, err = cxPropose(t, w.s1, w.entry, "adjustment", cxWhy, []adjLine{{Account: sv.Account, Member: sv.Member, Kobo: 50000}, {Account: w.vat, Kobo: -50000}}, "")
	refused(t, err, "an adjustment moving money to an account the entry never touched", refusedAs("adjustment_on_the_entrys_accounts"))
	_, err = cxPropose(t, w.s1, w.entry, "adjustment", cxWhy, []adjLine{{Account: sv.Account, Member: w.g1Mem, Kobo: -50000}, {Account: sv.Account, Member: sv.Member, Kobo: 50000}}, "")
	refused(t, err, "an adjustment moving savings to a member the entry never named", refusedAs("adjustment_on_the_entrys_accounts"))
	c, err := cxPropose(t, w.s1, w.entry, "adjustment", cxWhy, right, "")
	fixture(t, err, "the adjustment")
	accepted(t, cxDecide(t, w.s2, c, true, "", ""), "the chairman confirming")
	var adj string
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT coalesce((SELECT id::text FROM ledger_entries WHERE adjusts_id = $1), ''),
		(SELECT count(*) FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id WHERE e.adjusts_id = $1
		  AND ((l.account_id = $2 AND l.member_id = $3 AND l.amount_kobo = 50000) OR (l.account_id = $4 AND l.amount_kobo = -50000)))`,
		w.entry, sv.Account, sv.Member, bk.Account).Scan(&adj, &n), "the adjustment posted")
	if adj == "" {
		t.Fatalf("NOT POSTED — confirmed by a second officer, no adjustment of the entry exists")
	}
	if n != 2 {
		t.Fatalf("WRONG ADJUSTMENT — entry %s carries %d of the 2 lines proposed", adj, n)
	}
}

func TestCorrections03_EachHalfIsAnOfficersOwnActNeverTheirOwnEntry(t *testing.T) {
	w := newDisputeWorld(t)
	_, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "member_status")
	refused(t, err, "a proposal on another kind of act", refusedAs("entry_correction_act"))
	_, err = cxPropose(t, w.auditor, w.entry, "reversal", cxWhy, nil, "")
	refused(t, err, "the internal auditor proposing", refusedAs("entry_correction_by_officer"))
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	fixture(t, err, "the proposal")
	refused(t, cxDecide(t, w.s2, c, true, "", "member_status"), "a decision on another kind of act", refusedAs("entry_decision_act"))
	refused(t, cxDecide(t, w.auditor, c, true, "", ""), "the internal auditor deciding", refusedAs("entry_decision_by_officer"))
	// The chairman's own contribution: never proposed or decided by the chairman.
	w.saved(t, w.s2Mem, 100000)
	own := w.lastEntry
	_, err = cxPropose(t, w.s2, own, "reversal", cxWhy, nil, "")
	refused(t, err, "the chairman proposing to reverse their own contribution", refusedAs("entry_correction_not_own"))
	oc, err := cxPropose(t, w.s1, own, "reversal", cxWhy, nil, "")
	fixture(t, err, "the treasurer's proposal on the chairman's entry")
	refused(t, cxDecide(t, w.s2, oc, true, "", ""), "the chairman confirming the reversal of their own contribution", refusedAs("entry_correction_not_own"))
}

func TestCorrections04_OneWaitsAtATimeAndARejectionSaysWhy(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	fixture(t, err, "the proposal")
	_, err = cxPropose(t, w.s2, w.entry, "reversal", "A second proposal on the same entry", nil, "")
	refused(t, err, "a second correction while one waits", refusedAs("entry_correction_waits"))
	refused(t, cxDecide(t, w.s2, c, false, "", ""), "a rejection that does not say why", refusedAs("entry_correction_rejection_says_why"))
	accepted(t, cxDecide(t, w.s2, c, false, "The receipt book agrees with the entry", ""), "the rejection, saying why")
	if r, _ := dqReversalOf(t, w.entry); r != "" {
		t.Fatalf("POSTED — a rejected reversal reversed the entry (%s)", r)
	}
	refused(t, cxDecide(t, w.s2, c, true, "", ""), "a second decision on one correction", want{codes: []string{"23505"}})
	_, err = cxPropose(t, w.s2, w.entry, "reversal", "Proposed again with the bank statement", nil, "")
	accepted(t, err, "a new proposal once the first is decided")
}

func TestCorrections05_OnlyWhatAnOfficerRecordedByHandOnce(t *testing.T) {
	w := newWalletWorld(t)
	inflow, err := inflowFrom(w.c.s.id, w.socAcct, "", "", 300000)
	fixture(t, err, "VFD's confirmed payment")
	fixture(t, one(`SELECT contribution_post_inflow($1, $2, NULL, 'claim')`, inflow, w.bMem), "credited on VFD's word")
	var entry string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM ledger_entries WHERE vfd_inflow_id = $1`, inflow).Scan(&entry), "the credited entry")
	_, err = cxPropose(t, w.s1, entry, "reversal", cxWhy, nil, "")
	refused(t, err, "officers reversing a payment VFD confirmed", refusedAs("entry_correctable"))
	w.saved(t, w.g1Mem, 100000)
	hand := w.lastEntry
	c, err := cxPropose(t, w.s1, hand, "reversal", cxWhy, nil, "")
	fixture(t, err, "the proposal")
	fixture(t, cxDecide(t, w.s2, c, true, "", ""), "confirmed")
	_, err = cxPropose(t, w.s1, hand, "reversal", "Reverse it again", nil, "")
	refused(t, err, "a second reversal of an entry already reversed", refusedAs("entry_correctable"))
}

func TestCorrections06_NothingIsEdited(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	fixture(t, err, "the proposal")
	fixture(t, cxDecide(t, w.s2, c, false, "The receipt book agrees with the entry", ""), "the rejection")
	refused(t, one(`UPDATE entry_corrections SET reason = 'rewritten afterwards' WHERE id = $1`, c), "a proposal rewritten", refusedAs("vfd_records_append_only"))
	refused(t, one(`UPDATE entry_correction_decisions SET confirmed = true WHERE correction_id = $1`, c), "a rejection turned into a confirmation", refusedAs("vfd_records_append_only"))
}

func TestCorrections_Control_ATwoOfficerReversal(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	accepted(t, err, "the treasurer proposing")
	accepted(t, cxDecide(t, w.s2, c, true, "", ""), "the chairman confirming")
}
