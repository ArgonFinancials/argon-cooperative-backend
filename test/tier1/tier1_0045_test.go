//go:build tier1

// ACCEPTANCE TESTS — 0045, A FORGOTTEN PIN AND A CHANGED PHONE (2026-11-28,
// the reviewer's decisions; LEDGER_CONTRACT §12):
//
//   - an ordinary member's PIN is reset by the society's current secretary
//     alone, on their own PIN, with a reason; the old PIN, sessions and
//     devices die with it;
//   - an officer's PIN (any current post, signing or not) needs a request and
//     two OTHER current signatories, each approving on their own PIN with
//     their own reason;
//   - a phone change is the secretary's, on their own PIN, with a reason, the
//     member present and said to be; an officer's number goes the
//     two-signatory way, the request saying the officer is present;
//   - every change of a registered number writes a notice to the OLD number
//     naming the act that changed it, on either path; what came of the
//     notice is recorded once and never edited.
package tier1

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

const rpWhy = "Member forgot their PIN; identity confirmed in person at the meeting"

type rpWorld struct {
	s                        *soc
	sec, chair, treas        string // secretary (not signing), chairman and treasurer (signing)
	loanOfficer, loanMem     string // an officer holding a post that does not sign
	member, memberMem, phone string // an ordinary member with a PIN, a device and a session
}

func newResetWorld(t *testing.T) *rpWorld {
	t.Helper()
	w := &rpWorld{s: newSociety(t)}
	w.sec = w.s.user("Secretary")
	fixture(t, w.s.appoint(w.s.member(w.sec), "secretary", false, "2020-01-01", ""), "the secretary")
	w.chair = w.s.user("Chairman")
	w.s.signatory(w.chair, "chairman")
	w.treas = w.s.user("Treasurer")
	w.s.signatory(w.treas, "treasurer")
	w.loanOfficer = w.s.user("Loan officer")
	w.loanMem = w.s.member(w.loanOfficer)
	fixture(t, w.s.appoint(w.loanMem, "loan_officer", false, "2020-01-01", ""), "a loan officer, not signing")
	w.member = w.s.user("Member")
	w.memberMem = w.s.member(w.member)
	fixture(t, db.QueryRow(bg(), `SELECT phone FROM members WHERE id = $1`, w.memberMem).Scan(&w.phone), "the member's number")
	w.signedIn(t, w.member)
	w.signedIn(t, w.loanOfficer)
	return w
}

// signedIn: a PIN, a device and a live session.
func (w *rpWorld) signedIn(t *testing.T, user string) {
	t.Helper()
	var dev string
	fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, user, argonHash), "a PIN")
	fixture(t, db.QueryRow(bg(), `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id`, user, digest()).Scan(&dev), "a device")
	fixture(t, one(`INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, user, dev, digest()), "a session")
}

// rpAct: `actor` takes a step-up for `kind` and records the act.
type rpA struct {
	kind, actor, member, newPhone, request, reason string
	present                                        *bool
}

func rpAct(t *testing.T, a rpA) (string, error) {
	t.Helper()
	proof := stepUp(t, a.actor, a.kind)
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id, new_phone, request_id, reason, member_present)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			a.kind, a.actor, proof, nz(a.member), nz(a.newPhone), nz(a.request), nz(a.reason), a.present).Scan(&id)
	})
	return id, err
}

var rpYes, rpNo = func() *bool { b := true; return &b }(), func() *bool { b := false; return &b }()

func (w *rpWorld) alive(t *testing.T, user string) (pin bool, sessions, devices int) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM auth_credentials WHERE user_id = $1),
		(SELECT count(*) FROM auth_sessions WHERE user_id = $1 AND ended_at IS NULL),
		(SELECT count(*) FROM auth_devices WHERE user_id = $1 AND revoked_at IS NULL)`, user).Scan(&pin, &sessions, &devices), "what the person still holds")
	return
}

func phoneOf(t *testing.T, member string) string {
	t.Helper()
	var p string
	fixture(t, db.QueryRow(bg(), `SELECT phone FROM members WHERE id = $1`, member).Scan(&p), "the number")
	return p
}

type rpNotice struct{ id, old, new, actor, kind string }

