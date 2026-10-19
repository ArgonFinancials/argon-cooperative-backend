//go:build tier1

// Suite 0016 — A WITHDRAWAL REQUEST HAS AN AUTHOR. Written before the
// migration, for review (decided 2026-10-18).
//
// Finding 41: approval_requests recorded whom a request was for, never who
// raised it; raising needed no step-up; the client chose the expiry. It
// survived because nothing had needed to ask who raised a request until a
// member could raise one.
//
//   - A withdrawal request names its RAISER and rests on the raiser's own PIN
//     step-up (withdrawal_request), refused without either.
//   - The raiser is the member it pays, or an officer of the society raising for
//     a member without a phone (real in this market; refusing it pushes
//     societies back to paper).
//   - NOBODY SIGNS A REQUEST THEY RAISED (§10, "raises"): an officer who raised
//     a request for a member is recorded as its raiser and never signs it.
//   - THE SERVER SETS THE EXPIRY, from the society's window: seven days
//     shipped, set by the operator on the society's resolution. The one place a
//     default is right: unlike a quorum there is no safe refusal, because a
//     request with no expiry never expires. A client's expiry is ignored.
//   - WITHIN WITHDRAWABLE WHEN RAISED, refused with the figures: refusing early
//     respects the signatories' time; the hold checking again later is what
//     makes it safe to check early.
//   - A RESUBMISSION is for the same member and kind as the rejected request it
//     replaces.
//   - A REQUEST NO QUORUM CAN APPROVE IS REFUSED WHEN RAISED (decided
//     2026-10-19): the society's current signatories, less the raiser and the
//     member it pays, fewer than the quorum. A live defect before 0016 too (a
//     society of three officers with a quorum of three can never approve an
//     officer-raised request): otherwise it sits collecting one signature and
//     expires, and a member waits seven days to learn their society's
//     arithmetic made it impossible. The refusal states the arithmetic AND the
//     remedy: appoint another signatory, or lower the quorum.
package tier1

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// raiseSpec: a withdrawal request raised by `by` (default the member's own
// user). act: "" the raiser's own withdrawal_request act in this transaction;
// "none"; another kind. actBy: the act's owner, if not the raiser. noRaiser:
// no raised_by at all. expires: an expiry the client supplies (SQL), if any.
type raiseSpec struct {
	member, by, act, actBy, expires, resubmits string
	amount                                     int64
	noRaiser                                   bool
}

