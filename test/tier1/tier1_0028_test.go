//go:build tier1

// Suite 0028 — THE WALLET: VFD ACCOUNTS, AND THE LAST APPROVAL PAYS. Written
// before the migration (stage 3, 2026-11-10). LEDGER_CONTRACT §13 and the
// reviewer's rulings of 2026-11-10 (§12): each member has their own VFD
// account, opened here; a payout goes into it and no other; the society's
// account is recorded by the operator from VFD's answer.
//
//	ACCOUNTS: VFD's ten digits, recorded once, with VFD's client id; one for
//	each member (in their own society) and one for each society; never
//	edited. The society's by the operator: named, the login, why, logged.
//
//	THE LAST SIGNATURE CARRIES THE HOLD: a hold may rest on the act of the
//	signature that reached quorum, in its transaction (no second human act).
//
//	A TRANSFER is the hold's and nothing else's: its reference derived from
//	the hold (so a retry can never be a second payment), its amount the
//	hold's, FROM the society's account TO the member's own, on an open hold,
//	one per hold, SENT ONCE (no automatic retry, §13).
//
//	WHAT VFD SAID is recorded each time it is asked, and never edited. An
//	OUTCOME rests on it: PAID only on VFD's success; FAILED only on a
//	definitive answer (refused or never reached at the send, or not found
//	after the settlement window). A timeout is not a failure.
//
//	THE SYSTEM RECORDS, NO USER STANDS IN: a failure releases the hold, a
//	success posts the payout, in the outcome's own transaction, attributed to
//	the outcome. Every 0012 recording rule carries over: a different amount
//	is posted and flagged. A person cannot release, or record a payout on, a
//	hold whose transfer is at VFD.
package tier1

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type vworld struct {
	*mworld
	socAcct, memAcct string
}

func vfdNumber() string { return fmt.Sprintf("10%08d", seq.Add(1)%100000000) }

func vfdRef(hold string) string { return "Argon-" + strings.ReplaceAll(hold, "-", "") }

const socAcctReason = "Recording the society's VFD account, as VFD's name enquiry reports it"

func recordSocietyAccount(society, number, operator, login, reason string) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `SELECT operator_record_society_vfd_account($1, $2, $3, $4, $5, $6, $7, $8)::text`,
			society, number, "C-SOC-"+number, "A-SOC-"+number, "ARGON TEST SOCIETY", nz(operator), nz(login), nz(reason)).Scan(&id)
	})
	return id, err
}

func memberAccount(society, member, number, client string) error {
	return one(`INSERT INTO vfd_accounts (society_id, member_id, account_number, vfd_client_id, vfd_account_id, account_name, how)
		VALUES ($1, $2, $3, $4, 'A-'||$3, 'A MEMBER', 'opened_here')`, society, member, number, nz(client))
}

func newWalletWorld(t *testing.T) *vworld {
	t.Helper()
	w := &vworld{mworld: newMoneyWorld(t), socAcct: vfdNumber(), memAcct: vfdNumber()}
	_, err := recordSocietyAccount(w.c.s.id, w.socAcct, opName, opHost, socAcctReason)
	fixture(t, err, "the society's VFD account")
	fixture(t, memberAccount(w.c.s.id, w.bMem, w.memAcct, "C-MEM-"+w.memAcct), "the member's VFD account")
	return w
}

type xfer struct {
	hold, ref, from, to string
	amount              int64
}

func (w *vworld) transfer(h xfer) (string, error) {
	if h.ref == "" {
		h.ref = vfdRef(h.hold)
	}
	if h.from == "" {
		h.from = w.socAcct
	}
	if h.to == "" {
		h.to = w.memAcct
	}
	if h.amount == 0 {
		h.amount = payKobo
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, h.hold, h.ref, h.from, h.to, h.amount).Scan(&id)
	})
	return id, err
}

// heldTransfer: an approved request, its hold, and its transfer, as fixtures.
func (w *vworld) heldTransfer(t *testing.T) (req, hold, transfer string) {
	t.Helper()
	req, hold = w.held(t, w.bMem, payKobo)
	transfer, err := w.transfer(xfer{hold: hold})
	fixture(t, err, "the transfer")
	return
}

