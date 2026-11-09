//go:build tier1

// Suite 0024 — A FIRST DEVICE BY ENROLMENT CODE. Written before the
// migration, for review.
//
// Decided 2026-11-06 (LEDGER_CONTRACT.md §12): a person's first device is
// trusted by an ENROLMENT CODE handed over on a slip, not by SMS. The operator
// issues the founding officers' codes at handover; an officer issues everyone
// else's. The code's rules:
//   - ONE LIVE CODE PER PERSON. A new code voids the live one ("reissued"):
//     a lost slip must not strand a member for 30 days, and the old slip
//     stops working the moment it is replaced. EACH REPLACEMENT IS
//     ATTRIBUTABLE in its own right: who issued it, when, which code it
//     replaced, so a slip reissued three times for one person is visible.
//   - SINGLE USE; 30 DAYS, set by the server; STORED AS A DIGEST.
//   - VOID AFTER 5 WRONG TRIES, and those tries NEVER count against the PIN
//     lockout (they are the code's, not the PIN's).
//   - NOBODY ISSUES THEIR OWN (§10), through any member record. An officer
//     issues on their own fresh step-up; not the read-only auditor.
//   - The operator's path is for the founding officers only, and names the
//     operator, the login and why, as every operator act (0014).
//
// The PIN and the device a redeemed code leads to are the next layer's (the
// auth service): a code sets a PIN only for someone who has none, never in
// place of a reset (D-15), and is otherwise the second factor beside the PIN.
package tier1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- the world --------------------------------------------------------------

type eworld struct {
	s                      *soc
	officer, officerMem    string // the treasurer: a current, non-auditor post
	auditor                string // the internal auditor (read-only by Directorate rule)
	person, personMem      string // an ordinary member with no device yet
	other, otherMem        string // another ordinary member
}

func newEnrolWorld(t *testing.T) *eworld {
	t.Helper()
	w := &eworld{s: newSociety(t)}
	w.officer = w.s.user("Treasurer (issuer)")
	w.officerMem = w.s.member(w.officer)
	fixture(t, w.s.appoint(w.officerMem, "treasurer", true, "2020-01-01", ""), "the treasurer")
	w.auditor = w.s.user("Internal auditor")
	fixture(t, w.s.appoint(w.s.member(w.auditor), "internal_auditor", false, "2020-01-01", ""), "the auditor")
	w.person = w.s.user("Member (enrolling)")
	w.personMem = w.s.member(w.person)
	w.other = w.s.user("Member (another)")
	w.otherMem = w.s.member(w.other)
	return w
}

func digestOf(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// slip: a fresh code's text and digest, unique per call.
func slip() (string, string) {
	c := fmt.Sprintf("ENROL-%d-%d", time.Now().UnixNano(), seq.Add(1))
	return c, digestOf(c)
}

// issue: `actor` takes a step-up for enrolment_issue and, in one transaction,
// records the act for `actMember` and the code for (user, member). An honest
// issue has actMember == member.
type issueSpec struct {
	actor, actMember, user, member, digest string
	extra                                  string // extra column assignment for the code row, e.g. ", wrong_tries" / ", 2"
	extraCol                               string
	noAct                                  bool
}

func issueBy(t *testing.T, sp issueSpec) (string, error) {
	t.Helper()
	proof := stepUp(t, sp.actor, "enrolment_issue")
	var id string
	err := tx(func(x pgx.Tx) error {
		var act any
		if !sp.noAct {
			var a string
			if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
				sp.actor, proof, sp.actMember).Scan(&a); err != nil {
				return err
			}
			act = a
		}
		return x.QueryRow(bg(), `INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_act`+sp.extraCol+`)
			VALUES ($1, $2, $3, $4`+sp.extra+`) RETURNING id`, sp.user, sp.member, sp.digest, act).Scan(&id)
	})
	return id, err
}

