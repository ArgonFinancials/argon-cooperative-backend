//go:build tier1

// Suite 0034 — OFFICERS ENROL BY EMAIL. Written before the migration
// (2026-11-20). The user's ruling (LEDGER_CONTRACT §12): officers do not meet
// in person, so their enrolment code goes to their email address; everything
// else about the code stays (one use, expiry, voided on reissue and by five
// wrong tries, a first PIN only). The slip stays for members with no email
// and for founding officers.
//
//	THE EMAIL: on the member's record, set only by a current secretary on
//	their own act (member_email, or member_register at registration) in that
//	transaction; one address, lower-cased; who set it and when recorded.
//	AN EMAILED CODE: lasts 72 hours (enrolment_email_window, PROVISIONAL);
//	goes to the member's own recorded email and no other address; never from
//	the operator; never issued (either half) by the officer who set that
//	email; a second half goes the way the first went.
//	A SEND is recorded without the code: sent (naming its code) or failed
//	(saying why), never edited.
package tier1

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type mlworld struct {
	*hworld
}

func newEmailWorld(t *testing.T) *mlworld {
	t.Helper()
	return &mlworld{hworld: newHalvesWorld(t)}
}

// setEmail: `actor` takes a step-up for `kind` and, in one transaction (unless
// apart), records the act and sets member's email. kind "": member_email.
func setEmail(t *testing.T, actor, member, email, kind string, apart bool) error {
	t.Helper()
	if kind == "" {
		kind = "member_email"
	}
	proof := stepUp(t, actor, kind)
	var act string
	mk := func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act)
	}
	upd := func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `UPDATE members SET email = $2, email_set_by_act = $3 WHERE id = $1`, member, nz(email), act)
		return err
	}
	if apart {
		if err := tx(mk); err != nil {
			return err
		}
		return tx(upd)
	}
	return tx(func(x pgx.Tx) error {
		if err := mk(x); err != nil {
			return err
		}
		return upd(x)
	})
}

func emailOf(t *testing.T, member string) (email, setBy string, setAt *time.Time) {
	t.Helper()
	var e, by *string
	fixture(t, db.QueryRow(bg(), `SELECT m.email, a.actor_user_id::text, m.email_set_at FROM members m
		LEFT JOIN credential_acts a ON a.id = m.email_set_by_act WHERE m.id = $1`, member).Scan(&e, &by, &setAt), "the member's email")
	if e != nil {
		email = *e
	}
	if by != nil {
		setBy = *by
	}
	return
}

// emailed: `actor` issues an emailed whole code to (user, member).
func emailed(t *testing.T, actor, user, member string) (string, error) {
	t.Helper()
	_, d := slip()
	return issueBy(t, issueSpec{actor: actor, actMember: member, user: user, member: member, digest: d,
		extraCol: ", delivery", extra: ", 'email'"})
}

func codeLife(t *testing.T, id string) (life time.Duration, delivery string, sentTo *string) {
	t.Helper()
	var issued, expires time.Time
	fixture(t, db.QueryRow(bg(), `SELECT issued_at, expires_at, delivery, sent_to FROM enrolment_codes WHERE id = $1`, id).
		Scan(&issued, &expires, &delivery, &sentTo), "the code")
	return expires.Sub(issued), delivery, sentTo
}

func uniqueEmail(who string) string { return fmt.Sprintf("%s.%d@example.com", who, seq.Add(1)) }

// ---- the email ----------------------------------------------------------------------

