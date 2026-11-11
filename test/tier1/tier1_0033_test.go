//go:build tier1

// Suite 0033 — A PAYMENT NOBODY COULD MATCH, RESOLVED BY TWO OFFICERS. Written
// before the migration (stage 4, 2026-11-11). The reviewer's ruling (§12):
// when two members claim one payment, the amounts differ, or money arrives
// that nobody claims, ONE OFFICER PROPOSES AND A SECOND CONFIRMS, each on
// their own PIN, neither the member credited. They CREDIT the payment to a
// member at VFD's confirmed amount, never the claimed figure, or DECLINE a
// claim with a reason the member reads verbatim.
//
//	A PROPOSAL: a current officer of the society, not the internal auditor,
//	on their own payment_resolution act in its transaction, with a reason.
//	A credit names an uncredited payment of the society and a member of it;
//	a decline names an open claim of the society. One open proposal per
//	payment and per claim. Never edited.
//	A DECISION: once per proposal. A confirmation is by a DIFFERENT current
//	officer (not the auditor), on their own act. A rejection may be by any
//	current officer, the proposer included. Nobody credited, and no
//	claimant declined, takes part in either half.
//	CONFIRMED: a credit is posted in that transaction, at VFD's amount, by the
//	system, naming the payment; a decline leaves the claim not_credited with
//	the proposal's reason. REJECTED: nothing happens; the subject is open
//	again.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

type rsworld struct {
	*cworld
	o1, o2, auditor string // two officers (treasurer s1, chairman s2) and the auditor
}

func newResolveWorld(t *testing.T) *rsworld {
	t.Helper()
	w := &rsworld{cworld: newClaimWorld(t)}
	w.o1, w.o2 = w.s1, w.s2
	w.auditor = w.c.s.user("Internal auditor")
	fixture(t, w.c.s.appoint(w.c.s.member(w.auditor), "internal_auditor", false, "2020-01-01", ""), "the auditor")
	return w
}

type proposal struct {
	decision, inflow, claim, member, reason, key string
}

// propose: `actor` takes a step-up for `kind` and, in one transaction, writes
// the act and the proposal. kind "": payment_resolution.
func (w *rsworld) propose(t *testing.T, actor string, p proposal, kind string) (string, error) {
	t.Helper()
	if kind == "" {
		kind = "payment_resolution"
	}
	proof := stepUp(t, actor, kind)
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		key := p.key
		if key == "" {
			key = fmt.Sprintf("RES-%d", seq.Add(1))
		}
		return x.QueryRow(bg(), `INSERT INTO payment_resolutions (society_id, decision, inflow_id, claim_id, member_id, reason, proposed_by, proposed_act, client_reference)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id::text`,
			w.c.s.id, p.decision, nz(p.inflow), nz(p.claim), nz(p.member), nz(p.reason), actor, act, key).Scan(&id)
	})
	return id, err
}

