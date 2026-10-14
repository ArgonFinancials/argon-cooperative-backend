//go:build tier1

// Suite 0012 — THE PAYOUT HOLD: CHECK BEFORE THE MONEY MOVES, RECORD WHATEVER
// HAPPENED. Written before the migration, for review (finding 33).
//
// Decided 2026-10-13 (LEDGER_CONTRACT.md §11): THE LEDGER MUST BE ABLE TO
// RECORD ANYTHING THAT ACTUALLY HAPPENED. A rule that prevents recording
// reality is a blindfold, not a control. 0011 checked a withdrawal when it was
// recorded, after the money had left the bank, and refused to record one it
// judged wrong. 0012 moves every judgement to the moment the payout is
// AUTHORISED TO EXECUTE, and makes recording a matter of what is true:
//
//   - A HOLD, before money moves, on the authorising officer's own PIN step-up:
//     for an approved withdrawal request, for its amount and member, with the
//     charges the server computes, within what the member may withdraw (D-10:
//     savings − guarantees held per person − the floor − other OPEN holds).
//     One open hold per request. A hold is released, with its reason, when the
//     transfer definitively fails; a paid hold is never released.
//
//   - A RECORDED PAYOUT is refused only when it is malformed and a correct
//     form exists: no step-up, no transfer reference, the same transfer
//     recorded twice, the wrong member's savings, charges other than the
//     hold's. Everything that describes REALITY is recorded and FLAGGED for a
//     person (ledger_discrepancies): a payout with no hold, of a different
//     amount, a second payout on one hold, a payout on a released hold, a
//     payout that leaves the member overdrawn.
//
//   - Contributions follow the same principle: money that arrived is recorded
//     even where the society's charge series are incomplete, uncharged and
//     flagged (charge_undetermined), never refused.
//
// The figures are 0011's illustrative test data. The tests depend on the shape.
package tier1

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// payFees(): the illustrative series on ₦6,000, in this schema's terms.
func payFees() fees { return transferCharges(payKobo) }

// hold: an officer authorises a payout to execute. act: "" their own
// withdrawal_authorisation act in this transaction; "none"; another kind; or
// actID from elsewhere. by: the act's owner if not the actor.
type holdSpec struct {
	req, member string
	amount      int64
	actor, by   string
	act, actID  string
	charges     *fees // supplied charge figures (the server should ignore them)
}