// officerIssues: the treasurer issues the person a code; returns its id and text.
func (w *eworld) officerIssues(t *testing.T, user, member string) (string, string) {
	t.Helper()
	code, d := slip()
	id, err := issueBy(t, issueSpec{actor: w.officer, actMember: member, user: user, member: member, digest: d})
	fixture(t, err, "an officer's code")
	return id, code
}

func operatorIssue(member, digest, operator, login, reason string) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `SELECT operator_issue_enrolment($1, $2, $3, $4, $5)::text`,
			member, digest, nz(operator), nz(login), nz(reason)).Scan(&id)
	})
	return id, err
}

const enrolWhy = "Handing the founding treasurer their enrolment slip"

// redeemSQL: the redemption the schema has. 0024's took (user, digest); 0025
// replaced it with one that also trusts and records the device (a fresh token
// digest per call). This suite's rules are the code's, the same in both, so
// its mutation runs (0024's base) and GREEN (every migration) call each the
// one that exists. $1 user, $2 digest; returns the code's id or NULL.
func redeemSQL(t *testing.T) string {
	t.Helper()
	var has bool
	fixture(t, db.QueryRow(bg(), `SELECT to_regprocedure('enrolment_redeem(uuid,text,text)') IS NOT NULL`).Scan(&has), "the redemption's shape")
	if has {
		return `SELECT code_id::text FROM enrolment_redeem($1, $2, '` + digest() + `')`
	}
	return `SELECT enrolment_redeem($1, $2)::text`
}

// redeem: the code the person typed, as the auth service would check it.
// Returns the code's id, or "" when it is not accepted.
func redeem(t *testing.T, user, code string) string {
	t.Helper()
	var id *string
	fixture(t, db.QueryRow(bg(), redeemSQL(t), user, digestOf(code)).Scan(&id), "a redemption")
	if id == nil {
		return ""
	}
	return *id
}

type codeRow struct {
	used, voided           bool
	voidReason, replaces   string
	tries                  int
	issuedByAct, operator  string
}

func codeState(t *testing.T, id string) codeRow {
	t.Helper()
	var r codeRow
	var reason, replaces, act, op *string
	fixture(t, db.QueryRow(bg(), `SELECT used_at IS NOT NULL, voided_at IS NOT NULL, void_reason, replaces_id::text, wrong_tries,
		issued_by_act::text, issued_by_operator FROM enrolment_codes WHERE id = $1`, id).
		Scan(&r.used, &r.voided, &reason, &replaces, &r.tries, &act, &op), "the code")
	for _, p := range []struct{ s *string; d *string }{{reason, &r.voidReason}, {replaces, &r.replaces}, {act, &r.issuedByAct}, {op, &r.operator}} {
		if p.s != nil {
			*p.d = *p.s
		}
	}
	return r
}

// =============================================================================
// WHAT A CODE IS
// =============================================================================

func TestEnrol01_ACodeIsStoredAsADigest(t *testing.T) {
	w := newEnrolWorld(t)
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.person, member: w.personMem, digest: "483920"})
	refused(t, err, "an enrolment code stored as the code itself", refusedAs("enrolment_code_is_digest"))
}

// The server sets the 30 days; a client asking for 90 gets 30.
func TestEnrol02_TheServerSetsThirtyDays(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	id, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.person, member: w.personMem, digest: d,
		extraCol: ", expires_at", extra: ", now() + interval '90 days'"})
	fixture(t, err, "a code whose client asked for 90 days")
	var life string
	fixture(t, db.QueryRow(bg(), `SELECT (expires_at - issued_at)::text FROM enrolment_codes WHERE id = $1`, id).Scan(&life), "its life")
	if life != "30 days" {
		t.Fatalf("CLIENT CHOSE — the code lives %s; want the server's 30 days", life)
	}
}

func TestEnrol03_ACodeIsBornUntriedUnusedAndLive(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.person, member: w.personMem, digest: d,
		extraCol: ", wrong_tries, used_at", extra: ", 0, now()"})
	refused(t, err, "a code born already used", refusedAs("enrolment_code_born_fresh"))
}

