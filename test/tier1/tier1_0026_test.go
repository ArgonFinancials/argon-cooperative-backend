//go:build tier1

// Suite 0026 — A SIGNATORY'S CODE COMES IN TWO HALVES, FROM TWO OTHER CURRENT
// SIGNATORIES. Written before the migration.
//
// Decided 2026-11-06 and confirmed 2026-11-09 (LEDGER_CONTRACT.md §12, §10's
// fifth reach): any act that can put a person in a position of authority
// needs the same scrutiny as the authority itself. A code for someone who
// holds a signing post therefore needs two other current signatories, as a
// signature does. The founding officers' codes stay with the operator.
//
// The reviewer's three conditions:
//   - EACH HALF IS USELESS ALONE and does not narrow the other: every code is
//     two independent halves (the service draws 40 bits for each), and the
//     database keeps only each half's digest.
//   - NEITHER ISSUER SEES THE OTHER'S HALF, at any point: no half's text is
//     ever stored; each is shown only to the one who issued it (the API's).
//   - FIVE WRONG TRIES APPLY TO THE ASSEMBLED CODE: one row, one count.
//
// The shape: the first signatory's act creates the code with half one's
// digest and NO code digest: pending, not redeemable, and costing no try.
// A second, different current signatory's act completes it with half two;
// the server then sets the code's digest from the two, in either order, so
// the slips can be typed in either order. Every whole code has the same
// form (the service assembles its digest the same way), so a redemption
// never needs to know which kind of code it is.
package tier1

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"
)

type hworld struct {
	*eworld
	chair, chairMem   string // a current signatory
	treas, treasMem   string // another current signatory (eworld's officer)
	sec               string // a current officer who does not sign
	target, targetMem string // a current signatory who needs a code
}

func newHalvesWorld(t *testing.T) *hworld {
	t.Helper()
	w := &hworld{eworld: newEnrolWorld(t)}
	w.treas, w.treasMem = w.officer, w.officerMem
	w.chair = w.s.user("Chairman (signatory)")
	w.chairMem = w.s.signatory(w.chair, "chairman")
	w.sec = w.s.user("Secretary (does not sign)")
	fixture(t, w.s.appoint(w.s.member(w.sec), "secretary", false, "2020-01-01", ""), "the secretary")
	w.target = w.s.user("Branch officer (signatory, enrolling)")
	w.targetMem = w.s.signatory(w.target, "branch_officer")
	return w
}

// assembled: the code's digest from its two halves' digests, in either order.
func assembled(a, b string) string {
	if b < a {
		a, b = b, a
	}
	h := sha256.Sum256([]byte(a + b))
	return hex.EncodeToString(h[:])
}