// event: what VFD said. ago: how long before now it was said ("" now).
func vfdEvent(transfer, kind, result, code string, amount int64, ago string) (string, error) {
	var id string
	at := "now()"
	if ago != "" {
		at = "now() - interval '" + ago + "'"
	}
	var amt any
	if amount != 0 {
		amt = amount
	}
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_id, amount_kobo, at)
			VALUES ($1, $2, $3, $4, 'TXN-'||$1::text, $5, `+at+`) RETURNING id`, transfer, kind, result, nz(code), amt).Scan(&id)
	})
	return id, err
}

func vfdOutcome(transfer, result, evidence string) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO vfd_transfer_outcomes (transfer_id, result, evidence_id) VALUES ($1, $2, $3) RETURNING id`,
			transfer, result, evidence).Scan(&id)
	})
	return id, err
}

func mustEvent(t *testing.T, transfer, kind, result, code string, amount int64, ago string) string {
	t.Helper()
	id, err := vfdEvent(transfer, kind, result, code, amount, ago)
	fixture(t, err, "VFD's answer: "+kind+" "+result)
	return id
}

// paid: sent, accepted; requeried, succeeded for `amount`; the outcome.
func vfdPaid(t *testing.T, transfer string, amount int64) (string, error) {
	t.Helper()
	mustEvent(t, transfer, "sent", "accepted", "00", 0, "")
	ev := mustEvent(t, transfer, "requeried", "succeeded", "00", amount, "")
	return vfdOutcome(transfer, "paid", ev)
}

func openHolds(t *testing.T, member string) int64 {
	t.Helper()
	var n int64
	fixture(t, db.QueryRow(bg(), `SELECT open_holds_kobo FROM member_position($1)`, member).Scan(&n), "the member's open holds")
	return n
}

// =============================================================================
// ACCOUNTS
// =============================================================================

func TestWalletPay01_TheSocietysAccountIsRecordedByTheOperatorNamedWithWhy(t *testing.T) {
	for _, c := range []struct{ op, login, reason, code string }{
		{" ", opHost, socAcctReason, "operator_names_themselves"},
		{opName, "", socAcctReason, "operator_login_recorded"},
		{opName, opHost, "", "operator_gives_reason"},
	} {
		s := newSociety(t)
		_, err := recordSocietyAccount(s.id, vfdNumber(), c.op, c.login, c.reason)
		refused(t, err, "a society's VFD account recorded without "+c.code, refusedAs(c.code))
	}
	s := newSociety(t)
	n := vfdNumber()
	_, err := recordSocietyAccount(s.id, n, opName, opHost, socAcctReason)
	accepted(t, err, "the operator recording a society's VFD account")
	var logged string
	fixture(t, db.QueryRow(bg(), `SELECT coalesce((SELECT new_value || '|' || operator_name || '|' || server_login || '|' || reason
		FROM society_settings_log WHERE society_id = $1 AND setting = 'vfd_account'), '')`, s.id).Scan(&logged), "the log")
	if logged != n+"|"+opName+"|"+opHost+"|"+socAcctReason {
		t.Fatalf("UNLOGGED — the settings log reads %q; want the number, who, the login and why", logged)
	}
}

func TestWalletPay02_AnAccountIsVFDsTenDigitsRecordedOnceWithItsClient(t *testing.T) {
	w := newWalletWorld(t)
	for _, n := range []string{"12345", "ABCDEFGHIJ", "10123456789", ""} {
		refused(t, memberAccount(w.c.s.id, w.g1Mem, n, "C-1"), fmt.Sprintf("an account number %q", n), refusedAs("vfd_account_number_shape"))
	}
	refused(t, memberAccount(w.c.s.id, w.g1Mem, w.memAcct, "C-1"), "one VFD account number recorded for two members", uniqueOrCheck("vfd_account_number_once"))
	refused(t, memberAccount(w.c.s.id, w.g1Mem, vfdNumber(), " "), "an account with no VFD client id", refusedAs("vfd_account_names_client"))
}