func (w *mworld) hold(t *testing.T, h holdSpec) (string, error) {
	t.Helper()
	if h.actor == "" {
		h.actor = w.poster
	}
	by := h.actor
	if h.by != "" {
		by = h.by
	}
	kind := "withdrawal_authorisation"
	if h.act != "" && h.act != "none" {
		kind = h.act
	}
	var proof string
	if h.act != "none" && h.actID == "" {
		proof = stepUp(t, by, kind)
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		act := h.actID
		if h.act != "none" && act == "" {
			if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
				kind, by, proof).Scan(&act); err != nil {
				return fmt.Errorf("FIXTURE: the step-up act: %w", err)
			}
		}
		var c fees
		if h.charges != nil {
			c = *h.charges
		}
		return x.QueryRow(bg(), `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo,
			  partner_fee_kobo, `+vatColumn()+`, statutory_levy_kobo, argon_margin_kobo, actor_user_id, act_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			h.req, w.c.s.id, h.member, h.amount, c.fee, c.vat, c.levy, c.margin, h.actor, nz(act)).Scan(&id)
	})
	return id, err
}

// held: an approved request for `member` and its hold, both as fixtures.
func (w *mworld) held(t *testing.T, member string, amount int64) (req, hold string) {
	t.Helper()
	req = w.approved(t, member, amount)
	hold, err := w.hold(t, holdSpec{req: req, member: member, amount: amount})
	fixture(t, err, "the hold")
	return req, hold
}

func (w *mworld) release(hold, reason string) error {
	return one(`INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason) VALUES ($1, $2, $3)`, hold, w.poster, nz(reason))
}

// payoutRec: a recorded payout. The bank paid `amount`; `ch` are the charge
// lines; `member` is whose savings fall; ref is the transfer's reference.
type payoutRec struct {
	req, hold, member, ref string
	by                     string // the recorder, if not the chairman
	amount                 int64
	ch                     fees
}

// record posts a payout on the recorder's own step-up and returns its entry id.
func (w *mworld) record(t *testing.T, p payoutRec) (string, error) {
	t.Helper()
	if p.ref == "" {
		p.ref = "NIP-" + digest()[:12]
	}
	// The recorder is the chairman by default: the poster authorises holds,
	// and from 0015 the officer who authorised a hold never records its payout
	// (authorising and attesting are different duties). Recording as the
	// poster made authoriser_never_records refuse first, covering for the
	// rule under test (TestPayout23, 24, 26).
	by := p.by
	if by == "" {
		by = w.s2
	}
	proof := stepUp(t, by, "withdrawal_payout")
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('withdrawal_payout', $1, $2) RETURNING id`,
			by, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the step-up act: %w", err)
		}
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label,
			  act_id, request_id, hold_id, payout_reference)
			VALUES ($1, 'withdrawal_out', current_date, $2, 'treasurer', 'Treasurer', $3, $4, $5, $6) RETURNING id`,
			w.c.s.id, by, act, nz(p.req), nz(p.hold), p.ref).Scan(&id); err != nil {
			return err
		}
		return w.c.s.insertLines(x, id, w.payout(p.member, p.amount, p.ch)...)
	})
	return id, err
}

// flag: one item for a person, as they will read it.
type flag struct{ kind, expected, observed string }

// flagsOf: everything flagged on an entry.
func flagsOf(t *testing.T, entry string) []flag {
	t.Helper()
	var fs []flag
	rows, err := db.Query(bg(), `SELECT kind, expected, observed FROM ledger_discrepancies WHERE entry_id = $1 ORDER BY kind`, entry)
	fixture(t, err, "the discrepancies")
	defer rows.Close()
	for rows.Next() {
		var f flag
		fixture(t, rows.Scan(&f.kind, &f.expected, &f.observed), "a discrepancy")
		fs = append(fs, f)
	}
	return fs
}

// flags: the kinds flagged on an entry.
func flags(t *testing.T, entry string) []string {
	t.Helper()
	var ks []string
	rows, err := db.Query(bg(), `SELECT kind FROM ledger_discrepancies WHERE entry_id = $1 ORDER BY kind`, entry)
	fixture(t, err, "the discrepancies")
	defer rows.Close()
	for rows.Next() {
		var k string
		fixture(t, rows.Scan(&k), "a discrepancy")
		ks = append(ks, k)
	}
	return ks
}

// recordedAndFlagged: the payout committed, and `kind` is among its flags.
func recordedAndFlagged(t *testing.T, entry string, err error, kind, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("REFUSED — %s happened, and the ledger refused to record it: %v", what, err)
	}
	if got := flags(t, entry); !slices.Contains(got, kind) {
		t.Fatalf("UNFLAGGED — %s was recorded with flags %v; want %q for a person to explain", what, got, kind)
	}
	// A flag a resolver can act on says what was expected and what happened.
	for _, f := range flagsOf(t, entry) {
		if f.kind == kind && (strings.TrimSpace(f.expected) == "" || strings.TrimSpace(f.observed) == "") {
			t.Fatalf("MUTE FLAG — %q on %s says expected %q, observed %q: a resolver would have to go to the logs", kind, what, f.expected, f.observed)
		}
	}
}

// =============================================================================
// THE HOLD: EVERY JUDGEMENT BEFORE THE MONEY MOVES
// =============================================================================

func TestPayout01_AHoldNeedsAnApprovedRequest(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "a request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "one signature of two")
	_, err = w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo})
	refused(t, err, "a payout authorised on a request with one signature of the two", refusedAs("hold_request_approved"))
}

func TestPayout02_AHoldIsForTheAmountApproved(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: 700000})
	refused(t, err, "₦7,000 authorised on a request approved for ₦6,000", refusedAs("hold_matches_request"))
}

// The charges are the server's, computed when the hold is placed, whatever the
// client supplied.
func TestPayout03_TheHoldCarriesTheServersCharge(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	h, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo, charges: &fees{}})
	fixture(t, err, "a hold whose client supplied no charges")
	var got fees
	var f, v, l, m *int64
	fixture(t, db.QueryRow(bg(), `SELECT partner_fee_kobo, `+vatColumn()+`, statutory_levy_kobo, argon_margin_kobo
		FROM withdrawal_holds WHERE id = $1`, h).Scan(&f, &v, &l, &m), "the hold")
	for _, p := range []struct {
		src *int64
		dst *int64
	}{{f, &got.fee}, {v, &got.vat}, {l, &got.levy}, {m, &got.margin}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
	if got != payFees() {
		t.Fatalf("WRONG CHARGE — the hold carries %+v; the series say %+v", got, payFees())
	}
}

func TestPayout04_AnUnsetFloorRefusesTheHold(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "the floor undecided")
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo})
	refused(t, err, "a payout authorised in a society that has not set its account floor", refusedAs("account_floor_unset"))
}

func TestPayout05_TheFloorStaysBehind(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 950000)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: 950000})
	refused(t, err, "₦9,500 + ₦10.75 out of ₦10,000 savings with a ₦1,000 floor", refusedAs("withdrawal_within_withdrawable"))
}

// ₦8,990 fits within the ₦9,000 withdrawable; ₦8,990 + ₦10.75 does not.
func TestPayout06_TheChargesCountAgainstWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 899000)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: 899000})
	refused(t, err, "₦8,990 + ₦10.75 against ₦9,000 withdrawable", refusedAs("withdrawal_within_withdrawable"))
}

// guarantorWorld: a disbursed ₦24,000 loan both guarantors guarantee, and the
// first guarantor's ₦10,000 savings.
func guarantorWorld(t *testing.T) (*mworld, string) {
	t.Helper()
	w := newMoneyWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	w.saved(t, w.g1Mem, loanSavings)
	return w, loan
}

// The conservative default: the FULL outstanding ₦24,000 is held back while the
// guarantee stands.
func TestPayout07_AGuaranteeHoldsBackTheLoansOutstanding(t *testing.T) {
	w, _ := guarantorWorld(t)
	req := w.approved(t, w.g1Mem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.g1Mem, amount: payKobo})
	refused(t, err, "₦6,000 authorised out of a guarantor's savings while ₦24,000 they guarantee is outstanding",
		refusedAs("withdrawal_within_withdrawable"))
}

// Per PERSON, not per member record.
func TestPayout08_AGuaranteeHoldsThroughThePersonsOtherRecord(t *testing.T) {
	w, _ := guarantorWorld(t)
	other := w.c.s.member(w.g1)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, other), "the guarantor's second record")
	w.saved(t, other, loanSavings)
	req := w.approved(t, other, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: other, amount: payKobo})
	refused(t, err, "the guarantor authorising a payout through their second member record", refusedAs("withdrawal_within_withdrawable"))
}

// Money already held for one payout is not available to another: ₦6,010.75
// held, then ₦6,010.75 more against ₦9,000.
func TestPayout09_OpenHoldsCountAgainstWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	w.held(t, w.bMem, payKobo)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo})
	refused(t, err, "a second ₦6,010.75 authorised while the first is still held", refusedAs("withdrawal_within_withdrawable"))
}

func TestPayout10_OneOpenHoldPerRequest(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.bMem, loanSavings)
	req, _ := w.held(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo})
	refused(t, err, "the same approved request authorised to execute twice", uniqueOrCheck("hold_once_open"))
}

func TestPayout11_AHoldRestsOnAStepUp(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo, act: "none"})
	refused(t, err, "a payout authorised with no step-up behind it", refusedAs("hold_needs_step_up"))
}

func TestPayout12_TheActIsTheAuthorisersOwn(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo, by: w.s2})
	refused(t, err, "the treasurer's authorisation resting on the chairman's step-up", refusedAs("hold_act_is_holders"))
}

func TestPayout13_TheActIsAnAuthorisation(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo, act: "withdrawal_payout"})
	refused(t, err, "an authorisation resting on a step-up taken to record a payout", refusedAs("hold_act_kind"))
}

func TestPayout14_TheActIsFromThisTransaction(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo, actID: anAct(t, w.poster, "withdrawal_authorisation")})
	refused(t, err, "an authorisation resting on an act committed earlier", refusedAs("hold_act_same_transaction"))
}

func TestPayout15_OneStepUpAuthorisesOnce(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.bMem, loanSavings)
	a, b := w.approved(t, w.bMem, payKobo), w.approved(t, w.bMem, payKobo)
	proof := stepUp(t, w.poster, "withdrawal_authorisation")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('withdrawal_authorisation', $1, $2) RETURNING id`,
			w.poster, proof).Scan(&act); err != nil {
			return err
		}
		for _, req := range []string{a, b} {
			if _, err := x.Exec(bg(), `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, actor_user_id, act_id)
				VALUES ($1, $2, $3, $4, $5, $6)`, req, w.c.s.id, w.bMem, payKobo, w.poster, act); err != nil {
				return err
			}
		}
		return nil
	})
	refused(t, err, "one step-up authorising two payouts", uniqueOrCheck("hold_act_once"))
}

