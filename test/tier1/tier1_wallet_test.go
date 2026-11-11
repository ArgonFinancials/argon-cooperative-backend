//go:build tier1

// ACCEPTANCE TESTS — THE WALLET WORKER (stage 3, 2026-11-10). LEDGER_CONTRACT
// §13: the last approval pays. The worker sends the transfer the last
// signature authorised, asks VFD what happened, and records VFD's answer; the
// database (0028) releases or posts. Run against a FAKE VFD in the test (the
// client itself is suite vfd's), and, in GREEN, as argon_wallet with the real
// grants (findings 16 and 25).
//
// What they pin:
//   - it pays the last signature's hold, and never an officer's;
//   - no transfer before VFD confirms the member's account;
//   - VFD refusing the send, or never receiving it, fails the transfer now,
//     and the hold is released;
//   - a timeout is not a failure: the hold stays, and VFD is asked again;
//   - it never sends twice, even when the first answer was lost;
//   - "not found" fails a transfer only after the settlement window;
//   - an answer that cannot be read decides nothing;
//   - VFD's success is posted as the payout.
package tier1

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argon/backend/internal/vfd"
	"github.com/argon/backend/internal/wallet"
	"github.com/jackc/pgx/v5/pgxpool"
)

// switchVFD: a fake VFD whose answer on each path the test can change
// between ticks.
type switchVFD struct {
	*fakeVFD
	mu sync.Mutex
}

func newSwitchVFD(t *testing.T, modes map[string]string) *switchVFD {
	t.Helper()
	return &switchVFD{fakeVFD: newFakeVFD(t, modes)}
}

func (s *switchVFD) set(path, mode string) {
	s.fakeVFD.mu.Lock()
	s.fakeVFD.mode[path] = mode
	s.fakeVFD.mu.Unlock()
}

func (s *switchVFD) count(path string) int {
	n := 0
	for _, c := range s.all() {
		if c.Path == path {
			n++
		}
	}
	return n
}

// walletWorkerFor: the worker under test, for one society: the real one (as
// argon_wallet in GREEN), or under ARGON_TEST_WALLET_FAKE the reference
// (walletfake_test.go) with its break, on the owner's connection.
func walletWorkerFor(t *testing.T, society, url string) wallet.Worker {
	t.Helper()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, err := vfd.New(vfd.Config{BaseURL: url, AccessToken: vfdToken, Timeout: 300 * time.Millisecond, Log: log})
	fixture(t, err, "the VFD client")
	pool := db
	brk, fake := os.LookupEnv("ARGON_TEST_WALLET_FAKE")
	if !fake {
		pool = walletPool(t)
	}
	cfg := wallet.Config{DB: pool, VFD: client, Log: log, AskEvery: time.Millisecond, LostAfter: 50 * time.Millisecond, Society: society}
	var w wallet.Worker
	if fake {
		w, err = newRefWallet(cfg, brk)
	} else {
		w, err = wallet.New(cfg)
	}
	fixture(t, err, "the wallet worker")
	return w
}

func walletPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if walletDB == nil {
		t.Fatalf("FIXTURE: the wallet runs as argon_wallet, as in production, and this run has no such connection: " +
			"set ARGON_TEST_WALLET_URL and ARGON_TEST_GRANTS (mutate.sh's GREEN does)")
	}
	return walletDB
}

func tick(t *testing.T, w wallet.Worker) wallet.Round {
	t.Helper()
	r, err := w.Tick(bg())
	if err != nil {
		t.Fatalf("THE TICK FAILED — %v", err)
	}
	return r
}

// lastSignatureHold: a request signed to quorum, the last signature carrying
// the hold (§13), for the borrower's ₦6,000.
func (w *vworld) lastSignatureHold(t *testing.T) string {
	t.Helper()
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "the request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the first signature")
	fixture(t, w.signAndHold(t, req, w.s2Mem, "chairman", req, payKobo), "the last signature, with the hold")
	var hold string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM withdrawal_holds WHERE request_id = $1`, req).Scan(&hold), "the hold")
	return hold
}

type paymentState struct {
	transfer, sent, outcome string
	released, posted        bool
	events                  int
}

func paymentOf(t *testing.T, hold string) paymentState {
	t.Helper()
	var p paymentState
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(t.id::text, ''),
		coalesce((SELECT result FROM vfd_transfer_events WHERE transfer_id = t.id AND kind = 'sent'), ''),
		coalesce((SELECT result FROM vfd_transfer_outcomes WHERE transfer_id = t.id), ''),
		EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = $1),
		EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = $1),
		(SELECT count(*) FROM vfd_transfer_events WHERE transfer_id = t.id)
		FROM withdrawal_holds h LEFT JOIN vfd_transfers t ON t.hold_id = h.id WHERE h.id = $1`, hold).
		Scan(&p.transfer, &p.sent, &p.outcome, &p.released, &p.posted, &p.events), "the payment's state")
	return p
}

