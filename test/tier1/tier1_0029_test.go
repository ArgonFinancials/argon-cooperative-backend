//go:build tier1

// Suite 0029 — MONEY ARRIVING. VFD's notifications kept as received; an
// inflow confirmed once per VFD reference into a society's own account;
// reconciliation that says when it could not run; and what a person must act
// on (stage 3, 2026-11-10). docs/VFD_INTEGRATION.md §2, requirements 2–6.
//
//	KEPT: every notification exactly as received, never edited; every
//	attempt to process one, saying why when it did not confirm.
//	ONCE: an inflow per VFD reference (a unique index, not a body hash),
//	positive, into the society's own VFD account, never edited.
//	SAID: a reconciliation run says what it found, over a real period.
//	SEEN: a stuck transfer, a failed one, a hold that never went out, a
//	notification not confirmed, money nobody claimed, reconciliation that
//	could not run: each is on wallet_exceptions for a person.
package tier1

import (
	"fmt"
	"testing"
)

func vfdNotification(raw, reference, account, ago string) (string, error) {
	var id string
	at := "now()"
	if ago != "" {
		at = "now() - interval '" + ago + "'"
	}
	err := db.QueryRow(bg(), `INSERT INTO vfd_notifications (kind, raw, signed, reference, account_number, received_at)
		VALUES ('inflow', $1, false, $2, $3, `+at+`) RETURNING id::text`, raw, nz(reference), nz(account)).Scan(&id)
	return id, err
}

func vfdAttempt(n, result, detail string) error {
	return one(`INSERT INTO vfd_notification_attempts (notification_id, result, detail) VALUES ($1, $2, $3)`, n, result, nz(detail))
}

func vfdInflow(society, account, ref string, kobo int64) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo, originator_name)
		VALUES ($1, $2, $3, $4, 'ADEBAYO FOLAKE') RETURNING id::text`, ref, society, account, kobo).Scan(&id)
	return id, err
}

func vfdRun(account, outcome, detail, from string) error {
	return one(`INSERT INTO vfd_reconciliation_runs (account_number, period_from, period_to, outcome, detail)
		VALUES ($1, `+from+`, now(), $2, $3)`, account, outcome, nz(detail))
}

func vfdRefNew() string { return fmt.Sprintf("VFD-IN-%d-%s", seq.Add(1), digest()[:8]) }

func TestWalletInflow01_ANotificationIsKeptAsReceived(t *testing.T) {
	n, err := vfdNotification(`{"reference":"X"}`, "X", "", "")
	fixture(t, err, "a notification")
	refused(t, one(`UPDATE vfd_notifications SET raw = '{}' WHERE id = $1`, n), "a notification rewritten", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM vfd_notifications WHERE id = $1`, n), "a notification deleted", refusedAs("vfd_records_append_only"))
	refused(t, one(`INSERT INTO vfd_notifications (kind, raw, signed) VALUES ('payment', '{}', false)`), "a notification of no known kind", refusedAs("vfd_notification_kind_known"))
}

func TestWalletInflow02_AnAttemptSaysWhyItDidNotConfirm(t *testing.T) {
	n, err := vfdNotification(`{"reference":"Y"}`, "Y", "", "")
	fixture(t, err, "a notification")
	for _, r := range []string{"unmatched", "malformed", "not_confirmed"} {
		refused(t, vfdAttempt(n, r, " "), "an attempt that did not confirm, saying nothing of why: "+r, refusedAs("vfd_attempt_says_why"))
	}
	refused(t, vfdAttempt(n, "ignored", "because"), "an attempt with no known result", refusedAs("vfd_attempt_result_known"))
	fixture(t, vfdAttempt(n, "not_confirmed", "VFD: not found yet"), "an attempt")
	refused(t, one(`UPDATE vfd_notification_attempts SET result = 'confirmed' WHERE notification_id = $1`, n), "an attempt rewritten", refusedAs("vfd_records_append_only"))
}

func TestWalletInflow03_AnInflowIsRecordedOncePerVFDReference(t *testing.T) {
	w := newWalletWorld(t)
	ref := vfdRefNew()
	_, err := vfdInflow(w.c.s.id, w.socAcct, ref, 500000)
	fixture(t, err, "an inflow")
	_, err = vfdInflow(w.c.s.id, w.socAcct, ref, 500000)
	refused(t, err, "one VFD reference recorded twice (a retried notification credited twice)", uniqueOrCheck("vfd_inflow_once"))
}

func TestWalletInflow04_AnInflowLandsInTheSocietysOwnAccount(t *testing.T) {
	w := newWalletWorld(t)
	other := newWalletWorld(t)
	for _, c := range []struct{ acct, what string }{
		{w.memAcct, "a member's own account"}, {other.socAcct, "another society's account"}, {"1099999999", "an account nobody recorded"},
	} {
		_, err := vfdInflow(w.c.s.id, c.acct, vfdRefNew(), 500000)
		refused(t, err, "an inflow to "+c.what, refusedAs("vfd_inflow_to_society_account"))
	}
}

func TestWalletInflow05_AnInflowIsMoneyArrivingAndNeverEdited(t *testing.T) {
	w := newWalletWorld(t)
	_, err := vfdInflow(w.c.s.id, w.socAcct, vfdRefNew(), 0)
	refused(t, err, "an inflow of nothing", refusedAs("vfd_inflow_positive"))
	id, err := vfdInflow(w.c.s.id, w.socAcct, vfdRefNew(), 500000)
	fixture(t, err, "an inflow")
	refused(t, one(`UPDATE vfd_inflows SET amount_kobo = 1 WHERE id = $1`, id), "an inflow's amount changed", refusedAs("vfd_records_append_only"))
}

