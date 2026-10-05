//go:build tier1

// Suite 0007 — APPROVALS: a control, not a record (PRD 8.4). Written before
// the migration, for review. Jumps the loan queue (decided 2026-10-05):
// findings 14 and 15 are bigger than loans.
//
//   - The status is DERIVED, never set: approval_status(request, at). Approved
//     when enough distinct people holding a signing post signed before expiry;
//     rejected when a rejection row exists; expired when the clock passed with
//     neither; otherwise awaiting. Nothing can write it. (A stored status would
//     go stale the moment a request expired unattended, and keeping it fresh
//     would need a writer.)
//   - A rejection is its own ROW: who, in what post, when (server time), and
//     one of Stage 5's four reasons. A status alone would be a decision with no
//     author, no time and no reason.
//   - Signing refuses an expired or rejected request (finding 15).
//
// Every consumer (loans first) asks approval_status, never a column, so the
// next one cannot forget to distrust it.
package tier1

import (
	"testing"
	"time"
)

// ---- the fixture ---------------------------------------------------------------
//
// Society S: three signatories (treasurer, chairman, secretary), the internal
// auditor, a member with no post. A request needs 2 of them and expires in a day.

type aworld struct {
	s               *soc
	treasurer, tMem string
	chairman, cMem  string
	secretary, sMem string
	auditor, aMem   string
	plain, pMem     string
}

func newApprovals(t *testing.T) *aworld {
	t.Helper()
	w := &aworld{s: newSociety(t)}
	w.treasurer = w.s.user("Treasurer")
	w.tMem = w.s.signatory(w.treasurer, "treasurer")
	w.chairman = w.s.user("Chairman")
	w.cMem = w.s.signatory(w.chairman, "chairman")
	w.secretary = w.s.user("Secretary")
	w.sMem = w.s.signatory(w.secretary, "secretary")
	w.auditor = w.s.user("Internal auditor")
	w.aMem = w.s.member(w.auditor)
	fixture(t, w.s.appoint(w.aMem, "internal_auditor", false, "2020-01-01", ""), "auditor")
	w.plain = w.s.user("Member, no post")
	w.pMem = w.s.member(w.plain)
	return w
}