var okModes = map[string]string{"/transfer/recipient": "enquiry_ok", "/transfer": "transfer_ok", "/transactions": "requery_ok"}

func modes(over map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range okModes {
		m[k] = v
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

func TestWalletWorker01_TheLastSignaturesHoldIsPaidAndPosted(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transactions": "requery_ok_payout"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	p := paymentOf(t, hold)
	if p.transfer == "" || p.sent != "accepted" {
		t.Fatalf("NOT SENT — %+v; want a transfer VFD accepted", p)
	}
	b := f.all()
	for _, c := range b {
		if c.Path == "/transfer" && (vfdStr(c.Body, "reference") != vfdRef(hold) || vfdStr(c.Body, "toAccount") != w.memAcct || vfdStr(c.Body, "fromAccount") != w.socAcct || vfdStr(c.Body, "amount") != "6000.00") {
			t.Fatalf("WRONG TRANSFER — %v; want %s, from %s to %s, 6000.00", c.Body, vfdRef(hold), w.socAcct, w.memAcct)
		}
	}
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	if p = paymentOf(t, hold); p.outcome != "paid" || !p.posted {
		t.Fatalf("NOT POSTED — VFD said the transfer succeeded: %+v; want paid, and the payout posted", p)
	}
}

func TestWalletWorker02_AnOfficersHoldIsNotTheSystemsToPay(t *testing.T) {
	w := newWalletWorld(t)
	_, hold := w.held(t, w.bMem, payKobo)
	f := newSwitchVFD(t, okModes)
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if p := paymentOf(t, hold); p.transfer != "" || f.count("/transfer") != 0 {
		t.Fatalf("PAID AN OFFICER'S HOLD — %+v, %d sends; a hold authorised by hand is the officers' to pay", p, f.count("/transfer"))
	}
}

func TestWalletWorker03_NoTransferBeforeVFDConfirmsTheAccount(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer/recipient": "enquiry_none"}))
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if p := paymentOf(t, hold); p.transfer != "" || f.count("/transfer") != 0 {
		t.Fatalf("SENT UNCONFIRMED — VFD did not confirm the member's account and %+v, %d sends", p, f.count("/transfer"))
	}
}

func TestWalletWorker04_ARefusedSendFailsAndTheHoldIsReleased(t *testing.T) {
	for _, mode := range []string{"transfer_refused", "dead"} {
		w := newWalletWorld(t)
		hold := w.lastSignatureHold(t)
		url := deadAfterEnquiry(t) // the account confirmed, then no connection
		if mode == "transfer_refused" {
			url = newSwitchVFD(t, modes(map[string]string{"/transfer": "transfer_refused"})).srv.URL
		}
		tick(t, walletWorkerFor(t, w.c.s.id, url))
		if p := paymentOf(t, hold); p.outcome != "failed" || !p.released {
			t.Fatalf("NOT FAILED — VFD %s the send: %+v; want failed, and the hold released", mode, p)
		}
	}
}

// deadAfterEnquiry: a VFD that confirms the account, then closes the
// connection on the transfer (the request never reaches it).
func deadAfterEnquiry(t *testing.T) string {
	t.Helper()
	f := newFakeVFD(t, map[string]string{"/transfer/recipient": "enquiry_ok"})
	h := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/transfer" {
			if hj, ok := rw.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				c.Close()
				return
			}
		}
		h.ServeHTTP(rw, r)
	})
	return f.srv.URL
}

func TestWalletWorker05_ATimeoutIsNotAFailureAndNothingIsSentTwice(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer": "slow", "/transactions": "requery_99"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	for i := 0; i < 3; i++ {
		time.Sleep(5 * time.Millisecond)
		tick(t, wk)
	}
	p := paymentOf(t, hold)
	if p.sent != "unknown" || p.outcome != "" || p.released {
		t.Fatalf("DECIDED ON A GUESS — no answer to the send, VFD pending since: %+v; want unknown, no outcome, the hold still held", p)
	}
	if n := f.count("/transfer"); n != 1 {
		t.Fatalf("SENT %d TIMES — an automatic retry is how a payment goes out twice", n)
	}
	if f.count("/transactions") == 0 {
		t.Fatalf("NOT ASKED — a send with no answer must be asked about")
	}
}