// decide: `actor` confirms (reason "") or rejects (with reason) a proposal, on their own act.
func (w *rsworld) decide(t *testing.T, actor, resolution string, confirmed bool, reason string) error {
	t.Helper()
	proof := stepUp(t, actor, "payment_resolution")
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('payment_resolution', $1, $2) RETURNING id`, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO payment_resolution_decisions (resolution_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			resolution, confirmed, actor, act, nz(reason))
		return err
	})
}

const resolveWhy = "The payment's sender is the member's employer, by the payroll schedule of October"

// An uncredited payment, and a waiting claim on another session.
func (w *rsworld) unmatched(t *testing.T, kobo int64) (inflow string) {
	t.Helper()
	in, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), "", kobo)
	fixture(t, err, "a payment nobody claimed")
	return in
}

func TestResolve01_AProposalIsACurrentOfficersOwnActWithAReason(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	cr := proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}
	refused(t, second(w.propose(t, w.auditor, cr, "")), "the internal auditor proposes", refusedAs("resolution_by_an_officer"))
	refused(t, second(w.propose(t, w.g2, cr, "")), "a member with no post proposes", refusedAs("resolution_by_an_officer"))
	refused(t, second(w.propose(t, w.o1, cr, "contribution_record")), "a proposal on another kind of act", refusedAs("resolution_act"))
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: "  "}, "")), "a proposal with no reason", refusedAs("resolution_says_why"))
	_, err := w.propose(t, w.o1, cr, "")
	accepted(t, err, "the treasurer proposes, on their own act, with a reason")
}

func TestResolve02_ACreditNamesTheSocietysUncreditedPaymentAndItsMember(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.otherMem, reason: resolveWhy}, "")), "a credit to a member of another society", refusedAs("resolution_subject"))
	elsewhere, err := inflowFrom(w.other.id, w.otherAcct, sessionNew(), "", 50000)
	fixture(t, err, "another society's payment")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", inflow: elsewhere, member: w.bMem, reason: resolveWhy}, "")), "a credit of another society's payment", refusedAs("resolution_subject"))
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", member: w.bMem, reason: resolveWhy}, "")), "a credit naming no payment", refusedAs("resolution_subject"))
	done, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), w.memAcct, 50000)
	fixture(t, err, "a payment already credited (the member's own account)")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", inflow: done, member: w.g1Mem, reason: resolveWhy}, "")), "a credit of a payment already credited", refusedAs("resolution_payment_uncredited"))
}

func TestResolve03_ADeclineNamesAnOpenClaimOfTheSociety(t *testing.T) {
	w := newResolveWorld(t)
	s := sessionNew()
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 70000)
	fixture(t, err, "a claim")
	_, err = inflowFrom(w.c.s.id, w.socAcct, s, "", 70000)
	fixture(t, err, "its payment: credited")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "decline", claim: c, reason: resolveWhy}, "")), "declining a claim already credited", refusedAs("resolution_claim_open"))
	other, err := claim(w.other.id, w.otherMem, w.otherMemUser, sessionNew(), 70000)
	fixture(t, err, "a claim in another society")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "decline", claim: other, reason: resolveWhy}, "")), "declining another society's claim", refusedAs("resolution_subject"))
}

func TestResolve04_OneOpenProposalPerPaymentAndPerClaim(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	_, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}, "")
	fixture(t, err, "the first proposal")
	refused(t, second(w.propose(t, w.o2, proposal{decision: "credit", inflow: in, member: w.g1Mem, reason: resolveWhy}, "")), "a second proposal on a payment with one open", want{codes: []string{"23505"}, constraint: "resolution_open_once_per_payment"})
	c, err := claim(w.c.s.id, w.g1Mem, w.g1, sessionNew(), 9000)
	fixture(t, err, "a claim")
	_, err = w.propose(t, w.o1, proposal{decision: "decline", claim: c, reason: resolveWhy}, "")
	fixture(t, err, "the decline")
	refused(t, second(w.propose(t, w.o2, proposal{decision: "decline", claim: c, reason: resolveWhy}, "")), "a second proposal on a claim with one open", want{codes: []string{"23505"}, constraint: "resolution_open_once_per_claim"})
}

func TestResolve05_ASecondDifferentOfficerConfirms(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	r, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}, "")
	fixture(t, err, "the proposal")
	refused(t, w.decide(t, w.o1, r, true, ""), "the proposer confirms their own proposal", refusedAs("resolution_two_officers"))
	refused(t, w.decide(t, w.auditor, r, true, ""), "the auditor confirms", refusedAs("resolution_by_an_officer"))
	accepted(t, w.decide(t, w.o2, r, true, ""), "the chairman confirms")
	// A second decision of any kind: a rejection, which no other rule refuses
	// (a second confirmation of a credit is also refused as already credited).
	refused(t, w.decide(t, w.secretaryOf(t), r, false, "Rejected after the confirmation, to undo it"), "a rejection of a confirmed proposal", want{codes: []string{"23505"}, constraint: "resolution_decided_once"})
}

func TestResolve06_NobodyCreditedOrDeclinedTakesPart(t *testing.T) {
	w := newResolveWorld(t)
	// The treasurer is a member: a credit to the treasurer's own savings.
	in := w.unmatched(t, 50000)
	refused(t, second(w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.s1Mem, reason: resolveWhy}, "")), "an officer proposes crediting themselves", refusedAs("resolution_not_beneficiary"))
	r, err := w.propose(t, w.o2, proposal{decision: "credit", inflow: in, member: w.s1Mem, reason: resolveWhy}, "")
	fixture(t, err, "the chairman proposes crediting the treasurer")
	refused(t, w.decide(t, w.o1, r, true, ""), "the treasurer confirms a credit to themselves", refusedAs("resolution_not_beneficiary"))
	// A decline of an officer's own claim.
	c, err := claim(w.c.s.id, w.s1Mem, w.o1, sessionNew(), 9000)
	fixture(t, err, "the treasurer's own claim")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "decline", claim: c, reason: resolveWhy}, "")), "an officer declines their own claim", refusedAs("resolution_not_beneficiary"))
}

func TestResolve07_AConfirmedCreditIsPostedAtVFDsAmount(t *testing.T) {
	w := newResolveWorld(t)
	s := sessionNew()
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 500000)
	fixture(t, err, "a claim of ₦5,000")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 50000)
	fixture(t, err, "₦500 arrived")
	wantNotPosted(t, in, "amounts that differ, before the officers")
	r, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, claim: c, reason: resolveWhy}, "")
	fixture(t, err, "the proposal")
	wantNotPosted(t, in, "a proposal no second officer has confirmed")
	accepted(t, w.decide(t, w.o2, r, true, ""), "the confirmation")
	p := wantPosted(t, in, w.bMem, 50000, "a credit two officers confirmed")
	if p.actor != nil {
		t.Fatalf("ATTRIBUTED — the posting names user %v: the system posts it on the officers' resolution", *p.actor)
	}
	if st := claimState(t, c); st != "posted" {
		t.Fatalf("UNTOLD — the member's claim says %q after two officers credited it", st)
	}
	if _, ok := vfdExceptions(t, w.c.s.id)["claim_amount_differs|"+c]; ok {
		t.Fatalf("NOISE — the claim is still an exception after it was credited")
	}
}

func TestResolve08_AConfirmedDeclineLeavesTheClaimNotCreditedWithTheReason(t *testing.T) {
	w := newResolveWorld(t)
	c, err := claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), 30000)
	fixture(t, err, "a claim VFD never confirmed")
	r, err := w.propose(t, w.o1, proposal{decision: "decline", claim: c, reason: "No such payment reached the society's account; your bank's receipt shows it went to another account"}, "")
	fixture(t, err, "the decline")
	if st := claimState(t, c); st != "waiting" {
		t.Fatalf("EARLY — the claim says %q before a second officer confirmed", st)
	}
	accepted(t, w.decide(t, w.o2, r, true, ""), "the confirmation")
	var st, detail string
	fixture(t, db.QueryRow(bg(), `SELECT state, coalesce(detail, '') FROM contribution_claim_states WHERE claim_id = $1`, c).Scan(&st, &detail), "the claim's state")
	if st != "not_credited" || detail != "No such payment reached the society's account; your bank's receipt shows it went to another account" {
		t.Fatalf("UNTOLD — the declined claim says %q, %q", st, detail)
	}
	if _, ok := vfdExceptions(t, w.c.s.id)["claim_unconfirmed|"+c]; ok {
		t.Fatalf("NOISE — a declined claim is still an exception")
	}
}

func TestResolve09_ARejectionChangesNothingAndReopensTheSubject(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	r, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}, "")
	fixture(t, err, "the proposal")
	refused(t, w.decide(t, w.o2, r, false, ""), "a rejection with no reason", refusedAs("resolution_says_why"))
	accepted(t, w.decide(t, w.o1, r, false, "I proposed the wrong member: it is the guarantor's payment"), "the proposer withdraws their own proposal")
	wantNotPosted(t, in, "a rejected proposal")
	_, err = w.propose(t, w.o2, proposal{decision: "credit", inflow: in, member: w.g1Mem, reason: resolveWhy}, "")
	accepted(t, err, "a new proposal after the rejection")
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "resolution_awaiting_confirmation", second2(w.openResolution(t, in)), "a proposal waiting for its second officer")
}

func TestResolve10_ProposalsAndDecisionsAreNeverEdited(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 50000)
	r, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}, "")
	fixture(t, err, "the proposal")
	refused(t, one(`UPDATE payment_resolutions SET member_id = $2 WHERE id = $1`, r, w.g1Mem), "the member changed after the proposal", refusedAs("vfd_records_append_only"))
	fixture(t, w.decide(t, w.o2, r, false, "Wrong member"), "the rejection")
	refused(t, one(`DELETE FROM payment_resolution_decisions WHERE resolution_id = $1`, r), "a rejection deleted to reopen the proposal", refusedAs("vfd_records_append_only"))
}

// Two officers credit a payment two members claimed: the other claim closes,
// naming nobody, and a decline still open on it can no longer be confirmed.
func TestResolve11_ACreditTwoOfficersConfirmCloseEveryOtherClaimOnThePayment(t *testing.T) {
	w := newResolveWorld(t)
	s := sessionNew()
	a, err := claim(w.c.s.id, w.bMem, w.borrower, s, 60000)
	fixture(t, err, "one member's claim")
	b, err := claim(w.c.s.id, w.g1Mem, w.g1, s, 60000)
	fixture(t, err, "another member's claim on the same payment")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 60000)
	fixture(t, err, "the payment, contested")
	dec, err := w.propose(t, w.o1, proposal{decision: "decline", claim: b, reason: "The receipt this member showed is not theirs"}, "")
	fixture(t, err, "a decline of the other claim, left open")
	cr, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, claim: a, reason: resolveWhy}, "")
	fixture(t, err, "the credit")
	accepted(t, w.decide(t, w.o2, cr, true, ""), "the credit confirmed")
	var st, detail string
	fixture(t, db.QueryRow(bg(), `SELECT state, coalesce(detail, '') FROM contribution_claim_states WHERE claim_id = $1`, b).Scan(&st, &detail), "the other claim")
	if st != "not_credited" || detail != "This payment was credited to another member after two officers checked it." {
		t.Fatalf("HANGING — the other member's claim says %q, %q after the payment was credited to someone else by two officers", st, detail)
	}
	refused(t, w.decide(t, w.o2, dec, true, ""), "confirming a decline of a claim already closed", refusedAs("resolution_claim_open"))
	accepted(t, w.decide(t, w.o2, dec, false, "Closed by the credit"), "rejecting it, to clear it")
}

// A declined claim is closed for good: a payment arriving later on its session
// is nobody's until two officers credit it.
func TestResolve12_ADeclinedClaimNeverMatchesAPaymentArrivingLater(t *testing.T) {
	w := newResolveWorld(t)
	s := sessionNew()
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 45000)
	fixture(t, err, "a claim VFD has not confirmed")
	r, err := w.propose(t, w.o1, proposal{decision: "decline", claim: c, reason: "No such payment has reached the society's account"}, "")
	fixture(t, err, "the decline")
	fixture(t, w.decide(t, w.o2, r, true, ""), "the decline confirmed")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 45000)
	accepted(t, err, "the payment arrives a week later")
	wantNotPosted(t, in, "a payment on a declined claim's session")
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "inflow_unclaimed", in, "a payment whose only claim was declined")
}

// A retried proposal is recorded once (the client's key, unique in the society).
func TestResolve13_AProposalIsRecordedOncePerKey(t *testing.T) {
	w := newResolveWorld(t)
	c1, err := claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), 9000)
	fixture(t, err, "a claim")
	c2, err := claim(w.c.s.id, w.g1Mem, w.g1, sessionNew(), 9000)
	fixture(t, err, "another claim")
	_, err = w.propose(t, w.o1, proposal{decision: "decline", claim: c1, reason: resolveWhy, key: "RES-ONCE-" + c1[:8]}, "")
	fixture(t, err, "the first proposal")
	refused(t, second(w.propose(t, w.o1, proposal{decision: "decline", claim: c2, reason: resolveWhy, key: "RES-ONCE-" + c1[:8]}, "")), "a second proposal under the same key", want{codes: []string{"23505"}, constraint: "resolution_recorded_once"})
}

// Two officers credit an unclaimed payment; nothing is left in front of anyone.
func TestResolve_Control_TwoOfficersCreditAnUnclaimedPayment(t *testing.T) {
	w := newResolveWorld(t)
	in := w.unmatched(t, 80000)
	r, err := w.propose(t, w.o1, proposal{decision: "credit", inflow: in, member: w.bMem, reason: resolveWhy}, "")
	accepted(t, err, "the proposal")
	accepted(t, w.decide(t, w.o2, r, true, ""), "the confirmation")
	wantPosted(t, in, w.bMem, 80000, "the credit")
	for k := range vfdExceptions(t, w.c.s.id) {
		t.Fatalf("NOISE — %s is an exception after two officers credited the payment", k)
	}
}

func (w *rsworld) secretaryOf(t *testing.T) string {
	t.Helper()
	u := w.c.s.user("Secretary")
	fixture(t, w.c.s.appoint(w.c.s.member(u), "secretary", false, "2020-01-01", ""), "the secretary")
	return u
}

func (w *rsworld) openResolution(t *testing.T, inflow string) (string, error) {
	var id string
	err := db.QueryRow(bg(), `SELECT r.id::text FROM payment_resolutions r WHERE r.inflow_id = $1
		AND NOT EXISTS (SELECT 1 FROM payment_resolution_decisions d WHERE d.resolution_id = r.id)`, inflow).Scan(&id)
	return id, err
}

func second2(s string, _ error) string { return s }
