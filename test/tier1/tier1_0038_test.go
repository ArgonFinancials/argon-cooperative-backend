//go:build tier1

// ACCEPTANCE TESTS — 0038, SUSPENDING A MEMBER AND RECORDING ONE WHO LEAVES
// (2026-11-24, the reviewer: "The secretary records them, on their own PIN,
// with a reason"). A status changes only through a recorded change, by the
// society's current secretary on their own member_status act taken with it,
// never their own, with a reason; active and suspended move to each other,
// either may leave, leaving is final; an officer is not suspended or exited
// while holding a post; the record is never edited.
package tier1

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

type sworld struct {
	s                 *soc
	sec, secMem       string // the current secretary
	treas, treasMem   string // a current officer who is not the secretary
	member, memberMem string // an ordinary member, active
}

func newStatusWorld(t *testing.T) *sworld {
	t.Helper()
	w := &sworld{s: newSociety(t)}
	w.sec = w.s.user("Secretary")
	w.secMem = w.s.member(w.sec)
	fixture(t, w.s.appoint(w.secMem, "secretary", false, "2020-01-01", ""), "the secretary")
	w.treas = w.s.user("Treasurer")
	w.treasMem = w.s.signatory(w.treas, "treasurer")
	w.member = w.s.user("Member")
	w.memberMem = w.s.member(w.member)
	for _, m := range []string{w.secMem, w.treasMem, w.memberMem} {
		fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, m), "an active member")
	}
	return w
}

// change: `actor` takes a step-up for `kind` ("" = member_status) and, in one
// transaction (unless apart), records the act and the change.
func change(t *testing.T, actor, member, to, reason, kind string, apart bool) error {
	t.Helper()
	if kind == "" {
		kind = "member_status"
	}
	proof := stepUp(t, actor, kind)
	var act string
	mk := func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act)
	}
	rec := func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `INSERT INTO member_status_changes (member_id, to_status, reason, act_id) VALUES ($1, $2::member_status, $3, $4)`, member, to, reason, act)
		return err
	}
	if apart {
		if err := tx(mk); err != nil {
			return err
		}
		return tx(rec)
	}
	return tx(func(x pgx.Tx) error {
		if err := mk(x); err != nil {
			return err
		}
		return rec(x)
	})
}

func TestStatusChange01_TheSecretarySuspendsAndReinstatesWithAReason(t *testing.T) {
	w := newStatusWorld(t)
	accepted(t, change(t, w.sec, w.memberMem, "suspended", "Three months' contributions unpaid", "", false), "the secretary suspending a member")
	var from, to, why string
	fixture(t, db.QueryRow(bg(), `SELECT from_status::text, to_status::text, reason FROM member_status_changes WHERE member_id = $1`, w.memberMem).Scan(&from, &to, &why), "the record")
	if s := statusOf(t, w.memberMem); s != "suspended" || from != "active" || to != "suspended" || why != "Three months' contributions unpaid" {
		t.Fatalf("NOT APPLIED — the member is %q, the record %s -> %s (%q); want suspended, active -> suspended, with the reason", s, from, to, why)
	}
	accepted(t, change(t, w.sec, w.memberMem, "active", "Arrears paid", "", false), "the secretary reinstating the member")
	if s := statusOf(t, w.memberMem); s != "active" {
		t.Fatalf("NOT APPLIED — after reinstatement the member is %q; want active", s)
	}
}

func TestStatusChange02_OnlyTheCurrentSecretaryOnTheirOwnActTakenWithIt(t *testing.T) {
	w := newStatusWorld(t)
	refused(t, change(t, w.treas, w.memberMem, "suspended", "x", "", false), "the treasurer suspending a member", refusedAs("member_status_by_secretary"))
	refused(t, change(t, w.sec, w.memberMem, "suspended", "x", "member_email", false), "a suspension on another kind of act", refusedAs("member_status_act"))
	refused(t, change(t, w.sec, w.memberMem, "suspended", "x", "", true), "a suspension on an act from another transaction", refusedAs("member_status_act"))
}

func TestStatusChange03_TheSecretaryNeverChangesTheirOwn(t *testing.T) {
	w := newStatusWorld(t)
	// Reinstating their own suspended record: a move allowed, and no post rule
	// applies to it, so only "never their own" refuses it.
	fixture(t, one(`UPDATE members SET status = 'suspended' WHERE id = $1`, w.secMem), "a suspended secretary record")
	refused(t, change(t, w.sec, w.secMem, "active", "Reinstating myself", "", false), "the secretary reinstating their own record", refusedAs("member_status_not_self"))
}

func TestStatusChange04_AChangeSaysWhy(t *testing.T) {
	w := newStatusWorld(t)
	refused(t, change(t, w.sec, w.memberMem, "suspended", "  ", "", false), "a suspension with no reason", refusedAs("member_status_says_why"))
}

func TestStatusChange05_LeavingIsFinalAndOnlyTheseMovesAreMade(t *testing.T) {
	w := newStatusWorld(t)
	refused(t, change(t, w.sec, w.memberMem, "active", "Already active", "", false), "an active member made active", refusedAs("member_status_move"))
	accepted(t, change(t, w.sec, w.memberMem, "exited", "Resigned by letter", "", false), "a member leaving")
	refused(t, change(t, w.sec, w.memberMem, "active", "Came back", "", false), "a member who left made active again", refusedAs("member_status_move"))
}

func TestStatusChange06_AnOfficerIsNotSuspendedWhileHoldingAPost(t *testing.T) {
	w := newStatusWorld(t)
	refused(t, change(t, w.sec, w.treasMem, "suspended", "Absent", "", false), "a signing officer suspended in post", refusedAs("member_status_holds_a_post"))
}

func TestStatusChange07_TheRecordIsNeverEdited(t *testing.T) {
	w := newStatusWorld(t)
	fixture(t, change(t, w.sec, w.memberMem, "suspended", "Arrears", "", false), "a suspension")
	refused(t, one(`UPDATE member_status_changes SET reason = 'nothing happened' WHERE member_id = $1`, w.memberMem), "a reason rewritten", refusedAs("member_status_change_append_only"))
	refused(t, one(`DELETE FROM member_status_changes WHERE member_id = $1`, w.memberMem), "a record deleted", refusedAs("member_status_change_append_only"))
}

// The control: the secretary records an ordinary member leaving.
func TestStatusChange_Control_TheSecretaryRecordsAMemberLeaving(t *testing.T) {
	w := newStatusWorld(t)
	accepted(t, change(t, w.sec, w.memberMem, "exited", "Moved to Abuja", "", false), "a member leaving, recorded by the secretary")
}
