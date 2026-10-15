//go:build tier1

// Suite 0015 — NOBODY AUTHORISES, SIGNS OR ATTESTS TO A MOVEMENT THEY BENEFIT
// FROM; THE AUTHORISER NEVER RECORDS; A CONTRIBUTION IS RECORDED ONCE.
// Written before the migration, for review (decided 2026-10-14, urgent).
//
// LEDGER_CONTRACT §10, widened 2026-10-14. "Nobody signs a request they
// benefit from" was written for signatures, and the payout hold is a different
// object, so it did not reach: a treasurer who was the member being paid could
// authorise their own payout and record it, the money leaving on one person's
// judgement and one person's attestation, both theirs. The fourth appearance
// of the same conflict, and the worst, because it bypasses signatures. The
// rule now names the acts, so the next object inherits it:
//
//   - AUTHORISES: the hold is never authorised by the person whose savings pay
//     out, and never RELEASED by them either (added 2026-10-14 at review): a
//     release pays nothing, which is why it is easy to miss, but it frees the
//     member's withdrawable, the precondition for their next payout. A
//     beneficiary-treasurer could have a hold authorised, a colleague decline
//     to pay it, release it themselves, and reach the money again with no
//     second person touching the amount. An act with a financial effect on
//     the actor.
//   - ATTESTS: a contribution or payout is never recorded by the person it
//     credits or pays. Refusing a record is right here, under finding 33's own
//     test: the record is malformed (its attestation is the beneficiary's own)
//     and a correct form exists (another officer records the same event).
//   - Checked per PERSON (the member record's user), as §10 always was.
//
// AUTHORISING AND ATTESTING ARE DIFFERENT DUTIES: the officer who authorised a
// hold never records its payout. Authorising is the judgement (approved,
// within withdrawable, correctly charged); recording is the attestation that
// it happened. One person doing both means nobody checked the other.
//
// A SINGLE CONTRIBUTION NAMES ITS CLIENT REFERENCE, unique within the society,
// the shape of postAll's reference: on a phone on 3G a retry is normal, and a
// retried contribution recorded twice is a member credited twice.
package tier1

import "testing"

type benefitWorld struct {
	*mworld
	sec, secMem string // a third signatory: the secretary
}

// newBenefitWorld: the money world with a secretary, and the TREASURER (the
// poster) given savings of their own, recorded by the society's default actor,
// a user with no member record, so no rule under test (nor its over-strict
// control mutant) can refuse the fixture itself.
func newBenefitWorld(t *testing.T) *benefitWorld {
	t.Helper()
	w := &benefitWorld{mworld: newMoneyWorld(t)}
	s := w.c.s
	w.sec = s.user("Secretary (signatory)")
	w.secMem = s.signatory(w.sec, "secretary")
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.secMem), "active secretary")
	fixture(t, w.post(t, move{kind: "contribution_in", actor: s.actor, valueDate: "2026-09-01", lines: w.contribution(w.s1Mem, loanSavings, fees{})}),
		"the treasurer's own savings")
	return w
}

// ownRequest: an approved withdrawal paying the TREASURER, signed by the
// chairman and the secretary (the treasurer never signs their own, 0011).
func (w *benefitWorld) ownRequest(t *testing.T) string {
	t.Helper()
	req, err := w.raise(w.s1Mem, payKobo, 2)
	fixture(t, err, "the treasurer's own withdrawal request")
	fixture(t, w.c.s.sign(req, w.s2Mem, "chairman"), "the chairman's signature")
	fixture(t, w.c.s.sign(req, w.secMem, "secretary"), "the secretary's signature")
	return req
}

// heldBy: an approved request and its hold, authorised by `by`.
func (w *benefitWorld) heldBy(t *testing.T, req, member, by string) string {
	t.Helper()
	h, err := w.hold(t, holdSpec{req: req, member: member, amount: payKobo, actor: by})
	fixture(t, err, "the hold")
	return h
}

// releaseBy: `by` releases a hold, with a reason.
func (w *benefitWorld) releaseBy(hold, by string) error {
	return one(`INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason) VALUES ($1, $2, $3)`, hold, by, "NIP returned: beneficiary bank unavailable")
}

func recordedOnce() want {
	return want{codes: []string{"23505"}, constraint: "contribution_recorded_once"}
}

// =============================================================================
// NOBODY AUTHORISES OR ATTESTS TO A MOVEMENT THEY BENEFIT FROM
// =============================================================================

func TestBenefit01_NobodyAuthorisesTheirOwnPayout(t *testing.T) {
	w := newBenefitWorld(t)
	_, err := w.hold(t, holdSpec{req: w.ownRequest(t), member: w.s1Mem, amount: payKobo, actor: w.s1})
	refused(t, err, "the treasurer authorising their own payout", refusedAs("authoriser_not_beneficiary"))
}

func TestBenefit02_NobodyRecordsTheirOwnPayout(t *testing.T) {
	w := newBenefitWorld(t)
	req := w.ownRequest(t)
	hold := w.heldBy(t, req, w.s1Mem, w.s2) // the chairman authorises: correctly
	_, err := w.record(t, payoutRec{req: req, hold: hold, member: w.s1Mem, amount: payKobo, ch: payFees(), by: w.s1})
	refused(t, err, "the treasurer recording their own payout", refusedAs("payout_not_recorded_by_beneficiary"))
}