// The code is for the person the member record belongs to.
func TestEnrol04_ACodeIsForTheMembersOwnPerson(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.other, member: w.personMem, digest: d})
	refused(t, err, "a code on one member's record for another person", refusedAs("enrolment_code_for_the_member"))
}

// =============================================================================
// WHO ISSUES IT
// =============================================================================

// One issuer, never two: an officer's act AND the operator's name would leave
// "who issued this" for the reader to choose. (A code naming neither is refused
// by the operator path's own rules, which then apply: no login, no reason.)
func TestEnrol05_ACodeNamesOneIssuer(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.person, member: w.personMem, digest: d,
		extraCol: ", issued_by_operator, operator_login, operator_reason", extra: ", 'Adaeze Okafor', 'ubuntu@host', 'also me'"})
	refused(t, err, "a code naming both an officer's act and the operator", refusedAs("enrolment_code_has_issuer"))
}

// The officer's step-up is for THIS member, in THIS transaction.
func TestEnrol06_ACodeRestsOnItsOwnAct(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.otherMem, user: w.person, member: w.personMem, digest: d})
	refused(t, err, "a code for one member on a step-up taken for another", refusedAs("enrolment_code_rests_on_its_act"))
}

// §10: nobody issues their own, through any member record they hold.
func TestEnrol07_NobodyIssuesTheirOwn(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.officerMem, user: w.officer, member: w.officerMem, digest: d})
	refused(t, err, "the treasurer issuing their own code", refusedAs("enrolment_not_self"))
	second := w.s.member(w.officer) // a second record of the same person, holding no post
	_, d2 := slip()
	_, err = issueBy(t, issueSpec{actor: w.officer, actMember: second, user: w.officer, member: second, digest: d2})
	refused(t, err, "the treasurer issuing their own code through their other member record", refusedAs("enrolment_not_self"))
}

// A current officer of the member's society; never the read-only auditor, never a plain member.
func TestEnrol08_OnlyACurrentOfficerIssues(t *testing.T) {
	w := newEnrolWorld(t)
	for _, by := range []struct{ who, what string }{{w.auditor, "the internal auditor"}, {w.other, "a member with no post"}} {
		_, d := slip()
		_, err := issueBy(t, issueSpec{actor: by.who, actMember: w.personMem, user: w.person, member: w.personMem, digest: d})
		refused(t, err, by.what+" issuing a code", refusedAs("enrolment_by_current_officer"))
	}
}

// The operator's path is for the founding officers alone; everyone after, from an officer.
func TestEnrol09_TheOperatorIssuesOnlyToOfficers(t *testing.T) {
	w := newEnrolWorld(t)
	_, d := slip()
	_, err := operatorIssue(w.personMem, d, opName, opHost, enrolWhy)
	refused(t, err, "the operator issuing a code to a member with no post", refusedAs("enrolment_operator_for_officers"))
}

func TestEnrol10_TheOperatorNamesThemselvesTheLoginAndWhy(t *testing.T) {
	w := newEnrolWorld(t)
	for _, c := range []struct{ op, login, why, rule string }{
		{" ", opHost, enrolWhy, "operator_names_themselves"},
		{opName, "", enrolWhy, "operator_login_recorded"},
		{opName, opHost, "  ", "operator_gives_reason"},
	} {
		_, d := slip()
		_, err := operatorIssue(w.officerMem, d, c.op, c.login, c.why)
		refused(t, err, "an operator's code missing "+c.rule, refusedAs(c.rule))
	}
}

// =============================================================================
// ONE LIVE CODE; A REISSUE IS ATTRIBUTABLE
// =============================================================================