// firstHalf: `actor` takes a step-up and, in one transaction, records the act
// and a code for the target carrying only half one's digest.
func (w *hworld) firstHalf(t *testing.T, actor, digest string) (string, error) {
	t.Helper()
	proof := stepUp(t, actor, "enrolment_issue")
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
			actor, proof, w.targetMem).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act)
			VALUES ($1, $2, $3, $4) RETURNING id`, w.target, w.targetMem, digest, act).Scan(&id)
	})
	return id, err
}

// secondHalf: `actor` completes code `id` with half two's digest, on their own
// step-up for `actMember`, in one transaction (unless `apart`).
func (w *hworld) secondHalf(t *testing.T, actor, actMember, id, digest string, apart bool) error {
	t.Helper()
	proof := stepUp(t, actor, "enrolment_issue")
	var act string
	insertAct := func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
			actor, proof, actMember).Scan(&act)
	}
	update := func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `UPDATE enrolment_codes SET second_half_digest = $2, second_by_act = $3 WHERE id = $1`, id, digest, act)
		return err
	}
	if apart {
		if err := tx(insertAct); err != nil {
			return err
		}
		return tx(update)
	}
	return tx(func(x pgx.Tx) error {
		if err := insertAct(x); err != nil {
			return err
		}
		return update(x)
	})
}

// halvedCode: a complete code from the chairman and the treasurer; returns
// its id and its assembled digest.
func (w *hworld) halvedCode(t *testing.T) (string, string) {
	t.Helper()
	_, d1 := slip()
	_, d2 := slip()
	id, err := w.firstHalf(t, w.chair, d1)
	fixture(t, err, "the chairman's half")
	fixture(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, false), "the treasurer's half")
	return id, assembled(d1, d2)
}

// redeemDigest: a redemption of whatever was typed, by its digest, on a fresh
// device. Returns the code's id, or "" when nothing was redeemed.
func redeemDigest(t *testing.T, user, d string) string {
	t.Helper()
	var c *string
	fixture(t, db.QueryRow(bg(), `SELECT code_id::text FROM enrolment_redeem($1, $2, $3)`, user, d, digest()).Scan(&c), "a redemption")
	if c == nil {
		return ""
	}
	return *c
}

func codeDigestOf(t *testing.T, id string) string {
	t.Helper()
	var d *string
	fixture(t, db.QueryRow(bg(), `SELECT code_digest FROM enrolment_codes WHERE id = $1`, id).Scan(&d), "the code's digest")
	if d == nil {
		return ""
	}
	return *d
}

// =============================================================================
// WHO
// =============================================================================

// An officer cannot hand a signatory a whole code.
func TestHalves01_ASignatorysCodeFromAnOfficerComesInTwoHalves(t *testing.T) {
	w := newHalvesWorld(t)
	_, d := slip()
	_, err := issueBy(t, issueSpec{actor: w.chair, actMember: w.targetMem, user: w.target, member: w.targetMem, digest: d})
	refused(t, err, "the chairman handing a signatory a whole code", refusedAs("enrolment_signatory_two_halves"))
}

// Each half from a current signatory: an officer who does not sign issues neither.
func TestHalves02_EachHalfIsASignatorys(t *testing.T) {
	w := newHalvesWorld(t)
	_, d := slip()
	_, err := w.firstHalf(t, w.sec, d)
	refused(t, err, "the secretary (who does not sign) issuing half one", refusedAs("enrolment_half_by_signatory"))

	id, err := w.firstHalf(t, w.chair, d)
	fixture(t, err, "the chairman's half")
	_, d2 := slip()
	refused(t, w.secondHalf(t, w.sec, w.targetMem, id, d2, false), "the secretary issuing half two", refusedAs("enrolment_half_by_signatory"))
}

// Two DIFFERENT signatories: one cannot issue both halves.
func TestHalves03_TheHalvesComeFromTwoSignatories(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	id, err := w.firstHalf(t, w.chair, d1)
	fixture(t, err, "the chairman's half")
	_, d2 := slip()
	refused(t, w.secondHalf(t, w.chair, w.targetMem, id, d2, false), "the chairman issuing both halves", refusedAs("enrolment_halves_two_signatories"))
}

// The second half rests on its own step-up, for THIS member, taken with it.
func TestHalves04_TheSecondHalfRestsOnItsOwnAct(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	id, err := w.firstHalf(t, w.chair, d1)
	fixture(t, err, "the chairman's half")
	_, d2 := slip()
	refused(t, w.secondHalf(t, w.treas, w.personMem, id, d2, false), "half two on a step-up taken for another member", refusedAs("enrolment_code_rests_on_its_act"))
	refused(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, true), "half two on a step-up from another transaction", refusedAs("enrolment_code_rests_on_its_act"))
}

// =============================================================================
// WHAT A HALF IS
// =============================================================================

// Half one alone: pending. Not redeemable, by anything typed, and it costs no try.
func TestHalves05_OneHalfAloneRedeemsNothingAndCostsNoTry(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	id, err := w.firstHalf(t, w.chair, d1)
	fixture(t, err, "the chairman's half")
	for _, typed := range []string{d1, assembled(d1, d1), digest()} {
		if redeemDigest(t, w.target, typed) != "" {
			t.Fatalf("REDEEMED — a code with one half was redeemed")
		}
	}
	if c := codeState(t, id); c.tries != 0 || c.used || c.voided {
		t.Fatalf("COUNTED — tries on a code with one half: %+v (a pending code cannot be guessed, so it is not tried)", c)
	}
}

// The server sets the code's digest from the two halves, in either order.
func TestHalves06_TheServerAssemblesTheCode(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	_, d2 := slip()
	if d1 < d2 { // half one's digest sorts AFTER half two's: an assembly in issue order shows
		d1, d2 = d2, d1
	}
	id, err := w.firstHalf(t, w.chair, d1)
	fixture(t, err, "the chairman's half")
	if codeDigestOf(t, id) != "" {
		t.Fatalf("ASSEMBLED EARLY — a code with one half has a code digest")
	}
	fixture(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, false), "the treasurer's half")
	if got := codeDigestOf(t, id); got != assembled(d1, d2) {
		t.Fatalf("NOT ASSEMBLED — the completed code's digest is %q; want the digest of both halves", got)
	}
	if redeemDigest(t, w.target, assembled(d2, d1)) != id {
		t.Fatalf("REFUSED — the two halves typed in the other order did not redeem")
	}
}

// A half is set once, only on a code that is waiting for it, and never changed.
func TestHalves07_AHalfIsSetOnceAndNeverChanged(t *testing.T) {
	w := newHalvesWorld(t)
	id, _ := w.halvedCode(t)
	_, d3 := slip()
	refused(t, w.secondHalf(t, w.treas, w.targetMem, id, d3, false), "half two replaced", refusedAs("enrolment_code_append_only"))
	refused(t, one(`UPDATE enrolment_codes SET first_half_digest = $2 WHERE id = $1`, id, d3), "half one replaced", refusedAs("enrolment_code_append_only"))

	whole, _ := w.officerIssues(t, w.person, w.personMem)
	refused(t, w.secondHalf(t, w.treas, w.personMem, whole, d3, false), "half two added to a whole code", refusedAs("enrolment_code_append_only"))
}

// One shape per code: whole (a code digest, no halves), or halved (half one,
// then half two and the assembled digest). Never a code digest beside one half.
func TestHalves08_ACodeIsWholeOrHalvedNeverBoth(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	_, d := slip()
	proof := stepUp(t, w.chair, "enrolment_issue")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id) VALUES ('enrolment_issue', $1, $2, $3) RETURNING id`,
			w.chair, proof, w.targetMem).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO enrolment_codes (user_id, member_id, code_digest, first_half_digest, issued_by_act)
			VALUES ($1, $2, $3, $4, $5)`, w.target, w.targetMem, d, d1, act)
		return err
	})
	refused(t, err, "a code with both a code digest and half one", refusedAs("enrolment_code_one_shape"))
}

// The tries are the assembled code's: five wrong tries void it, whichever
// half was wrong.
func TestHalves09_FiveWrongTriesVoidTheAssembledCode(t *testing.T) {
	w := newHalvesWorld(t)
	id, _ := w.halvedCode(t)
	for i := 0; i < 5; i++ {
		redeemDigest(t, w.target, digest())
	}
	if c := codeState(t, id); !c.voided || c.voidReason != "wrong_tries" {
		t.Fatalf("NOT VOIDED — five wrong tries on a halved code left it %+v", c)
	}
}

// =============================================================================
// CONTROLS
// =============================================================================

func TestHalves_Control_TwoSignatoriesIssueASignatorysCode(t *testing.T) {
	w := newHalvesWorld(t)
	_, d1 := slip()
	_, d2 := slip()
	id, err := w.firstHalf(t, w.chair, d1)
	accepted(t, err, "the chairman's half of a signatory's code")
	accepted(t, w.secondHalf(t, w.treas, w.targetMem, id, d2, false), "the treasurer's half of a signatory's code")
	if redeemDigest(t, w.target, assembled(d1, d2)) != id {
		t.Fatalf("REFUSED — a signatory's code from two other signatories did not redeem")
	}
}

// The founding officers' codes stay with the operator, whole.
func TestHalves_Control_TheOperatorStillIssuesAFoundingSignatorysCode(t *testing.T) {
	w := newHalvesWorld(t)
	code, d := slip()
	id, err := operatorIssue(w.targetMem, d, opName, opHost, enrolWhy)
	accepted(t, err, "the operator issuing a founding signatory's whole code")
	if got := redeem(t, w.target, code); got != id {
		t.Fatalf("REFUSED — the founding signatory's code: %q", got)
	}
}

// A member who does not sign still gets a whole code from one officer.
func TestHalves_Control_AMemberStillGetsAWholeCode(t *testing.T) {
	w := newHalvesWorld(t)
	code, d := slip()
	id, err := issueBy(t, issueSpec{actor: w.treas, actMember: w.personMem, user: w.person, member: w.personMem, digest: d})
	accepted(t, err, "the treasurer issuing an ordinary member a whole code")
	if got := redeem(t, w.person, code); got != id {
		t.Fatalf("REFUSED — an ordinary member's whole code: %q", got)
	}
}