// With no hold, no hold rule applies: only attestation can refuse it.
func TestBenefit03_NobodyRecordsTheirOwnUnheldPayout(t *testing.T) {
	w := newBenefitWorld(t)
	_, err := w.record(t, payoutRec{member: w.s1Mem, amount: payKobo, ch: payFees(), by: w.s1})
	refused(t, err, "the treasurer recording an unheld payout to themselves", refusedAs("payout_not_recorded_by_beneficiary"))
}

func TestBenefit04_NobodyRecordsTheirOwnContribution(t *testing.T) {
	w := newBenefitWorld(t)
	err := w.post(t, move{kind: "contribution_in", actor: w.s1, lines: w.contribution(w.s1Mem, 2500000, fees{})})
	refused(t, err, "the treasurer recording money into their own savings", refusedAs("contribution_not_recorded_by_beneficiary"))
}

// A release frees the member's withdrawable: the beneficiary never releases
// the hold on their own payout.
func TestBenefit08_NobodyReleasesTheirOwnHold(t *testing.T) {
	w := newBenefitWorld(t)
	hold := w.heldBy(t, w.ownRequest(t), w.s1Mem, w.s2)
	refused(t, w.releaseBy(hold, w.s1), "the treasurer releasing the hold on their own payout", refusedAs("releaser_not_beneficiary"))
}

// =============================================================================
// AUTHORISING AND ATTESTING ARE DIFFERENT DUTIES
// =============================================================================

func TestBenefit05_TheAuthoriserNeverRecordsThePayout(t *testing.T) {
	w := newBenefitWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	hold := w.heldBy(t, req, w.bMem, w.s1)
	_, err := w.record(t, payoutRec{req: req, hold: hold, member: w.bMem, amount: payKobo, ch: payFees(), by: w.s1})
	refused(t, err, "the treasurer recording the payout they authorised", refusedAs("authoriser_never_records"))
}

// =============================================================================
// A CONTRIBUTION IS RECORDED ONCE
// =============================================================================

func TestBenefit06_ASingleContributionNamesItsClientReference(t *testing.T) {
	w := newBenefitWorld(t)
	err := w.post(t, move{kind: "contribution_in", clientRef: "none", lines: w.contribution(w.g1Mem, 2500000, fees{})})
	refused(t, err, "a contribution with no client reference", refusedAs("contribution_names_client_reference"))
}

// The retry on a dropped connection: the same reference, the same society.
func TestBenefit07_AClientReferenceIsRecordedOnce(t *testing.T) {
	w := newBenefitWorld(t)
	ref := "CLI-" + digest()[:16]
	fixture(t, w.post(t, move{kind: "contribution_in", clientRef: ref, lines: w.contribution(w.g1Mem, 2500000, fees{})}), "the first")
	err := w.post(t, move{kind: "contribution_in", clientRef: ref, lines: w.contribution(w.g1Mem, 2500000, fees{})})
	refused(t, err, "the same contribution recorded again on a retry", recordedOnce())
}

// =============================================================================
// CONTROLS
// =============================================================================

// The correct form exists, and is accepted: another signatory authorises the
// treasurer's payout, and another officer records it.
func TestBenefit_Control_TheCorrectFormIsAccepted(t *testing.T) {
	w := newBenefitWorld(t)
	req := w.ownRequest(t)
	hold := w.heldBy(t, req, w.s1Mem, w.s2)
	_, err := w.record(t, payoutRec{req: req, hold: hold, member: w.s1Mem, amount: payKobo, ch: payFees(), by: w.sec})
	accepted(t, err, "the treasurer's payout, authorised by the chairman and recorded by the secretary")
}

// Being a member of the society is not a conflict; being the member credited is.
func TestBenefit_Control_AnOfficerRecordsAnotherMembersContribution(t *testing.T) {
	w := newBenefitWorld(t)
	accepted(t, w.post(t, move{kind: "contribution_in", actor: w.s1, lines: w.contribution(w.g1Mem, 2500000, fees{})}),
		"the treasurer recording a guarantor's contribution")
}

func TestBenefit_Control_AnotherSocietyMayUseTheSameReference(t *testing.T) {
	w, other := newBenefitWorld(t), newBenefitWorld(t)
	ref := "CLI-" + digest()[:16]
	fixture(t, w.post(t, move{kind: "contribution_in", clientRef: ref, lines: w.contribution(w.g1Mem, 2500000, fees{})}), "one society's")
	accepted(t, other.post(t, move{kind: "contribution_in", clientRef: ref, lines: other.contribution(other.g1Mem, 2500000, fees{})}),
		"another society's client minting the same reference")
}

func TestBenefit_Control_AnotherOfficerRecordsTheHeldPayout(t *testing.T) {
	w := newBenefitWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	hold := w.heldBy(t, req, w.bMem, w.s1)
	_, err := w.record(t, payoutRec{req: req, hold: hold, member: w.bMem, amount: payKobo, ch: payFees(), by: w.s2})
	accepted(t, err, "the payout the treasurer authorised, recorded by the chairman")
}

func TestBenefit_Control_AnotherSignatoryReleasesTheHold(t *testing.T) {
	w := newBenefitWorld(t)
	hold := w.heldBy(t, w.ownRequest(t), w.s1Mem, w.s2)
	accepted(t, w.releaseBy(hold, w.s2), "the chairman releasing the hold on the treasurer's payout")
}