// expired: a request that expired yesterday (2 of 3).
func (w *aworld) expired(t *testing.T) string {
	t.Helper()
	var id string
	fixture(t, db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, kind, amount_kobo, required, expires_at)
		VALUES ($1, $2, 'withdrawal_out', 50000000, 2, now() - interval '1 day') RETURNING id`,
		w.s.id, "APR-X-"+digest()[:12]).Scan(&id), "an expired request")
	return id
}

func (w *aworld) reject(req, member, user, post, reason string) error {
	return one(`INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, note)
		VALUES ($1, $2, $3, $4, $5, 'The supporting payslip is not attached')`, req, member, user, post, reason)
}

// status reads THE status, as at `at` ("" = now).
func status(t *testing.T, req, at string) string {
	t.Helper()
	var st string
	fixture(t, db.QueryRow(bg(), `SELECT approval_status($1, coalesce($2::timestamptz, now()))::text`, req, nz(at)).Scan(&st), "approval_status")
	return st
}

func wantStatus(t *testing.T, req, at, want, why string) {
	t.Helper()
	if got := status(t, req, at); got != want {
		t.Fatalf("ACCEPTED — status %q, want %q: %s", got, want, why)
	}
}

func dayAfterExpiry(t *testing.T, req string) string {
	t.Helper()
	var at time.Time
	fixture(t, db.QueryRow(bg(), `SELECT expires_at + interval '1 day' FROM approval_requests WHERE id = $1`, req).Scan(&at), "expiry")
	return at.Format(time.RFC3339Nano)
}

// =============================================================================
// THE STATUS IS DERIVED, AND NOTHING CAN WRITE IT
// =============================================================================

func TestApproval1_StatusCannotBeWritten(t *testing.T) {
	w := newApprovals(t)
	refused(t, one(`UPDATE approval_requests SET state = 'approved' WHERE id = $1`, w.s.request()),
		"writing 'approved' onto a request directly",
		want{codes: []string{"42703"}},                                        // no such column: nothing stores it
		want{codes: []string{"23514"}, constraint: "approval_status_derived"}) // or a stored copy no one may write
}

// The attack finding 14 describes, attempted; the status must not move.
func TestApproval2_SettingTheColumnApprovesNothing(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	_ = one(`UPDATE approval_requests SET state = 'approved' WHERE id = $1`, req) // refused, or (today) it lands
	wantStatus(t, req, "", "awaiting", "a request with no signatures reads approved because a column was written")
}

func TestApproval3_ApprovedAtQuorumNotBefore(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	wantStatus(t, req, "", "awaiting", "one signature of two")
	fixture(t, w.s.sign(req, w.cMem, "chairman"), "second signature")
	wantStatus(t, req, "", "approved", "two signatures of two")
}

// Finding 15, first half.
func TestApproval4_SigningAnExpiredRequestIsRefused(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.s.sign(w.expired(t), w.tMem, "treasurer"), "signing a request that expired yesterday", refusedAs("signature_before_expiry"))
}

func TestApproval5_UnapprovedPastExpiryReadsExpired(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "one signature")
	wantStatus(t, req, dayAfterExpiry(t, req), "expired", "one signature of two, a day after expiry")
}

// An approval is a fact once reached; expiry does not undo it.
func TestApproval6_ApprovedStaysApprovedAfterExpiry(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	fixture(t, w.s.sign(req, w.cMem, "chairman"), "second signature")
	wantStatus(t, req, dayAfterExpiry(t, req), "approved", "a quorum reached before expiry, read a day after it")
}

// =============================================================================
// A REJECTION IS A ROW: author, post, time, reason
// =============================================================================

func TestApproval7_RejectionClosesTheRequest(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.reject(req, w.tMem, w.treasurer, "treasurer", "documents_missing"), "a rejection")
	wantStatus(t, req, "", "rejected", "a request with a rejection row")
}

func TestApproval8_RejectionGivesOneOfTheFourReasons(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.reject(w.s.request(), w.tMem, w.treasurer, "treasurer", "I just don't like it"),
		"a rejection whose reason is free text, routing nowhere", refusedAs("rejection_reason_known"))
}

func TestApproval9_RejectionTimeIsTheServers(t *testing.T) {
	w := newApprovals(t)
	refused(t, one(`INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, rejected_at)
		VALUES ($1, $2, $3, 'treasurer', 'amount_wrong', '2026-01-15')`, w.s.request(), w.tMem, w.treasurer),
		"a rejection back-dated by its author", refusedAs("rejection_rejected_at_is_now"))
}

func TestApproval10_RejecterHoldsACurrentSigningPost(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.reject(w.s.request(), w.pMem, w.plain, "treasurer", "not_eligible"),
		"a member with no post rejecting", refusedAs("rejecter_holds_signing_post"))
	refused(t, w.reject(w.s.request(), w.aMem, w.auditor, "internal_auditor", "looks_wrong"),
		"the internal auditor (read-only) rejecting", refusedAs("rejecter_holds_signing_post"))
}

// The rejection is the act of the person whose member record it names.
func TestApproval11_RejecterIsThePersonOfTheMemberRecord(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.reject(w.s.request(), w.tMem, w.chairman, "treasurer", "amount_wrong"),
		"the treasurer's member record with the chairman's user", refusedAs("rejecter_user_is_member_user"))
}

// Finding 15, second half.
func TestApproval12_SigningARejectedRequestIsRefused(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.reject(req, w.tMem, w.treasurer, "treasurer", "looks_wrong"), "a rejection")
	refused(t, w.s.sign(req, w.cMem, "chairman"), "signing a rejected request", refusedAs("signature_on_open_request"))
}

func TestApproval13_RejectingAnApprovedRequestIsRefused(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	fixture(t, w.s.sign(req, w.cMem, "chairman"), "second signature")
	refused(t, w.reject(req, w.sMem, w.secretary, "secretary", "looks_wrong"),
		"rejecting a request already approved", refusedAs("rejection_on_open_request"))
}

func TestApproval14_RejectingAnExpiredRequestIsRefused(t *testing.T) {
	w := newApprovals(t)
	refused(t, w.reject(w.expired(t), w.tMem, w.treasurer, "treasurer", "amount_wrong"),
		"rejecting a request that has already expired", refusedAs("rejection_on_open_request"))
}

func TestApproval15_RejectionsAreImmutable(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.reject(req, w.tMem, w.treasurer, "treasurer", "documents_missing"), "a rejection")
	refused(t, one(`UPDATE approval_rejections SET reason = 'amount_wrong' WHERE request_id = $1`, req),
		"changing a rejection's reason after the fact", refusedAs("approval_rejections_immutable"))
	refused(t, one(`DELETE FROM approval_rejections WHERE request_id = $1`, req),
		"deleting a rejection to reopen the request", refusedAs("approval_rejections_immutable"))
}

// =============================================================================
// CONTROLS
// =============================================================================

// Two of three is a quorum: the third signatory is not needed. (A rule, not
// a control: on M0 nothing ever reads 'approved'.)
func TestApproval16_TwoOfThreeApproves(t *testing.T) {
	w := newApprovals(t)
	req := w.s.request()
	fixture(t, w.s.sign(req, w.tMem, "treasurer"), "first signature")
	fixture(t, w.s.sign(req, w.sMem, "secretary"), "second signature (the chairman never signs)")
	wantStatus(t, req, "", "approved", "two of three signatories signed a 2-of-3 request")
}

func TestApproval_Control_EachOfTheFourReasons(t *testing.T) {
	w := newApprovals(t)
	for _, r := range []string{"documents_missing", "amount_wrong", "not_eligible", "looks_wrong"} {
		accepted(t, w.reject(w.s.request(), w.tMem, w.treasurer, "treasurer", r), "a rejection for "+r)
	}
}