func TestEmail01_OnlyTheCurrentSecretarySetsAnEmailOnTheirOwnAct(t *testing.T) {
	w := newEmailWorld(t)
	refused(t, setEmail(t, w.treas, w.personMem, uniqueEmail("p"), "", false), "the treasurer sets a member's email", refusedAs("email_by_secretary"))
	refused(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "contribution_record", false), "an email set on another kind of act", refusedAs("email_act"))
	refused(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", true), "an email set on an act from another transaction", refusedAs("email_act"))
	refused(t, one(`UPDATE members SET email = $2 WHERE id = $1`, w.personMem, uniqueEmail("p")), "an email set with no act at all", refusedAs("email_act"))
	accepted(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the secretary sets it on their own act")
}

func TestEmail02_AnEmailIsOneAddressLowerCased(t *testing.T) {
	w := newEmailWorld(t)
	for _, bad := range []string{"not an email", "two@@example.com", "a@b", "a b@example.com", "x@example.com,y@example.com"} {
		refused(t, setEmail(t, w.sec, w.personMem, bad, "", false), fmt.Sprintf("the address %q", bad), refusedAs("member_email_shape"))
	}
	addr := fmt.Sprintf("Officer.%d@Example.COM", seq.Add(1))
	accepted(t, setEmail(t, w.sec, w.personMem, "  "+addr+" ", "", false), "an address with capitals and spaces")
	if got, _, _ := emailOf(t, w.personMem); got != toLower(addr) {
		t.Fatalf("UNNORMALISED — stored %q, want %q", got, toLower(addr))
	}
}

func TestEmail03_WhoSetItAndWhenIsRecordedClearingToo(t *testing.T) {
	w := newEmailWorld(t)
	fixture(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the email")
	_, by, at := emailOf(t, w.personMem)
	if by != w.sec || at == nil {
		t.Fatalf("UNATTRIBUTED — set by %q at %v; want the secretary, with a time", by, at)
	}
	accepted(t, setEmail(t, w.sec, w.personMem, "", "", false), "the secretary clears it")
	e, by, at := emailOf(t, w.personMem)
	if e != "" || by != w.sec || at == nil {
		t.Fatalf("UNATTRIBUTED CLEAR — email %q, set by %q at %v", e, by, at)
	}
	// The record of who set it never changes without the email changing.
	refused(t, one(`UPDATE members SET email_set_at = now() - interval '1 year' WHERE id = $1`, w.personMem), "the time it was set rewritten", refusedAs("email_act"))
}

// ---- an emailed code ------------------------------------------------------------------

func TestEmail04_AnEmailedCodeLasts72HoursASlip30Days(t *testing.T) {
	w := newEmailWorld(t)
	fixture(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the email")
	id, err := emailed(t, w.treas, w.person, w.personMem)
	accepted(t, err, "an emailed code")
	if life, _, _ := codeLife(t, id); life != 72*time.Hour {
		t.Fatalf("WRONG LIFE — an emailed code lives %v, not 72 hours", life)
	}
	sid, _ := w.officerIssues(t, w.other, w.otherMem)
	if life, _, _ := codeLife(t, sid); life != 30*24*time.Hour {
		t.Fatalf("WRONG LIFE — a slip lives %v, not 30 days", life)
	}
}

func TestEmail05_AnEmailedCodeGoesToTheMembersOwnEmailAndNoOther(t *testing.T) {
	w := newEmailWorld(t)
	_, err := emailed(t, w.treas, w.person, w.personMem)
	refused(t, err, "an emailed code for a member with no email", refusedAs("enrolment_email_needs_address"))
	addr := uniqueEmail("person")
	fixture(t, setEmail(t, w.sec, w.personMem, addr, "", false), "the email")
	_, d := slip()
	id, err := issueBy(t, issueSpec{actor: w.treas, actMember: w.personMem, user: w.person, member: w.personMem, digest: d,
		extraCol: ", delivery, sent_to", extra: ", 'email', 'attacker@example.net'"})
	accepted(t, err, "an emailed code naming another address")
	if _, del, to := codeLife(t, id); del != "email" || to == nil || *to != addr {
		got := "nothing"
		if to != nil {
			got = *to
		}
		t.Fatalf("WRONG ADDRESS — the code was recorded as sent to %s; the member's email is %s", got, addr)
	}
}

func TestEmail06_WhoSetTheEmailDoesNotIssueTheEmailedCode(t *testing.T) {
	w := newEmailWorld(t)
	// A member's whole code: the secretary (an officer) could issue it by slip.
	fixture(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the email, by the secretary")
	_, err := emailed(t, w.sec, w.person, w.personMem)
	refused(t, err, "the secretary emails a code to the address they set", refusedAs("enrolment_email_set_by_issuer"))
	_, err = emailed(t, w.treas, w.person, w.personMem)
	accepted(t, err, "another officer emails it")
}

func TestEmail07_TheOperatorNeverEmailsACode(t *testing.T) {
	w := newEmailWorld(t)
	fixture(t, setEmail(t, w.sec, w.targetMem, uniqueEmail("t"), "", false), "the target's email")
	_, d := slip()
	refused(t, tx(func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_operator, operator_login, operator_reason, delivery)
			VALUES ($1, $2, $3, 'Operator', 'op@host', 'handover', 'email')`, w.target, w.targetMem, d)
		return err
	}), "the operator emails a founding officer's code", refusedAs("enrolment_email_by_officer"))
}

func TestEmail08_HalvesGoTheWayTheFirstWentAndNotFromTheSetter(t *testing.T) {
	w := newEmailWorld(t)
	// The secretary (who does not sign) sets the email; two signatories email the halves.
	fixture(t, setEmail(t, w.sec, w.targetMem, uniqueEmail("t"), "", false), "the target's email")
	_, d1 := slip()
	var id string
	err := tx(func(x pgx.Tx) error {
		proof := stepUp(t, w.chair, "enrolment_issue")
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
			w.chair, proof, w.targetMem).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act, delivery) VALUES ($1, $2, $3, $4, 'email') RETURNING id`,
			w.target, w.targetMem, d1, act).Scan(&id)
	})
	accepted(t, err, "the chairman emails the first half")
	refused(t, one(`UPDATE enrolment_codes SET delivery = 'slip' WHERE id = $1`, id), "the code's delivery changed after its first half", refusedAs("enrolment_code_append_only"))
	_, d2 := slip()
	accepted(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, false), "the treasurer emails the second half")
	if life, del, to := codeLife(t, id); del != "email" || to == nil || life != 72*time.Hour {
		t.Fatalf("WRONG — the halved code reads %s to %v, living %v", del, to, life)
	}
}

func TestEmail09_TheSetterIssuesNeitherHalf(t *testing.T) {
	// A society whose secretary SIGNS: the one who sets an email could otherwise issue a half.
	e := newEnrolWorld(t)
	w := &mlworld{hworld: &hworld{eworld: e}}
	w.treas, w.treasMem = e.officer, e.officerMem
	w.chair = e.s.user("Chairman (signatory)")
	w.chairMem = e.s.signatory(w.chair, "chairman")
	signSec := e.s.user("Secretary who signs")
	e.s.signatory(signSec, "secretary")
	w.target = e.s.user("Branch officer (signatory, enrolling)")
	w.targetMem = e.s.signatory(w.target, "branch_officer")
	fixture(t, setEmail(t, signSec, w.targetMem, uniqueEmail("t"), "", false), "the email, by the signing secretary")
	_, err := w.firstHalfEmailed(t, signSec)
	refused(t, err, "the setter emails the first half", refusedAs("enrolment_email_set_by_issuer"))
	id, err := w.firstHalfEmailed(t, w.chair)
	fixture(t, err, "the chairman emails the first half")
	_, d2 := slip()
	refused(t, w.secondHalf(t, signSec, w.targetMem, id, d2, false), "the setter emails the second half", refusedAs("enrolment_email_set_by_issuer"))
	accepted(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, false), "the treasurer emails the second half")
}