func TestPayout16_AReleaseSaysWhy(t *testing.T) {
	w := newMoneyWorld(t)
	_, h := w.held(t, w.bMem, payKobo)
	refused(t, w.release(h, "  "), "a hold released with no reason given", refusedAs("hold_release_names_reason"))
}

// Releasing a hold whose payout was recorded would free money that has gone.
func TestPayout17_APaidHoldIsNeverReleased(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	fixture(t, err, "the payout recorded")
	refused(t, w.release(h, "The transfer failed"), "a paid hold released", refusedAs("hold_release_unpaid"))
}

func TestPayout_Control_HoldingExactlyWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	amount := borrowerWithdrawable - transferCharges(borrowerWithdrawable).total() // ₦8,989.25 + ₦10.75 = ₦9,000
	if amount+transferCharges(amount).total() != borrowerWithdrawable {
		t.Fatalf("FIXTURE: the arithmetic: %d + %d is not %d", amount, transferCharges(amount).total(), borrowerWithdrawable)
	}
	req := w.approved(t, w.bMem, amount)
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: amount})
	accepted(t, err, "₦8,989.25 + ₦10.75 held, exactly the ₦9,000 the borrower may withdraw")
}

func TestPayout_Control_ARepaidLoanHoldsNothing(t *testing.T) {
	w, loan := guarantorWorld(t)
	_, err := w.repay(loan, loanPrincipal)
	fixture(t, err, "the loan repaid in full")
	req := w.approved(t, w.g1Mem, payKobo)
	_, err = w.hold(t, holdSpec{req: req, member: w.g1Mem, amount: payKobo})
	accepted(t, err, "a guarantor's payout authorised once the loan they guaranteed owes nothing")
}