// Three reissues: one live code, three voided "reissued", each pointing at the
// code it replaced, each with its own issuer and time: the pattern is visible.
func TestEnrol11_AReissueVoidsTheLiveCodeAndIsAttributable(t *testing.T) {
	w := newEnrolWorld(t)
	var ids []string
	for i := 0; i < 4; i++ {
		id, _ := w.officerIssues(t, w.person, w.personMem)
		ids = append(ids, id)
	}
	for i, id := range ids {
		r := codeState(t, id)
		if i < 3 && (!r.voided || r.voidReason != "reissued") {
			t.Fatalf("STILL LIVE — code %d of 4 after its replacement: %+v", i+1, r)
		}
		if i == 3 && (r.voided || r.used) {
			t.Fatalf("NOT LIVE — the newest code: %+v", r)
		}
		if i > 0 && r.replaces != ids[i-1] {
			t.Fatalf("UNCHAINED — code %d replaces %q; want the code before it, %s", i+1, r.replaces, ids[i-1])
		}
		if r.issuedByAct == "" {
			t.Fatalf("UNATTRIBUTED — code %d names no issuing act", i+1)
		}
	}
	var live int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM enrolment_codes WHERE user_id = $1 AND used_at IS NULL AND voided_at IS NULL`, w.person).Scan(&live), "live codes")
	if live != 1 {
		t.Fatalf("TWO LIVE — %d live codes for one person; want 1", live)
	}
}

// The replaced slip stops working; the new one still enrols. (Trying the old
// slip is a wrong try against the new code: a code is not told apart from a
// guess.)
func TestEnrol12_AReplacedSlipStopsWorking(t *testing.T) {
	w := newEnrolWorld(t)
	_, old := w.officerIssues(t, w.person, w.personMem)
	newID, code := w.officerIssues(t, w.person, w.personMem)
	if got := redeem(t, w.person, old); got != "" {
		t.Fatalf("ACCEPTED — the replaced slip still enrols (code %s)", got)
	}
	if got := redeem(t, w.person, code); got != newID {
		t.Fatalf("STRANDED — the new slip after the old one was tried: %q, want %s", got, newID)
	}
}

// (TestEnrol13 was "two live codes cannot exist". Removed before review:
// every insert passes through the reissue rule, which voids the live code
// first, so the partial unique index behind it is never reached and no mutant
// can make the test fail. The index stays as a backstop; test 11 proves the
// rule.)

// =============================================================================
// REDEEMING IT
// =============================================================================

func TestEnrol14_ACodeIsSingleUse(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	if got := redeem(t, w.person, code); got != id {
		t.Fatalf("FIXTURE: the first redemption: %q", got)
	}
	if got := redeem(t, w.person, code); got != "" {
		t.Fatalf("USED TWICE — the code enrolled a second time")
	}
}

func TestEnrol15_FiveWrongTriesVoidIt(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	for i := 0; i < 5; i++ {
		redeem(t, w.person, "wrong-"+fmt.Sprint(i))
	}
	r := codeState(t, id)
	if !r.voided || r.voidReason != "wrong_tries" {
		t.Fatalf("STILL LIVE — after five wrong tries: %+v", r)
	}
	if got := redeem(t, w.person, code); got != "" {
		t.Fatalf("ACCEPTED — the right code after five wrong tries")
	}
}

func TestEnrol_Control_FourWrongTriesLeaveItLive(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	for i := 0; i < 4; i++ {
		redeem(t, w.person, "wrong-"+fmt.Sprint(i))
	}
	if got := redeem(t, w.person, code); got != id {
		t.Fatalf("REFUSED — the right code after four wrong tries: %q", got)
	}
}

// Thirty days, by the server's clock.
func TestEnrol16_ACodeExpiresAfterThirtyDays(t *testing.T) {
	a := newAuth(t)
	w := newEnrolWorld(t)
	_, late := w.officerIssues(t, w.person, w.personMem)
	_, onTime := w.officerIssues(t, w.other, w.otherMem)
	try := func(user, code string) string {
		var id *string
		fixture(t, a.c.QueryRow(bg(), redeemSQL(t), user, digestOf(code)).Scan(&id), "a redemption")
		if id == nil {
			return ""
		}
		return *id
	}
	a.advance(29 * 24 * time.Hour)
	if try(w.other, onTime) == "" {
		t.Fatalf("FIXTURE: a code on day 29 refused")
	}
	a.advance(24*time.Hour + time.Minute)
	if try(w.person, late) != "" {
		t.Fatalf("EXPIRED CODE ACCEPTED — a code 30 days and a minute old enrolled")
	}
}

// The code's tries are the code's: no sign-in attempt, no lock.
func TestEnrol17_WrongTriesNeverCountAgainstThePinLockout(t *testing.T) {
	w := newEnrolWorld(t)
	w.officerIssues(t, w.person, w.personMem)
	var before, after int
	count := `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1`
	fixture(t, db.QueryRow(bg(), count, w.person).Scan(&before), "attempts before")
	for i := 0; i < 5; i++ {
		redeem(t, w.person, "wrong-"+fmt.Sprint(i))
	}
	fixture(t, db.QueryRow(bg(), count, w.person).Scan(&after), "attempts after")
	// Both locks (0022): the member's own phone, and any other device. A code
	// try is never from a trusted device, so the other-device lock is the one
	// a miscounted try would reach.
	var own, other bool
	fixture(t, db.QueryRow(bg(), `SELECT locked_until IS NOT NULL OR coalesce(needs_officer, false) FROM lock_state($1)`, w.person).Scan(&own), "the own-phone lock")
	fixture(t, db.QueryRow(bg(), `SELECT locked_until IS NOT NULL OR coalesce(needs_officer, false) FROM lock_state_unknown_device($1)`, w.person).Scan(&other), "the other-device lock")
	if after != before || own || other {
		t.Fatalf("COUNTED — five wrong enrolment tries wrote %d sign-in attempts; own-phone lock %v, other-device lock %v", after-before, own, other)
	}
}

// =============================================================================
// THE RECORD
// =============================================================================

// A code's record is never removed, and only moves forward: tried, used,
// voided. Its digest, person and issuer never change; a used or voided code
// never comes back.
func TestEnrol18_TheRecordIsAppendOnly(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	_, d := slip()
	for _, c := range []struct{ sql, what string }{
		{`DELETE FROM enrolment_codes WHERE id = $1`, "a code deleted"},
		{`UPDATE enrolment_codes SET code_digest = '` + d + `' WHERE id = $1`, "a code's digest replaced"},
		{`UPDATE enrolment_codes SET wrong_tries = 0, expires_at = now() + interval '1 year' WHERE id = $1`, "a code's life extended"},
	} {
		refused(t, one(c.sql, id), c.what, refusedAs("enrolment_code_append_only"))
	}
	if redeem(t, w.person, code) != id {
		t.Fatalf("FIXTURE: the redemption")
	}
	refused(t, one(`UPDATE enrolment_codes SET used_at = NULL WHERE id = $1`, id), "a used code made unused", refusedAs("enrolment_code_append_only"))
}

func TestEnrol_Control_AnOfficerIssuesOnTheirStepUp(t *testing.T) {
	w := newEnrolWorld(t)
	code, d := slip()
	id, err := issueBy(t, issueSpec{actor: w.officer, actMember: w.personMem, user: w.person, member: w.personMem, digest: d})
	accepted(t, err, "the treasurer issuing a member's code on their own step-up")
	if got := redeem(t, w.person, code); got != id {
		t.Fatalf("REFUSED — the person's own code, first try: %q", got)
	}
}

func TestEnrol_Control_TheOperatorIssuesAFoundingOfficersCode(t *testing.T) {
	w := newEnrolWorld(t)
	code, d := slip()
	id, err := operatorIssue(w.officerMem, d, opName, opHost, enrolWhy)
	accepted(t, err, "the operator issuing the founding treasurer's code")
	if got := redeem(t, w.officer, code); got != id {
		t.Fatalf("REFUSED — the founding treasurer's code: %q", got)
	}
}