func (w *mlworld) firstHalfEmailed(t *testing.T, actor string) (string, error) {
	t.Helper()
	_, d := slip()
	var id string
	err := tx(func(x pgx.Tx) error {
		proof := stepUp(t, actor, "enrolment_issue")
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
			actor, proof, w.targetMem).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act, delivery) VALUES ($1, $2, $3, $4, 'email') RETURNING id`,
			w.target, w.targetMem, d, act).Scan(&id)
	})
	return id, err
}

// ---- a send -------------------------------------------------------------------------

func emailSend(member, code, outcome, detail string) error {
	return one(`INSERT INTO email_sends (member_id, code_id, kind, sent_to, outcome, detail) VALUES ($1, $2, 'whole', 'x@example.com', $3, $4)`,
		member, nz(code), outcome, nz(detail))
}

func TestEmail10_ASendIsRecordedWithoutTheCodeAndNeverEdited(t *testing.T) {
	w := newEmailWorld(t)
	fixture(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the email")
	id, err := emailed(t, w.treas, w.person, w.personMem)
	fixture(t, err, "an emailed code")
	refused(t, emailSend(w.personMem, "", "sent", ""), "a send recorded as sent naming no code", refusedAs("email_send_says_what"))
	refused(t, emailSend(w.personMem, "", "failed", ""), "a failed send saying nothing", refusedAs("email_send_says_what"))
	accepted(t, emailSend(w.personMem, id, "sent", ""), "a send of the code")
	accepted(t, emailSend(w.personMem, "", "failed", "SES: MessageRejected"), "a failed send, saying why")
	// A change only the record's own rule forbids (a reason rewritten); turning a
	// failure into "sent" is also refused by the shape of a send.
	refused(t, one(`UPDATE email_sends SET detail = 'it went fine' WHERE outcome = 'failed' AND member_id = $1`, w.personMem), "a failure's reason rewritten", refusedAs("email_send_append_only"))
	refused(t, one(`DELETE FROM email_sends WHERE member_id = $1`, w.personMem), "the record deleted", refusedAs("email_send_append_only"))
}

// An officer's email set by the secretary; another officer emails the code;
// the slip still works for a member with no email.
func TestEmail_Control_TheSecretarySetsAnotherOfficerEmailsTheSlipStays(t *testing.T) {
	w := newEmailWorld(t)
	accepted(t, setEmail(t, w.sec, w.personMem, uniqueEmail("p"), "", false), "the email")
	_, err := emailed(t, w.treas, w.person, w.personMem)
	accepted(t, err, "the treasurer emails the code")
	// A slip, for a member with no email: asserted, not a fixture, because the
	// slip still working is part of what this control claims.
	_, d := slip()
	_, err = issueBy(t, issueSpec{actor: w.treas, actMember: w.otherMem, user: w.other, member: w.otherMem, digest: d})
	accepted(t, err, "a slip for a member with no email")
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