func TestWalletPay03_OneAccountForEachMemberInTheirOwnSocietyAndOneForTheSociety(t *testing.T) {
	w := newWalletWorld(t)
	refused(t, memberAccount(w.c.s.id, w.bMem, vfdNumber(), "C-2"), "a second VFD account for one member", uniqueOrCheck("vfd_account_one_each"))
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	refused(t, err, "a second VFD account for one society", uniqueOrCheck("vfd_account_one_each"))
	other := newSociety(t)
	refused(t, memberAccount(other.id, w.g1Mem, vfdNumber(), "C-3"), "a member's account recorded under another society", refusedAs("vfd_account_members_society"))
}

func TestWalletPay04_AnAccountIsNeverEdited(t *testing.T) {
	w := newWalletWorld(t)
	refused(t, one(`UPDATE vfd_accounts SET account_number = $2 WHERE member_id = $1`, w.bMem, vfdNumber()), "an account's number changed", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM vfd_accounts WHERE member_id = $1`, w.bMem), "an account deleted", refusedAs("vfd_records_append_only"))
}

// =============================================================================
// THE LAST SIGNATURE CARRIES THE HOLD
// =============================================================================

// signAndHold: the signature and the hold in one transaction, the hold
// resting on the signature's own act (or on `onAct`, an act from elsewhere).
func (w *vworld) signAndHold(t *testing.T, req, member, post string, holdReq string, amount int64) error {
	t.Helper()
	user := userOf(t, member)
	proof := stepUp(t, user, "approval_signature")
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('approval_signature', $1, $2) RETURNING id`, user, proof).Scan(&act); err != nil {
			return fmt.Errorf("FIXTURE: the act: %w", err)
		}
		if _, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, $3, $4)`, req, member, post, act); err != nil {
			return fmt.Errorf("FIXTURE: the signature: %w", err)
		}
		_, err := x.Exec(bg(), `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, actor_user_id, act_id)
			VALUES ($1, $2, $3, $4, $5, $6)`, holdReq, w.c.s.id, w.bMem, amount, user, act)
		return err
	})
}

func TestWalletPay05_TheLastSignatureCarriesTheHold(t *testing.T) {
	w := newWalletWorld(t)
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the first signature")
	accepted(t, w.signAndHold(t, req, w.s2Mem, "chairman", req, payKobo), "the signature that reaches quorum, carrying the hold")

	// The signature's act authorises THIS request's hold, no other.
	other, err := w.raise(w.bMem, payKobo/4, 2)
	fixture(t, err, "a second request")
	fixture(t, w.c.s.sign(other, w.s1Mem, "treasurer"), "its first signature")
	third, err := w.raise(w.bMem, payKobo/4, 2)
	fixture(t, err, "a third request")
	fixture(t, w.c.s.sign(third, w.s1Mem, "treasurer"), "its first signature")
	fixture(t, w.c.s.sign(third, w.s2Mem, "chairman"), "its second signature")
	// The hold matches `third` in every way but its authority, so only the
	// rule under test can refuse it.
	refused(t, w.signAndHold(t, other, w.s2Mem, "chairman", third, payKobo/4), "a signature on one request carrying another request's hold",
		refusedAs("hold_rests_on_this_requests_signature"))
}

// =============================================================================
// THE TRANSFER IS THE HOLD'S
// =============================================================================

func TestWalletPay06_ATransfersReferenceIsTheHolds(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	_, err := w.transfer(xfer{hold: hold, ref: "Argon-" + digest()[:24]})
	refused(t, err, "a transfer under a reference not derived from its hold", refusedAs("vfd_reference_is_the_holds"))
}

func TestWalletPay07_ATransferIsForTheHeldAmount(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	_, err := w.transfer(xfer{hold: hold, amount: payKobo + 100})
	refused(t, err, "a transfer of more than was held", refusedAs("vfd_transfer_is_the_holds"))
}

func TestWalletPay08_ATransferGoesToTheMembersOwnAccountAndNoOther(t *testing.T) {
	w := newWalletWorld(t)
	other := vfdNumber()
	fixture(t, memberAccount(w.c.s.id, w.g1Mem, other, "C-G1"), "another member's account")
	_, hold := w.held(t, w.bMem, payKobo)
	_, err := w.transfer(xfer{hold: hold, to: other})
	refused(t, err, "a payout sent to another member's account", refusedAs("vfd_transfer_to_members_account"))
	_, err = w.transfer(xfer{hold: hold, to: "0123456789"})
	refused(t, err, "a payout sent to an account nobody recorded", refusedAs("vfd_transfer_to_members_account"))

	// A member with no account has nowhere to be paid: no transfer.
	w2 := newWalletWorld(t)
	w2.saved(t, w2.g1Mem, loanSavings)
	_, hold2 := w2.held(t, w2.g1Mem, payKobo)
	_, err = w2.transfer(xfer{hold: hold2, to: w2.memAcct})
	refused(t, err, "a payout for a member with no VFD account", refusedAs("vfd_transfer_to_members_account"))
}

func TestWalletPay09_ATransferComesFromTheSocietysAccount(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	_, err := w.transfer(xfer{hold: hold, from: w.memAcct})
	refused(t, err, "a payout sent from the member's own account", refusedAs("vfd_transfer_from_society_account"))
}

func TestWalletPay10_ATransferIsOnAnOpenHold(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	fixture(t, w.release(hold, "the member withdrew the request"), "the release")
	_, err := w.transfer(xfer{hold: hold})
	refused(t, err, "a transfer on a released hold", refusedAs("vfd_transfer_on_open_hold"))
}

func TestWalletPay11_OneTransferPerHold(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, _ := w.heldTransfer(t)
	_, err := w.transfer(xfer{hold: hold})
	refused(t, err, "a second transfer for one hold", uniqueOrCheck("vfd_transfer_once"))
}

func TestWalletPay12_ATransferIsSentOnce(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	mustEvent(t, tr, "sent", "unknown", "", 0, "")
	_, err := vfdEvent(tr, "sent", "accepted", "00", 0, "")
	refused(t, err, "a transfer sent a second time (an automatic retry is how a payment goes out twice)", refusedAs("vfd_sent_once"))
}

func TestWalletPay13_ARequeryFollowsTheSend(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	_, err := vfdEvent(tr, "requeried", "succeeded", "00", payKobo, "")
	refused(t, err, "VFD's answer to a requery of a transfer never sent", refusedAs("vfd_requery_after_send"))
}

// =============================================================================
// AN OUTCOME RESTS ON WHAT VFD SAID
// =============================================================================

func TestWalletPay14_PaidOnlyOnVFDsSuccess(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	sent := mustEvent(t, tr, "sent", "accepted", "00", 0, "")
	_, err := vfdOutcome(tr, "paid", sent)
	refused(t, err, "paid on VFD accepting the send (accepted is not arrived)", refusedAs("vfd_paid_on_success"))
	// Pending, though VFD names the amount: still not arrived.
	pending := mustEvent(t, tr, "requeried", "pending", "00", payKobo, "")
	_, err = vfdOutcome(tr, "paid", pending)
	refused(t, err, "paid on a pending requery", refusedAs("vfd_paid_on_success"))
	_, err = vfdEvent(tr, "requeried", "succeeded", "00", 0, "")
	refused(t, err, "VFD's success recorded with no amount (the payout posts what VFD paid)", refusedAs("vfd_success_names_amount"))

	_, _, tr2 := w.heldTransferFor(t, w.g1Mem)
	mustEvent(t, tr2, "sent", "accepted", "00", 0, "")
	elsewhere := mustEvent(t, tr2, "requeried", "succeeded", "00", payKobo, "")
	_, err = vfdOutcome(tr, "paid", elsewhere)
	refused(t, err, "paid on another transfer's success", refusedAs("vfd_outcome_evidence_is_its_own"))
}

// heldTransferFor: as heldTransfer, for another member with their own account.
func (w *vworld) heldTransferFor(t *testing.T, member string) (req, hold, transfer string) {
	t.Helper()
	n := vfdNumber()
	fixture(t, memberAccount(w.c.s.id, member, n, "C-"+n), "the member's account")
	w.saved(t, member, loanSavings)
	req, hold = w.held(t, member, payKobo)
	transfer, err := w.transfer(xfer{hold: hold, to: n})
	fixture(t, err, "the transfer")
	return
}

// A timeout is not a failure; "not found" moments after sending is settlement
// lag. Only a definitive answer fails a transfer.
func TestWalletPay15_FailedOnlyOnADefinitiveAnswer(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	unknown := mustEvent(t, tr, "sent", "unknown", "", 0, "")
	_, err := vfdOutcome(tr, "failed", unknown)
	refused(t, err, "failed on a send that timed out", refusedAs("vfd_failed_only_definitive"))
	soon := mustEvent(t, tr, "requeried", "not_found", "108", 0, "")
	_, err = vfdOutcome(tr, "failed", soon)
	refused(t, err, "failed on 'not found' moments after sending", refusedAs("vfd_failed_only_definitive"))
	pending := mustEvent(t, tr, "requeried", "pending", "00", 0, "")
	_, err = vfdOutcome(tr, "failed", pending)
	refused(t, err, "failed on a pending requery", refusedAs("vfd_failed_only_definitive"))

	for _, sent := range []string{"refused", "not_reached"} {
		_, _, tr := w.heldTransferFor(t, map[string]string{"refused": w.g1Mem, "not_reached": w.g2Mem}[sent])
		ev := mustEvent(t, tr, "sent", sent, "61", 0, "")
		_, err := vfdOutcome(tr, "failed", ev)
		accepted(t, err, "failed on a send VFD "+sent)
	}
	w2 := newWalletWorld(t)
	_, _, tr2 := w2.heldTransfer(t)
	mustEvent(t, tr2, "sent", "unknown", "", 0, "3 hours")
	late := mustEvent(t, tr2, "requeried", "not_found", "108", 0, "")
	_, err = vfdOutcome(tr2, "failed", late)
	accepted(t, err, "failed on 'not found' three hours after sending, past the settlement window")
}

// =============================================================================
// THE SYSTEM RECORDS; NO USER STANDS IN FOR IT
// =============================================================================

func TestWalletPay16_AFailureReleasesTheHoldInItsOwnTransaction(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, tr := w.heldTransfer(t)
	if openHolds(t, w.bMem) == 0 {
		t.Fatalf("FIXTURE: the hold is not open")
	}
	ev := mustEvent(t, tr, "sent", "refused", "61", 0, "")
	o, err := vfdOutcome(tr, "failed", ev)
	accepted(t, err, "the transfer failed")
	var actor *string
	var by, reason string
	err = db.QueryRow(bg(), `SELECT actor_user_id::text, coalesce(vfd_outcome_id::text, ''), coalesce(reason, '') FROM withdrawal_hold_releases WHERE hold_id = $1`, hold).Scan(&actor, &by, &reason)
	if err != nil {
		t.Fatalf("NOT RELEASED — the transfer failed and the hold is still open: %v", err)
	}
	if actor != nil || by != o {
		t.Fatalf("WRONG ATTRIBUTION — released by user %v, outcome %q; want no user, the outcome %s", actor, by, o)
	}
	if !strings.Contains(reason, "61") {
		t.Fatalf("WRONG REASON — %q; want VFD's own answer (61) in it", reason)
	}
	if n := openHolds(t, w.bMem); n != 0 {
		t.Fatalf("STILL HELD — %d kobo held after the failure", n)
	}
}

func payoutBySystem(t *testing.T, o string) (entry, hold, ref, label, valueDate string, actor *string) {
	t.Helper()
	err := db.QueryRow(bg(), `SELECT id::text, coalesce(hold_id::text, ''), coalesce(payout_reference, ''), actor_post_label, value_date::text, actor_user_id::text
		FROM ledger_entries WHERE vfd_outcome_id = $1`, o).Scan(&entry, &hold, &ref, &label, &valueDate, &actor)
	if err != nil {
		t.Fatalf("NOT POSTED — VFD confirmed the transfer and no payout names the outcome: %v", err)
	}
	return
}

func TestWalletPay17_ASuccessPostsThePayoutInItsOwnTransaction(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, tr := w.heldTransfer(t)
	before := vfdSavingsOf(t, w.bMem)
	o, err := vfdPaid(t, tr, payKobo)
	accepted(t, err, "VFD confirmed the transfer")
	entry, h, ref, label, valueDate, actor := payoutBySystem(t, o)
	var sentOn string
	fixture(t, db.QueryRow(bg(), `SELECT (at AT TIME ZONE 'Africa/Lagos')::date::text FROM vfd_transfer_events WHERE transfer_id = $1 AND kind = 'sent'`, tr).Scan(&sentOn), "the send's date")
	switch {
	case actor != nil:
		t.Fatalf("A USER STANDS IN — the payout names user %s; the system records it", *actor)
	case h != hold || ref != vfdRef(hold):
		t.Fatalf("WRONG LINK — hold %s, reference %s; want %s, %s", h, ref, hold, vfdRef(hold))
	case strings.TrimSpace(label) == "":
		t.Fatalf("UNLABELLED — the system's payout says nothing of who recorded it")
	case valueDate != sentOn:
		t.Fatalf("WRONG DATE — the payout is dated %s; the money left on %s", valueDate, sentOn)
	}
	if got, want := before-vfdSavingsOf(t, w.bMem), payKobo+payFees().total(); got != want {
		t.Fatalf("WRONG AMOUNT — savings fell by %d; want the amount and the hold's charges, %d", got, want)
	}
	if f := flags(t, entry); len(f) != 0 {
		t.Fatalf("FLAGGED — a payout exactly as held carries %v", f)
	}
	if n := openHolds(t, w.bMem); n != 0 {
		t.Fatalf("STILL HELD — %d kobo counted as held after the payout", n)
	}
}

func vfdSavingsOf(t *testing.T, member string) int64 {
	t.Helper()
	var n int64
	fixture(t, db.QueryRow(bg(), `SELECT savings_kobo FROM member_position($1)`, member).Scan(&n), "the member's savings")
	return n
}

// Record what happened: a different amount is posted, and flagged.
func TestWalletPay18_ASuccessForADifferentAmountIsPostedAndFlagged(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	o, err := vfdPaid(t, tr, payKobo-5000)
	accepted(t, err, "VFD confirmed a transfer of ₦50 less than held")
	entry, _, _, _, _, _ := payoutBySystem(t, o)
	recordedAndFlagged(t, entry, nil, "payout_amount_differs", "a payout of a different amount, confirmed by VFD")
}

func TestWalletPay19_APersonCannotReleaseAHoldWhoseTransferIsAtVFD(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, tr := w.heldTransfer(t)
	refused(t, w.release(hold, "the member asked us to cancel"), "a person releasing a hold whose transfer is at VFD", refusedAs("hold_release_while_at_vfd"))
	mustEvent(t, tr, "sent", "unknown", "", 0, "")
	refused(t, w.release(hold, "VFD has not answered"), "a person releasing a hold on a guess", refusedAs("hold_release_while_at_vfd"))
}

func TestWalletPay20_APersonCannotRecordAPayoutOnAHoldWhoseTransferIsAtVFD(t *testing.T) {
	w := newWalletWorld(t)
	req, hold, _ := w.heldTransfer(t)
	_, err := w.record(t, payoutRec{req: req, hold: hold, member: w.bMem, amount: payKobo, ch: payFees()})
	refused(t, err, "an officer recording by hand a payout VFD is making", refusedAs("payout_at_vfd_by_system"))
}

func TestWalletPay21_ATransferHasOneOutcome(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	sent := mustEvent(t, tr, "sent", "refused", "61", 0, "")
	_, err := vfdOutcome(tr, "failed", sent)
	fixture(t, err, "the failure")
	// A second outcome of the OTHER kind: a second failure would be refused by
	// the release's own key first (one release per hold), covering for the rule.
	ok := mustEvent(t, tr, "requeried", "succeeded", "00", payKobo, "")
	_, err = vfdOutcome(tr, "paid", ok)
	refused(t, err, "a second outcome for one transfer", uniqueOrCheck("vfd_outcome_once"))
}

func TestWalletPay22_WhatVFDSaidIsNeverEditedAndTheSystemsEntryIsAnOutcomes(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	ev := mustEvent(t, tr, "sent", "unknown", "", 0, "")
	refused(t, one(`UPDATE vfd_transfer_events SET result = 'refused' WHERE id = $1`, ev), "VFD's answer rewritten", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM vfd_transfer_events WHERE id = $1`, ev), "VFD's answer deleted", refusedAs("vfd_records_append_only"))
	refused(t, one(`UPDATE vfd_transfers SET to_account = '0123456789' WHERE id = $1`, tr), "a transfer redirected", refusedAs("vfd_records_append_only"))
	err := tx(func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_post_label) VALUES ($1, 'transfer_out', current_date, 'Nobody')`, w.c.s.id)
		return err
	})
	refused(t, err, "an entry with no user and no VFD outcome behind it", refusedAs("entry_names_actor_or_vfd"))
}

// The system's records name an outcome, and only that outcome's own: an
// entry naming a failure is no payout, and a failure releases its own hold.
func TestWalletPay23_TheSystemsRecordsAreTheirOutcomesOwn(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, tr := w.heldTransfer(t)
	ev := mustEvent(t, tr, "sent", "refused", "61", 0, "")
	failed, err := vfdOutcome(tr, "failed", ev)
	fixture(t, err, "the failure")
	// A whole payout, lines and the hold's charges, so only the rule under
	// test can refuse it.
	err = tx(func(x pgx.Tx) error {
		var id string
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_post_label, hold_id, payout_reference, vfd_outcome_id)
			VALUES ($1, 'withdrawal_out', current_date, 'Argon system', $2, $3, $4) RETURNING id`, w.c.s.id, hold, vfdRef(hold), failed).Scan(&id); err != nil {
			return err
		}
		return w.c.s.insertLines(x, id, w.payout(w.bMem, payKobo, payFees())...)
	})
	refused(t, err, "a payout entry naming VFD's failure", refusedAs("vfd_payout_is_the_outcomes"))
	w.saved(t, w.g1Mem, loanSavings)
	_, other := w.held(t, w.g1Mem, payKobo)
	refused(t, one(`INSERT INTO withdrawal_hold_releases (hold_id, reason, vfd_outcome_id) VALUES ($1, 'VFD said so', $2)`, other, failed),
		"another hold released on this transfer's failure", refusedAs("release_is_the_outcomes"))
}

func TestWalletPay_Control_TheWholePayout(t *testing.T) {
	w := newWalletWorld(t)
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the first signature")
	accepted(t, w.signAndHold(t, req, w.s2Mem, "chairman", req, payKobo), "the last signature, with the hold")
	var hold string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM withdrawal_holds WHERE request_id = $1`, req).Scan(&hold), "the hold")
	tr, err := w.transfer(xfer{hold: hold})
	accepted(t, err, "the transfer")
	_, err = vfdEvent(tr, "sent", "accepted", "00", 0, "")
	accepted(t, err, "VFD accepting it")
	_, err = vfdEvent(tr, "requeried", "pending", "00", 0, "")
	accepted(t, err, "VFD saying it is pending")
	ok, err := vfdEvent(tr, "requeried", "succeeded", "00", payKobo, "")
	accepted(t, err, "VFD saying it succeeded")
	_, err = vfdOutcome(tr, "paid", ok)
	accepted(t, err, "the outcome")
}