func TestWalletInflow06_AReconciliationSaysWhatItFoundOverARealPeriod(t *testing.T) {
	w := newWalletWorld(t)
	refused(t, vfdRun(w.socAcct, "could_not_run", " ", "now() - interval '1 day'"), "a reconciliation that says nothing", refusedAs("vfd_reconciliation_says_what"))
	refused(t, vfdRun(w.socAcct, "matched", "all present", "now() + interval '1 day'"), "a reconciliation over a period that ends before it begins", refusedAs("vfd_reconciliation_period"))
}

type vfdException struct{ kind, society, subject string }

func vfdExceptions(t *testing.T, society string) map[string]vfdException {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT kind, coalesce(society_id::text, ''), subject_id::text, detail FROM wallet_exceptions WHERE society_id = $1`, society)
	fixture(t, err, "the exceptions")
	defer rows.Close()
	out := map[string]vfdException{}
	for rows.Next() {
		var e vfdException
		var detail string
		fixture(t, rows.Scan(&e.kind, &e.society, &e.subject, &detail), "an exception")
		if detail == "" {
			t.Fatalf("MUTE — exception %s says nothing a person can act on", e.kind)
		}
		out[e.kind+"|"+e.subject] = e
	}
	return out
}

func wantVfdException(t *testing.T, got map[string]vfdException, kind, subject, what string) {
	t.Helper()
	if _, ok := got[kind+"|"+subject]; !ok {
		t.Fatalf("UNSEEN — %s is not on wallet_exceptions as %s (a person would never know): %v", what, kind, got)
	}
}

func TestWalletInflow07_WhatAPersonMustActOnIsSeen(t *testing.T) {
	w := newWalletWorld(t)
	// A transfer VFD has not answered, 31 minutes on.
	_, _, stuck := w.heldTransfer(t)
	mustEvent(t, stuck, "sent", "unknown", "", 0, "31 minutes")
	// A failed one.
	_, _, failed := w.heldTransferFor(t, w.g1Mem)
	ev := mustEvent(t, failed, "sent", "refused", "61", 0, "")
	_, err := vfdOutcome(failed, "failed", ev)
	fixture(t, err, "the failure")
	// A notification ten minutes old, never confirmed.
	n, err := vfdNotification(`{"reference":"Z"}`, "Z", w.socAcct, "11 minutes")
	fixture(t, err, "a notification")
	fixture(t, vfdAttempt(n, "not_confirmed", "VFD: pending"), "its attempt")
	// Money nobody has claimed.
	in, err := vfdInflow(w.c.s.id, w.socAcct, vfdRefNew(), 500000)
	fixture(t, err, "an inflow")
	// Reconciliation that could not run.
	fixture(t, vfdRun(w.socAcct, "could_not_run", "no statement endpoint is known", "now() - interval '1 day'"), "the run")
	var rid string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM vfd_reconciliation_runs WHERE account_number = $1`, w.socAcct).Scan(&rid), "the run's id")

	got := vfdExceptions(t, w.c.s.id)
	wantVfdException(t, got, "transfer_stuck", stuck, "a transfer VFD has not answered in 31 minutes")
	wantVfdException(t, got, "transfer_failed", failed, "a failed transfer")
	wantVfdException(t, got, "notification_unconfirmed", n, "a notification never confirmed")
	wantVfdException(t, got, "inflow_unclaimed", in, "money nobody has claimed")
	wantVfdException(t, got, "reconciliation_not_run", rid, "reconciliation that could not run")
}

// A hold the last signature placed that has not gone out, ten minutes on.
func TestWalletInflow08_AHoldThatNeverWentOutIsSeen(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	if _, ok := vfdExceptions(t, w.c.s.id)["hold_not_sent|"+hold]; ok {
		t.Fatalf("EARLY — a hold placed a moment ago is already an exception: the worker has not had its minute")
	}
	fixture(t, one(`ALTER TABLE withdrawal_holds DISABLE TRIGGER USER`), "test scaffolding: date the hold back")
	err := one(`UPDATE withdrawal_holds SET held_at = now() - interval '11 minutes' WHERE id = $1`, hold)
	fixture(t, one(`ALTER TABLE withdrawal_holds ENABLE TRIGGER USER`), "test scaffolding: the triggers back")
	fixture(t, err, "the hold, dated back")
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "hold_not_sent", hold, "an approved hold with no transfer, eleven minutes on")
}

// What is settled is not an exception: a transfer paid, a notification
// confirmed, a run that matched.
func TestWalletInflow_Control_WhatIsSettledIsNotAnException(t *testing.T) {
	w := newWalletWorld(t)
	_, _, tr := w.heldTransfer(t)
	mustEvent(t, tr, "sent", "accepted", "00", 0, "40 minutes")
	ok := mustEvent(t, tr, "requeried", "succeeded", "00", payKobo, "")
	_, err := vfdOutcome(tr, "paid", ok)
	accepted(t, err, "the payout")
	n, err := vfdNotification(`{"reference":"OK"}`, "OK", w.socAcct, "20 minutes")
	accepted(t, err, "a notification")
	accepted(t, vfdAttempt(n, "confirmed", ""), "confirmed")
	accepted(t, vfdRun(w.socAcct, "matched", "every inflow present", "now() - interval '1 day'"), "a run that matched")
	for k := range vfdExceptions(t, w.c.s.id) {
		t.Fatalf("NOISE — %s is an exception, and it is settled", k)
	}
}