// A send whose answer was never recorded (the run died between sending and
// recording) is recorded as unknown and asked about: never resent.
func TestWalletWorker06_ALostSendIsAskedAboutNeverResent(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	tr, err := w.transfer(xfer{hold: hold})
	fixture(t, err, "a transfer whose send was never recorded")
	time.Sleep(80 * time.Millisecond)
	f := newSwitchVFD(t, modes(map[string]string{"/transactions": "requery_99"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	if n := f.count("/transfer"); n != 0 {
		t.Fatalf("RESENT — a transfer whose first answer was lost was sent again (%d)", n)
	}
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM vfd_transfer_events WHERE transfer_id = $1 AND kind = 'sent' AND result = 'unknown'`, tr).Scan(&n), "the lost send")
	if n != 1 {
		t.Fatalf("NOT RECORDED — a transfer with no send recorded is never recorded as an unknown send (%d): its hold is held forever, asked about by nobody", n)
	}
	if f.count("/transactions") == 0 {
		t.Fatalf("NOT ASKED — a lost send must be asked about")
	}
}

// "Not found" moments after sending is settlement lag; after the window, a
// failure. An answer that cannot be read is neither.
func TestWalletWorker07_NotFoundFailsOnlyAfterTheWindow(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transactions": "requery_108"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	if p := paymentOf(t, hold); p.outcome != "" || p.released {
		t.Fatalf("FAILED ON LAG — 'not found' minutes after sending: %+v; want no outcome yet", p)
	}

	// The same, three hours after the send (written as the owner: the wallet
	// cannot date VFD's answers, P65).
	w2 := newWalletWorld(t)
	hold2 := w2.lastSignatureHold(t)
	tr, err := w2.transfer(xfer{hold: hold2})
	fixture(t, err, "the transfer")
	mustEvent(t, tr, "sent", "accepted", "00", 0, "3 hours")
	f2 := newSwitchVFD(t, modes(map[string]string{"/transactions": "garbage"}))
	wk2 := walletWorkerFor(t, w2.c.s.id, f2.srv.URL)
	tick(t, wk2)
	if p := paymentOf(t, hold2); p.outcome != "" {
		t.Fatalf("FAILED ON NOTHING — an unreadable answer three hours on: %+v; want no outcome", p)
	}
	f2.set("/transactions", "requery_108")
	time.Sleep(5 * time.Millisecond)
	tick(t, wk2)
	if p := paymentOf(t, hold2); p.outcome != "failed" || !p.released {
		t.Fatalf("NOT FAILED — 'not found' three hours after the send: %+v; want failed, the hold released", p)
	}
}

// ---- money arriving (0029) ------------------------------------------------------

// inflowWorld: a society whose own VFD account is the one the fake's inflow
// requery names.
func inflowWorld(t *testing.T) *vworld {
	t.Helper()
	w := &vworld{mworld: newMoneyWorld(t), socAcct: vfdNumber()}
	_, err := recordSocietyAccount(w.c.s.id, w.socAcct, opName, opHost, socAcctReason)
	fixture(t, err, "the society's VFD account")
	return w
}

// arrived: a notification as the webhook keeps it, claiming ₦10,000.
func arrived(t *testing.T, ref, acct string) string {
	t.Helper()
	raw := `{"reference":"` + ref + `","amount":"10000.00","account_number":"` + acct + `","originator_account_name":"ADEBAYO FOLAKE","originator_bank":"000013"}`
	n, err := vfdNotification(raw, ref, acct, "")
	fixture(t, err, "the notification")
	return n
}

func inflowsOf(t *testing.T, ref string) (n int, kobo int64, acct string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT count(*), coalesce(max(amount_kobo), 0), coalesce(max(account_number), '') FROM vfd_inflows WHERE vfd_reference = $1`, ref).Scan(&n, &kobo, &acct), "the inflows")
	return
}

func inflowAttempts(t *testing.T, note string) []string {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT result || coalesce(': ' || detail, '') FROM vfd_notification_attempts WHERE notification_id = $1 ORDER BY at`, note)
	fixture(t, err, "the attempts")
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		fixture(t, rows.Scan(&s), "an attempt")
		out = append(out, s)
	}
	return out
}

// Believed only on VFD's word: VFD's amount, not the notification's.
func TestWalletWorker08_AnInflowIsVFDsAmountNotTheNotifications(t *testing.T) {
	w := inflowWorld(t)
	ref := vfdRefNew()
	note := arrived(t, ref, w.socAcct)
	f := newSwitchVFD(t, map[string]string{"/transactions": "requery_inflow:" + w.socAcct})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	n, kobo, acct := inflowsOf(t, ref)
	if n != 1 || kobo != 600000 || acct != w.socAcct {
		t.Fatalf("WRONG — %d inflow(s) of %d kobo to %s; want one, of ₦6,000 as VFD says (the notification claimed ₦10,000), to the society's account", n, kobo, acct)
	}
	if a := inflowAttempts(t, note); len(a) != 1 || a[0] != "confirmed" {
		t.Fatalf("UNRECORDED — the attempts read %v; want one, confirmed", a)
	}
}

func TestWalletWorker09_NothingIsRecordedWhileVFDDoesNotSaySucceeded(t *testing.T) {
	w := inflowWorld(t)
	ref := vfdRefNew()
	note := arrived(t, ref, w.socAcct)
	f := newSwitchVFD(t, map[string]string{"/transactions": "requery_108"})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if n, _, _ := inflowsOf(t, ref); n != 0 {
		t.Fatalf("BELIEVED — VFD has no such transaction and %d inflow(s) were recorded", n)
	}
	if a := inflowAttempts(t, note); len(a) != 1 || !strings.HasPrefix(a[0], "not_confirmed: ") {
		t.Fatalf("MUTE — the attempts read %v; want one, not_confirmed, saying what VFD said", a)
	}
}

func TestWalletWorker10_ANotificationDeliveredTwiceIsRecordedOnce(t *testing.T) {
	w := inflowWorld(t)
	ref := vfdRefNew()
	arrived(t, ref, w.socAcct)
	second := arrived(t, ref, w.socAcct)
	f := newSwitchVFD(t, map[string]string{"/transactions": "requery_inflow:" + w.socAcct})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if n, _, _ := inflowsOf(t, ref); n != 1 {
		t.Fatalf("CREDITED %d TIMES — one VFD reference, delivered twice", n)
	}
	if a := inflowAttempts(t, second); len(a) != 1 || a[0] != "duplicate" {
		t.Fatalf("UNRECORDED — the second delivery's attempts read %v; want duplicate", a)
	}
}

// VFD says the money went to an account that is no society's: unmatched,
// for a person, never credited to the account the notification named.
func TestWalletWorker11_MoneyToNoSocietysAccountIsUnmatched(t *testing.T) {
	w := newWalletWorld(t) // VFD will say the money went to an account nobody recorded
	ref := vfdRefNew()
	raw := `{"reference":"` + ref + `","amount":"6000.00","account_number":"` + w.socAcct + `"}`
	note, err := vfdNotification(raw, ref, w.socAcct, "")
	fixture(t, err, "a notification naming the society's account")
	// VFD says the money went to an account nobody recorded.
	f := newSwitchVFD(t, map[string]string{"/transactions": "requery_inflow:" + vfdNumber()})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if n, _, _ := inflowsOf(t, ref); n != 0 {
		t.Fatalf("GUESSED — VFD says the money went elsewhere, and %d inflow(s) were credited to the account the notification named", n)
	}
	if a := inflowAttempts(t, note); len(a) != 1 || !strings.HasPrefix(a[0], "unmatched: ") {
		t.Fatalf("UNSEEN — the attempts read %v; want unmatched, saying where VFD says it went", a)
	}
}

// No statement endpoint is known: reconciliation records that it could not
// run, and never that nothing was missed.
func TestWalletWorker12_ReconciliationRecordsThatItCouldNotRun(t *testing.T) {
	w := newWalletWorld(t)
	f := newSwitchVFD(t, okModes)
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	var outcome, detail string
	err := db.QueryRow(bg(), `SELECT outcome, detail FROM vfd_reconciliation_runs WHERE account_number = $1`, w.socAcct).Scan(&outcome, &detail)
	if err != nil {
		t.Fatalf("SILENT — no reconciliation run recorded for the society's account (%v): a missed inflow would never be found, and nobody told", err)
	}
	if outcome != "could_not_run" || !strings.Contains(detail, "statement") {
		t.Fatalf("WRONG — the run reads %s: %q; want could_not_run, saying no statement endpoint is known", outcome, detail)
	}
}

// A claim and its inflow committed at the same moment: each transaction saw
// no sign of the other, so neither matched (0032). The tick matches them.
func TestWalletWorker13_AClaimARaceMissedIsMatchedOnTheTick(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	fixture(t, one(`ALTER TABLE contribution_claims DISABLE TRIGGER contribution_claims_recorded`), "test scaffolding: the race (the claim unseen)")
	fixture(t, one(`ALTER TABLE vfd_inflows DISABLE TRIGGER vfd_inflows_recorded`), "test scaffolding: the race (the inflow unseen)")
	_, cerr := claim(w.c.s.id, w.bMem, w.borrower, s, 140000)
	in, ierr := inflowFrom(w.c.s.id, w.socAcct, s, "", 140000)
	fixture(t, one(`ALTER TABLE contribution_claims ENABLE TRIGGER contribution_claims_recorded`), "test scaffolding: the claim's trigger back")
	fixture(t, one(`ALTER TABLE vfd_inflows ENABLE TRIGGER vfd_inflows_recorded`), "test scaffolding: the inflow's trigger back")
	fixture(t, cerr, "the claim")
	fixture(t, ierr, "the inflow")
	wantNotPosted(t, in, "the race, before the tick")
	f := newSwitchVFD(t, okModes)
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	wantPosted(t, in, w.bMem, 140000, "a claim and its inflow that a race left unmatched, after the tick")
}