func noticesFor(t *testing.T, member string) []rpNotice {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT n.id::text, n.old_phone, n.new_phone, a.actor_user_id::text, a.kind
		FROM phone_change_notices n JOIN credential_acts a ON a.id = n.act_id WHERE n.member_id = $1 ORDER BY n.at`, member)
	fixture(t, err, "the notices")
	defer rows.Close()
	var out []rpNotice
	for rows.Next() {
		var n rpNotice
		fixture(t, rows.Scan(&n.id, &n.old, &n.new, &n.actor, &n.kind), "a notice")
		out = append(out, n)
	}
	return out
}

// officerPath: a request for `kind` about the loan officer, then approvals.
func (w *rpWorld) officerPath(t *testing.T, kind, newPhone string, approvers ...string) (string, error) {
	t.Helper()
	req, err := rpAct(t, rpA{kind: kind, actor: w.chair, member: w.loanMem, newPhone: newPhone, reason: rpWhy, present: rpYes})
	if err != nil {
		return "", err
	}
	for _, by := range approvers {
		if _, err := rpAct(t, rpA{kind: "signatory_approval", actor: by, request: req, reason: "I know them; they asked me in person today"}); err != nil {
			return req, err
		}
	}
	return req, nil
}

func TestResetPhone02_OnlyTheSecretaryResetsAMember(t *testing.T) {
	w := newResetWorld(t)
	for _, who := range []string{w.treas, w.chair} {
		_, err := rpAct(t, rpA{kind: "member_reset", actor: who, member: w.memberMem, reason: rpWhy})
		refused(t, err, "an officer who is not the secretary resetting a member's PIN", refusedAs("reset_by_secretary"))
	}
	if pin, _, _ := w.alive(t, w.member); !pin {
		t.Fatalf("RESET — a refused reset still took the member's PIN")
	}
}

func TestResetPhone03_AnOfficersPINIsNeverOneOfficersEvenANonSigningOne(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "member_reset", actor: w.sec, member: w.loanMem, reason: rpWhy})
	refused(t, err, "the secretary alone resetting a loan officer's PIN (an officer's PIN opens more than their own savings)", refusedAs("signatory_reset_needs_two"))
}

func TestResetPhone04_TwoOtherSignatoriesResetAnOfficer(t *testing.T) {
	w := newResetWorld(t)
	_, err := w.officerPath(t, "signatory_reset_request", "", w.chair)
	accepted(t, err, "a request and one approval")
	if pin, _, _ := w.alive(t, w.loanOfficer); !pin {
		t.Fatalf("RESET — one approval reset an officer's PIN; two are needed")
	}
	w2 := newResetWorld(t)
	_, err = w2.officerPath(t, "signatory_reset_request", "", w2.chair, w2.treas)
	accepted(t, err, "a request for a non-signing officer, approved by two signatories each with a reason")
	if pin, s, d := w2.alive(t, w2.loanOfficer); pin || s != 0 || d != 0 {
		t.Fatalf("STILL ALIVE — two signatories approved; the officer keeps PIN=%v, %d sessions, %d devices", pin, s, d)
	}
}

func TestResetPhone05_EachApprovingSignatoryStatesTheirReason(t *testing.T) {
	w := newResetWorld(t)
	req, err := rpAct(t, rpA{kind: "signatory_reset_request", actor: w.chair, member: w.loanMem, reason: rpWhy})
	fixture(t, err, "the request")
	_, err = rpAct(t, rpA{kind: "signatory_approval", actor: w.treas, request: req})
	refused(t, err, "an approval that gives no reason", refusedAs("approval_states_reason"))
}

func TestResetPhone06_OnlyTheSecretaryChangesANumber(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.treas, member: w.memberMem, newPhone: unregistered(), reason: rpWhy, present: rpYes})
	refused(t, err, "the treasurer changing a member's number", refusedAs("phone_change_by_secretary"))
	if phoneOf(t, w.memberMem) != w.phone {
		t.Fatalf("CHANGED — a refused change moved the number")
	}
}

func TestResetPhone07_TheMemberIsPresentAndTheActSaysSo(t *testing.T) {
	w := newResetWorld(t)
	for _, p := range []*bool{nil, rpNo} {
		_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: unregistered(), reason: rpWhy, present: p})
		refused(t, err, "a phone change not saying the member is present", refusedAs("phone_change_member_present"))
	}
	fresh := unregistered()
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: fresh, reason: rpWhy, present: rpYes})
	accepted(t, err, "the secretary's change, the member present")
	if got := phoneOf(t, w.memberMem); got != fresh {
		t.Fatalf("NOT APPLIED — the number is %s; want %s", got, fresh)
	}
}

func TestResetPhone08_AnOfficersNumberNeedsTwoEvenANonSigningOne(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.loanMem, newPhone: unregistered(), reason: rpWhy, present: rpYes})
	refused(t, err, "the secretary alone changing a loan officer's number", refusedAs("signatory_phone_needs_two"))
	_, err = rpAct(t, rpA{kind: "signatory_phone_request", actor: w.chair, member: w.loanMem, newPhone: unregistered(), reason: rpWhy, present: rpNo})
	refused(t, err, "an officer's phone request not saying the officer is present", refusedAs("phone_change_member_present"))
}

func TestResetPhone09_TheOldNumberIsToldWhoChangedIt(t *testing.T) {
	w := newResetWorld(t)
	fresh := unregistered()
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: fresh, reason: rpWhy, present: rpYes})
	accepted(t, err, "the secretary's change")
	n := noticesFor(t, w.memberMem)
	if len(n) != 1 || n[0].old != w.phone || n[0].new != fresh || n[0].actor != w.sec || n[0].kind != "phone_change" {
		t.Fatalf("UNTOLD — after the change the notices are %+v; want one, to the old number %s, naming the secretary's act", n, w.phone)
	}
}

func TestResetPhone10_TheOldNumberIsToldOnTheTwoSignatoryPathToo(t *testing.T) {
	w := newResetWorld(t)
	old, fresh := phoneOf(t, w.loanMem), unregistered()
	_, err := w.officerPath(t, "signatory_phone_request", fresh, w.chair, w.treas)
	accepted(t, err, "an officer's number changed by a request and two signatories")
	n := noticesFor(t, w.loanMem)
	if len(n) != 1 || n[0].old != old || n[0].new != fresh || n[0].actor != w.treas || n[0].kind != "signatory_approval" {
		t.Fatalf("UNTOLD — after the change the notices are %+v; want one, to %s, naming the approval that completed it", n, old)
	}
}

func TestResetPhone11_WhatCameOfTheNoticeIsRecordedOnce(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: unregistered(), reason: rpWhy, present: rpYes})
	fixture(t, err, "the change")
	n := noticesFor(t, w.memberMem)
	if len(n) != 1 {
		t.Fatalf("UNTOLD — %d notices for one change", len(n))
	}
	accepted(t, one(`INSERT INTO phone_change_notice_sends (notice_id, outcome) VALUES ($1, 'not_reached')`, n[0].id), "recording the old phone not reached")
	refused(t, one(`INSERT INTO phone_change_notice_sends (notice_id, outcome, provider_message_id) VALUES ($1, 'accepted', 'm-1')`, n[0].id),
		"a second outcome for one notice (an unreachable phone written over as told)", want{codes: []string{"23505"}, constraint: "phone_notice_sent_once"})
}

func TestResetPhone12_NoticesAndWhatCameOfThemAreNeverEdited(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: unregistered(), reason: rpWhy, present: rpYes})
	fixture(t, err, "the change")
	n := noticesFor(t, w.memberMem)
	if len(n) != 1 {
		t.Fatalf("UNTOLD — %d notices for one change", len(n))
	}
	fixture(t, one(`INSERT INTO phone_change_notice_sends (notice_id, outcome) VALUES ($1, 'refused')`, n[0].id), "an outcome")
	refused(t, one(`UPDATE phone_change_notices SET old_phone = new_phone WHERE id = $1`, n[0].id), "a notice redirected", refusedAs("phone_notice_append_only"))
	refused(t, one(`UPDATE phone_change_notice_sends SET outcome = 'accepted', provider_message_id = 'm-1' WHERE notice_id = $1`, n[0].id),
		"an unreachable phone rewritten as told", refusedAs("phone_notice_append_only"))
	refused(t, one(`DELETE FROM phone_change_notices WHERE id = $1`, n[0].id), "a notice removed", refusedAs("phone_notice_append_only"))
}

// The control: the decided path works end to end on any schema that has it.
func TestResetPhone_Control_TheSecretaryResetsAMemberAndChangesTheirNumber(t *testing.T) {
	w := newResetWorld(t)
	_, err := rpAct(t, rpA{kind: "member_reset", actor: w.sec, member: w.memberMem, reason: rpWhy})
	accepted(t, err, "the secretary resetting an ordinary member's PIN, with a reason")
	if pin, s, d := w.alive(t, w.member); pin || s != 0 || d != 0 {
		t.Fatalf("STILL ALIVE — after the reset the member keeps PIN=%v, %d sessions, %d devices; the old PIN dies with the act", pin, s, d)
	}
	_, err = rpAct(t, rpA{kind: "phone_change", actor: w.sec, member: w.memberMem, newPhone: unregistered(), reason: rpWhy, present: rpYes})
	accepted(t, err, "the secretary changing the member's number, the member present")
}