// A released hold frees what it held: the same request may be authorised again.
func TestPayout_Control_AReleasedHoldFreesItsAmount(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, 850000)
	fixture(t, w.release(h, "The transfer was refused by the recipient's bank"), "the release")
	_, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: 850000})
	accepted(t, err, "the same request authorised again once its first hold was released")
}

// =============================================================================
// THE RECORDING: WHATEVER HAPPENED, RECORDED; ANY DIFFERENCE, FLAGGED
// =============================================================================

// Money left with no authorisation behind it. That is exactly what a person
// must see, and the ledger must say it left.
func TestPayout18_APayoutWithNoHoldIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	e, err := w.record(t, payoutRec{member: w.bMem, amount: payKobo, ch: payFees()})
	recordedAndFlagged(t, e, err, "payout_without_hold", "a ₦6,000 payout nobody authorised")
}

func TestPayout19_APayoutOfADifferentAmountIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	e, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: 650000, ch: payFees()})
	recordedAndFlagged(t, e, err, "payout_amount_differs", "₦6,500 paid on a ₦6,000 hold")
}

// The double payout the wallet platform suffered: the money left twice.
func TestPayout20_ASecondPayoutOnOneHoldIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.bMem, loanSavings)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	fixture(t, err, "the first payout")
	e, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	recordedAndFlagged(t, e, err, "hold_paid_twice", "a second ₦6,000 transfer on the same hold")
}