func (w *mworld) raiseBy(t *testing.T, r raiseSpec) (string, error) {
	t.Helper()
	if r.by == "" {
		r.by = userOf(t, r.member)
	}
	if r.amount == 0 {
		r.amount = payKobo
	}
	if r.expires == "" {
		r.expires = `now() + interval '1 day'`
	}
	kind := "withdrawal_request"
	if r.act != "" && r.act != "none" {
		kind = r.act
	}
	actBy := r.by
	if r.actBy != "" {
		actBy = r.actBy
	}
	var proof string
	if r.act != "none" {
		proof = stepUp(t, actBy, kind)
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		var act *string
		if r.act != "none" {
			var a string
			if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
				kind, actBy, proof).Scan(&a); err != nil {
				return fmt.Errorf("FIXTURE: the step-up act: %w", err)
			}
			act = &a
		}
		var raiser any = r.by
		if r.noRaiser {
			raiser = nil
		}
		return x.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at,
			  raised_by_user_id, act_id, resubmits_id)
			VALUES ($1, $2, $3, 'withdrawal_out', $4, 2, `+r.expires+`, $5, $6, $7) RETURNING id`,
			w.c.s.id, fmt.Sprintf("WDR-%d", seq.Add(1)), r.member, r.amount, raiser, act, nz(r.resubmits)).Scan(&id)
	})
	return id, err
}

// rejected: a request raised by its member and rejected by the chairman.
func (w *mworld) rejected(t *testing.T, member string) string {
	t.Helper()
	req, err := w.raiseBy(t, raiseSpec{member: member})
	fixture(t, err, "the request")
	fixture(t, onStepUp(t, "approval_rejections", "approval_rejection", w.s2,
		"request_id, member_id, user_id, post_at_rejection, reason, note", req, w.s2Mem, w.s2, "chairman", "documents_missing", "The payslip is not attached"),
		"the rejection")
	return req
}

// thirdSignatory: the money world has two signatories (the treasurer and the
// chairman) at a quorum of two; a request an officer raises needs a third, or
// no quorum can approve it (TestRaise12).
func (w *mworld) thirdSignatory(t *testing.T) {
	t.Helper()
	m := w.c.s.signatory(w.c.s.user("Secretary (signatory)"), "secretary")
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, m), "active secretary")
}

func setWindow(society string, days int, operator string) error {
	return one(`SELECT operator_set_withdrawal_request_days($1, $2, $3, $4, $5, $6::date, $7)`,
		society, days, nz(operator), opHost, minutes, resolved, opWhy)
}

// window: the request's expiry, in days after it was raised.
func window(t *testing.T, req string) string {
	t.Helper()
	var d string
	fixture(t, db.QueryRow(bg(), `SELECT (expires_at - raised_at)::text FROM approval_requests WHERE id = $1`, req).Scan(&d), "the request's window")
	return d
}

// =============================================================================
// AN AUTHOR, ON THEIR OWN STEP-UP
// =============================================================================

func TestRaise01_ARequestNamesItsRaiser(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, noRaiser: true})
	refused(t, err, "a withdrawal request nobody raised", refusedAs("withdrawal_request_names_raiser"))
}

func TestRaise02_ARequestRestsOnTheRaisersStepUp(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, act: "none"})
	refused(t, err, "a withdrawal request with no step-up", refusedAs("withdrawal_request_needs_step_up"))
}

func TestRaise03_TheActIsTheRaisersOwn(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, actBy: w.s1})
	refused(t, err, "the borrower's request on the treasurer's step-up", refusedAs("withdrawal_request_act_is_raisers"))
}

// Neither the member nor an officer: another member raising for them.
func TestRaise04_OnlyTheMemberOrAnOfficerRaises(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, by: w.g1})
	refused(t, err, "a guarantor (no post) raising the borrower's withdrawal", refusedAs("raiser_is_member_or_officer"))
}

// =============================================================================
// THE SERVER SETS THE EXPIRY
// =============================================================================

func TestRaise05_TheServerSetsTheExpiry(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.raiseBy(t, raiseSpec{member: w.bMem, expires: `now() + interval '30 days'`})
	fixture(t, err, "a request whose client asked for thirty days")
	if got := window(t, req); got != "7 days" {
		t.Fatalf("ACCEPTED — the client chose the window it is judged in: %s; want the society's 7 days", got)
	}
}

func TestRaise06_TheSocietysWindowSetsTheExpiry(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, setWindow(w.c.s.id, 3, opName), "the society's window set to three days")
	req, err := w.raiseBy(t, raiseSpec{member: w.bMem})
	fixture(t, err, "the request")
	if got := window(t, req); got != "3 days" {
		t.Fatalf("IGNORED — the society set three days and the request lives %s", got)
	}
}

// =============================================================================
// WITHIN WITHDRAWABLE WHEN RAISED
// =============================================================================

// All the borrower may withdraw, with the charges on top: refused, with the
// figures only the database has.
func TestRaise07_ARequestBeyondWithdrawableIsRefusedWithTheFigures(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, amount: borrowerWithdrawable})
	refused(t, err, "a request for everything withdrawable, charges on top", refusedAs("withdrawal_request_within_withdrawable"))
	if !strings.Contains(err.Error(), "held for guarantees") {
		t.Fatalf("UNEXPLAINED — the refusal does not carry the figures: %v", err)
	}
}

// =============================================================================
// THE RAISER NEVER SIGNS (§10)
// =============================================================================

func TestRaise08_TheRaiserNeverSignsIt(t *testing.T) {
	w := newMoneyWorld(t)
	w.thirdSignatory(t)
	req, err := w.raiseBy(t, raiseSpec{member: w.bMem, by: w.s1})
	fixture(t, err, "the treasurer raising for the borrower")
	refused(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the treasurer signing the request they raised", refusedAs("raiser_never_signs"))
}

// =============================================================================
// RESUBMISSION
// =============================================================================

func TestRaise09_AResubmissionIsForTheSameMember(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, loanSavings)
	original := w.rejected(t, w.bMem)
	_, err := w.raiseBy(t, raiseSpec{member: w.g1Mem, resubmits: original})
	refused(t, err, "a guarantor's request resubmitting the borrower's rejected one", refusedAs("resubmission_same_member"))
}

// =============================================================================
// THE OPERATOR SETS THE WINDOW
// =============================================================================

func TestRaise10_TheWindowIsAtLeastOneDay(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, setWindow(w.c.s.id, 0, opName), "a request window of zero days", refusedAs("withdrawal_request_days_sane"))
}

func TestRaise11_AWindowChangeIsLogged(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, setWindow(w.c.s.id, 14, opName), "the window set to fourteen days")
	r := lastLog(t, w.c.s.id)
	if r.setting != "withdrawal_request_days" || r.oldV != "7" || r.newV != "14" || r.operator != opName || r.ref != minutes {
		t.Fatalf("UNRECORDED — log %+v; want withdrawal_request_days, 7 -> 14, by the operator, on the resolution", r)
	}
}

// =============================================================================
// A REQUEST NO QUORUM CAN APPROVE
// =============================================================================

// Two signatories, a quorum of two, the treasurer raising for a member: only
// the chairman may sign. Refused when raised, with the arithmetic and the
// remedy, in the database's words.
func TestRaise12_ARequestThatCannotReachItsQuorumIsRefused(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, by: w.s1})
	refused(t, err, "a request only one signatory may sign, at a quorum of two", refusedAs("request_cannot_reach_quorum"))
	if msg := err.Error(); !strings.Contains(msg, "1 signatories may sign it") || !strings.Contains(msg, "needs 2") || !strings.Contains(msg, "Appoint another signatory, or lower the withdrawal quorum") {
		t.Fatalf("UNEXPLAINED — the refusal must say how many may sign, how many are needed, and what to do: %v", err)
	}
}

// =============================================================================
// CONTROLS
// =============================================================================

func TestRaise_Control_AMemberRaisesTheirOwn(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem})
	accepted(t, err, "the borrower raising their own withdrawal on their own step-up")
}

func TestRaise_Control_AnOfficerRaisesForAMember(t *testing.T) {
	w := newMoneyWorld(t)
	w.thirdSignatory(t) // the chairman and the secretary may sign it: the quorum of two is reachable
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, by: w.s1})
	accepted(t, err, "the treasurer raising for a member without a phone, on the treasurer's own step-up")
}

func TestRaise_Control_AResubmissionForTheSameMember(t *testing.T) {
	w := newMoneyWorld(t)
	original := w.rejected(t, w.bMem)
	_, err := w.raiseBy(t, raiseSpec{member: w.bMem, resubmits: original})
	accepted(t, err, "the borrower resubmitting their rejected request")
}
