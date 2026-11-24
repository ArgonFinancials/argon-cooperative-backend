//go:build tier1

// ACCEPTANCE TESTS — 0039, VFD AS PRODUCTION PROVED IT (2026-11-24, the
// reviewer, on docs/VFD_INTEGRATION.md §1.6, written from 193 transfers looked
// up at VFD). The database's half: what an outcome may rest on, where a
// transfer leaves from, what a person is shown, where a notification came from.
package tier1

import (
	"testing"
)

// vfdEventTxn: VFD's answer with a transactionStatus of its own.
func vfdEventTxn(t *testing.T, transfer, kind, result, code, txn string, amount int64, ago string) string {
	t.Helper()
	at := "now()"
	if ago != "" {
		at = "now() - interval '" + ago + "'"
	}
	var amt any
	if amount != 0 {
		amt = amount
	}
	var id string
	fixture(t, db.QueryRow(bg(), `INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_status, amount_kobo, at)
		VALUES ($1, $2, $3, $4, $5, $6, `+at+`) RETURNING id`, transfer, kind, result, nz(code), nz(txn), amt).Scan(&id), "VFD's answer")
	return id
}

func TestVfdRules01_PaidOnTheSends00OrARequerys00With00(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	refused(t, vfdOutcomeErr(tr, "paid", mustEvent(t, tr, "sent", "refused", "61", 0, "")), "paid on a send VFD refused", refusedAs("vfd_paid_on_success"))
	ninety := vfdEventTxn(t, tr, "requeried", "succeeded", "00", "99", payKobo, "")
	refused(t, vfdOutcomeErr(tr, "paid", ninety), "paid on transactionStatus 99 (the one real 99 never arrived)", refusedAs("vfd_paid_on_success"))
	_, _, tr2 := w.heldTransferFor(t, w.g1Mem)
	accepted(t, vfdOutcomeErr(tr2, "paid", mustEvent(t, tr2, "sent", "accepted", "00", 0, "")), "paid on VFD's 00 to the send")
}

func TestVfdRules02_FailedOnlyOnOneOhEightTwicePastThirtyMinutes(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	refused(t, vfdOutcomeErr(tr, "failed", mustEvent(t, tr, "sent", "refused", "61", 0, "3 hours")), "failed on a send VFD refused (3 of 32 had been sent)", refusedAs("vfd_failed_only_definitive"))
	once := mustEvent(t, tr, "requeried", "not_found", "108", 0, "1 hour")
	refused(t, vfdOutcomeErr(tr, "failed", once), "failed on one 108", refusedAs("vfd_failed_only_definitive"))

	_, _, tr2 := w.heldTransferFor(t, w.g1Mem)
	mustEvent(t, tr2, "sent", "unknown", "", 0, "40 minutes")
	mustEvent(t, tr2, "requeried", "not_found", "108", 0, "35 minutes") // within 30 minutes of the send? no: 5 minutes after it
	late := mustEvent(t, tr2, "requeried", "not_found", "108", 0, "")
	refused(t, vfdOutcomeErr(tr2, "failed", late), "failed on 108 twice, the first five minutes after the send", refusedAs("vfd_failed_only_definitive"))

	_, _, tr3 := w.heldTransferFor(t, w.g2Mem)
	mustEvent(t, tr3, "sent", "unknown", "", 0, "3 hours")
	vfdEventTxn(t, tr3, "requeried", "not_found", "01", "", 0, "2 hours")
	other := vfdEventTxn(t, tr3, "requeried", "not_found", "01", "", 0, "1 hour")
	refused(t, vfdOutcomeErr(tr3, "failed", other), "failed on 'no transaction' that is not 108", refusedAs("vfd_failed_only_definitive"))
}

func TestVfdRules03_ATransferLeavesFromThePool(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	_, err := w.transfer(xfer{hold: hold, from: w.socAcct})
	refused(t, err, "a payout sent from the society's own account (it holds nothing: VFD sweeps it into the pool)", refusedAs("vfd_transfer_from_pool"))
}

func TestVfdRules04_ThePoolIsRecordedOnceAndNeverEdited(t *testing.T) {
	ensurePool()
	refused(t, one(`INSERT INTO vfd_pool (account_number, account_id, client_id, client_name) VALUES ('1000000009', 'X', 'Y', 'Z')`),
		"a second pool", want{codes: []string{"23505"}})
	refused(t, one(`UPDATE vfd_pool SET account_number = '1000000009'`), "the pool rewritten", refusedAs("vfd_records_append_only"))
}

// What VFD said that is neither 00/00 nor 108 is shown for a person.
func TestVfdRules05_AnythingElseVFDSaysIsShownForAPerson(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	mustEvent(t, tr, "sent", "refused", "61", 0, "1 hour")
	vfdEventTxn(t, tr, "requeried", "pending", "00", "91", 0, "")
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM wallet_exceptions WHERE kind = 'transfer_needs_vfd' AND subject_id = $1`, tr).Scan(&n), "the exceptions")
	if n != 1 {
		t.Fatalf("UNSEEN — VFD answered 91 and wallet_exceptions shows %d transfer_needs_vfd; want one", n)
	}
}

// Paid on the send's 00: the payout posts what was sent.
func TestVfdRules06_PaidOnTheSendPostsWhatWasSent(t *testing.T) {
	w := newWalletWorld(t)
	_, hold, tr := w.heldTransfer(t)
	accepted(t, vfdOutcomeErr(tr, "paid", mustEvent(t, tr, "sent", "accepted", "00", 0, "")), "paid on the send's 00")
	var bank int64
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(-sum(l.amount_kobo), 0) FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id JOIN accounts a ON a.id = l.account_id
		WHERE e.hold_id = $1 AND a.role = 'bank'`, hold).Scan(&bank), "the payout")
	if bank != payKobo {
		t.Fatalf("NOT POSTED — the payout moved %d kobo out of the bank; want what was sent, %d", bank, payKobo)
	}
}

func TestVfdRules07_ANotificationSaysWhereItCameFrom(t *testing.T) {
	refused(t, one(`INSERT INTO vfd_notifications (kind, raw, signed) VALUES ('inflow', '{}', false)`), "a notification from nowhere said", want{codes: []string{"23502"}})
	refused(t, one(`INSERT INTO vfd_notifications (kind, raw, signed, source) VALUES ('inflow', '{}', false, 'email')`), "a notification from a source unknown", refusedAs("vfd_notification_source_known"))
}

// The control: 108 twice, both well past 30 minutes, releases.
func TestVfdRules_Control_OneOhEightTwicePastThirtyMinutesReleases(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	accepted(t, vfdOutcomeErr(tr, "failed", vfdFailEvidence(t, tr)), "failed on 108 twice, both past the window")
}