// Released as failed, then the transfer went through after all.
func TestPayout21_APayoutOnAReleasedHoldIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	fixture(t, w.release(h, "VFD reported the transfer failed"), "the release")
	e, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	recordedAndFlagged(t, e, err, "payout_on_released_hold", "a transfer that completed after its hold was released")
}

// ₦10,000 paid on a ₦6,000 hold leaves ₦10,000 of savings below zero.
func TestPayout22_APayoutThatOverdrawsIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	e, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: 1000000, ch: payFees()})
	recordedAndFlagged(t, e, err, "payout_overdraws", "a payout larger than the member's savings")
}

// Refused: whose savings fall is the hold's to say; the correct record exists.
func TestPayout23_ThePayoutIsTheHoldsMember(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, loanSavings)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.g1Mem, amount: payKobo, ch: payFees()})
	refused(t, err, "the borrower's held payout recorded against a guarantor's savings", refusedAs("payout_from_hold_member"))
}

// Refused: the charges were fixed when the payout was authorised.
func TestPayout24_ThePayoutCarriesTheHoldsCharge(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: fees{}})
	refused(t, err, "a held payout recorded with no charge where the hold says ₦10.75", refusedAs("entry_charge_is_the_holds"))
}

// Refused: one bank transfer is one record. Recording it again is a recording
// error, not a second payment.
func TestPayout25_ATransferIsRecordedOnce(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.bMem, loanSavings)
	req, h := w.held(t, w.bMem, payKobo)
	ref := "NIP-ONCE-" + digest()[:8]
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees(), ref: ref})
	fixture(t, err, "the payout")
	_, err = w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees(), ref: ref})
	refused(t, err, "the same bank transfer recorded a second time", uniqueOrCheck("payout_recorded_once"))
}

func TestPayout26_APayoutNamesItsTransfer(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees(), ref: "  "})
	refused(t, err, "a payout recorded without the transfer that paid it", refusedAs("withdrawal_names_payout_reference"))
}

// The series changed between authorisation and payout; the hold's charge
// stands. (0011 recomputed it at recording and would refuse.)
func TestPayout27_TheHoldsChargeStandsWhenTheSeriesChange(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", feeComponent(), "2026-10-01", srcFee, band{flat: 9999})
	fixture(t, err, "a new fee series, in force before today")
	_, err = w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	accepted(t, err, "a payout recorded with the charges it was authorised at")
}

func TestPayout_Control_APayoutExactlyAsHeldIsRecordedWithNoFlag(t *testing.T) {
	w := newMoneyWorld(t)
	req, h := w.held(t, w.bMem, payKobo)
	e, err := w.record(t, payoutRec{req: req, hold: h, member: w.bMem, amount: payKobo, ch: payFees()})
	accepted(t, err, "the held payout, as held")
	if got := flags(t, e); len(got) != 0 {
		t.Fatalf("FLAGGED — a payout exactly as authorised was flagged %v; a person would chase nothing", got)
	}
}

// =============================================================================
// CONTRIBUTIONS: MONEY THAT ARRIVED IS RECORDED
// =============================================================================

// The society has decided its fee, VAT and margin, but not the levy (it waits
// on Compliance). Money arrived; it is recorded, uncharged, and flagged.
func TestPayout28_AContributionWhoseChargeCannotBeDeterminedIsRecordedAndFlagged(t *testing.T) {
	w := newMoneyWorld(t)
	allSeriesBut(t, w.c.s.id, "contribution_in", "statutory_levy")
	err := w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, fees{})})
	recordedAndFlagged(t, w.lastEntry, err, "charge_undetermined", "₦25,000 that arrived in a society whose levy is undecided")
	// UNDETERMINED, not zero: uncharged with a flag that said "free" would later
	// read as correctly free. And which component is missing.
	for _, f := range flagsOf(t, w.lastEntry) {
		if f.kind == "charge_undetermined" && (!strings.Contains(strings.ToLower(f.observed), "undetermined") || !strings.Contains(f.expected, "statutory_levy")) {
			t.Fatalf("MISLEADING FLAG — expected %q, observed %q: it must say the charge is undetermined, and that statutory_levy is what is missing", f.expected, f.observed)
		}
	}
}
