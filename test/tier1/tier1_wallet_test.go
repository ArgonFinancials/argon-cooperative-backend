//go:build tier1

// ACCEPTANCE TESTS — THE WALLET WORKER (stage 3, 2026-11-10). LEDGER_CONTRACT
// §13: the last approval pays. The worker sends the transfer the last
// signature authorised, asks VFD what happened, and records VFD's answer; the
// database (0028) releases or posts. Run against a FAKE VFD in the test (the
// client itself is suite vfd's), and, in GREEN, as argon_wallet with the real
// grants (findings 16 and 25).
//
// What they pin (docs/VFD_INTEGRATION.md §1.6, §3.4, built 2026-11-24):
//   - it pays the last signature's hold, and never an officer's, from Argon's
//     VFD pool, after VFD confirms the member's account;
//   - VFD's 00 to the send is the payment; anything else, a refusal and a
//     timeout included, is a pending check with the money held;
//   - asked about at 2 minutes, every 10 minutes for 24 hours, then hourly;
//   - 00 with transactionStatus 00 is paid; 108 twice, both 30 minutes or
//     more after the send, releases; anything else holds, and a person is
//     told; it never sends twice; every reply VFD gives is kept;
//   - a deposit no notification brought is found in the account's history,
//     and confirmed with VFD like any other;
//   - (0040) a member's bank details are checked with VFD: its answer is
//     recorded, found or not, and silence records nothing; whether the name
//     resembles the register is the database's; a name that is not the one
//     recorded when the details were checked is never paid.
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
	walletLogs = &logs
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, err := vfd.New(vfd.Config{BaseURL: url, AccessToken: vfdToken, Timeout: 300 * time.Millisecond, Log: log})
	fixture(t, err, "the VFD client")
	pool := db
	brk, fake := os.LookupEnv("ARGON_TEST_WALLET_FAKE")
	if !fake {
		pool = walletPool(t)
	}
	cfg := wallet.Config{DB: pool, VFD: client, Log: log, FirstAsk: time.Millisecond, AskEvery: time.Millisecond, LateAskEvery: time.Millisecond,
		LostAfter: 50 * time.Millisecond, SweepEvery: time.Hour, Society: society}
	if walletCfg != nil {
		walletCfg(&cfg)
		walletCfg = nil
	}
	var w wallet.Worker
	if fake {
		w, err = newRefWallet(cfg, brk)
	} else {
		w, err = wallet.New(cfg)
	}
	fixture(t, err, "the wallet worker")
	return w
}

// walletCfg: a test's change to the next worker's configuration.
var walletCfg func(*wallet.Config)

// walletLogs: the next worker's log, for the tests that read it.
var walletLogs *bytes.Buffer

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

var okModes = map[string]string{"/transfer/recipient": "enquiry_ok", "/transfer": "transfer_ok", "/transactions": "requery_ok", "/account/enquiry": "pool_ok"}

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

// §1.6.2: VFD's 00 to the send is the payment, posted at once, from the pool.
func TestWalletWorker01_VFDs00ToTheSendIsThePaymentFromThePool(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	// The requery answers 91: only the send's 00 can pay this transfer.
	f := newSwitchVFD(t, modes(map[string]string{"/transactions": "requery_91"}))
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	p := paymentOf(t, hold)
	if p.transfer == "" || p.sent != "accepted" {
		t.Fatalf("NOT SENT — %+v; want a transfer VFD accepted", p)
	}
	for _, c := range f.all() {
		if c.Path == "/transfer" && (vfdStr(c.Body, "reference") != vfdRef(hold) || vfdStr(c.Body, "toAccount") != w.memAcct || vfdStr(c.Body, "fromAccount") != testPoolAcct ||
			vfdStr(c.Body, "fromSavingsId") != "P-ID" || vfdStr(c.Body, "amount") != "6000.00") {
			t.Fatalf("WRONG TRANSFER — %v; want %s, from the pool %s (its id P-ID) to %s, 6000.00", c.Body, vfdRef(hold), testPoolAcct, w.memAcct)
		}
	}
	if p.outcome != "paid" || !p.posted {
		t.Fatalf("NOT PAID — VFD answered 00 to the send: %+v; want paid and the payout posted at once (161 of 161 such sends were paid)", p)
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

// §1.6.3: a refused send, or one that never connected, is a pending check,
// never a release: in 3 of 32 refused sends VFD had already sent the money.
func TestWalletWorker04_ARefusedSendIsHeldNotReleased(t *testing.T) {
	for _, mode := range []string{"transfer_refused", "dead"} {
		w := newWalletWorld(t)
		hold := w.lastSignatureHold(t)
		url := deadAfterEnquiry(t) // the account confirmed, then no connection
		if mode == "transfer_refused" {
			url = newSwitchVFD(t, modes(map[string]string{"/transfer": "transfer_refused", "/transactions": "requery_91"})).srv.URL
		}
		wk := walletWorkerFor(t, w.c.s.id, url)
		tick(t, wk)
		time.Sleep(5 * time.Millisecond)
		tick(t, wk)
		if p := paymentOf(t, hold); p.outcome != "" || p.released {
			t.Fatalf("RELEASED ON A REFUSAL — VFD %s the send: %+v; want no outcome, the money still held", mode, p)
		}
	}
}

// deadAfterEnquiry: a VFD that confirms the account and names the pool, then
// closes the connection on the transfer (the request never reaches it).
func deadAfterEnquiry(t *testing.T) string {
	t.Helper()
	f := newFakeVFD(t, map[string]string{"/transfer/recipient": "enquiry_ok", "/account/enquiry": "pool_ok", "/transactions": "requery_91"})
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

// A send with no answer in time: a pending check, never sent again, and paid
// when VFD's requery says 00 with transactionStatus 00.
func TestWalletWorker05_ATimeoutIsAskedAboutNeverResentAndPaidOnZeroZero(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer": "slow", "/transactions": "requery_99"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	for i := 0; i < 2; i++ {
		time.Sleep(5 * time.Millisecond)
		tick(t, wk)
	}
	p := paymentOf(t, hold)
	if p.sent != "unknown" || p.outcome != "" || p.released {
		t.Fatalf("DECIDED ON A GUESS — no answer to the send, VFD answering 99 since: %+v; want unknown, no outcome, the hold still held", p)
	}
	f.set("/transactions", "requery_ok_payout")
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	if p = paymentOf(t, hold); p.outcome != "paid" || !p.posted {
		t.Fatalf("NOT PAID — VFD's requery said 00 with 00: %+v; want paid and posted", p)
	}
	if n := f.count("/transfer"); n != 1 {
		t.Fatalf("SENT %d TIMES — an automatic retry is how a payment goes out twice", n)
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

// §1.6.4: 108 releases only when seen twice in a row, both 30 minutes or more
// after the send. Moments after the send, never; once, never.
func TestWalletWorker07_OneOhEightTwicePastThirtyMinutesReleases(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer": "slow", "/transactions": "requery_108"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	for i := 0; i < 2; i++ {
		time.Sleep(5 * time.Millisecond)
		tick(t, wk)
	}
	if p := paymentOf(t, hold); p.outcome != "" || p.released {
		t.Fatalf("RELEASED EARLY — 108 moments after the send: %+v; want no outcome", p)
	}

	// The same, three hours after the send (written as the owner: the wallet
	// cannot date VFD's answers, P65).
	w2 := newWalletWorld(t)
	hold2 := w2.lastSignatureHold(t)
	tr, err := w2.transfer(xfer{hold: hold2})
	fixture(t, err, "the transfer")
	mustEvent(t, tr, "sent", "unknown", "", 0, "3 hours")
	f2 := newSwitchVFD(t, modes(map[string]string{"/transactions": "requery_108"}))
	wk2 := walletWorkerFor(t, w2.c.s.id, f2.srv.URL)
	tick(t, wk2)
	if p := paymentOf(t, hold2); p.outcome != "" || p.released {
		t.Fatalf("RELEASED ON ONE 108 — %+v; want no outcome until VFD says 108 twice in a row", p)
	}
	time.Sleep(5 * time.Millisecond)
	tick(t, wk2)
	if p := paymentOf(t, hold2); p.outcome != "failed" || !p.released {
		t.Fatalf("NOT RELEASED — 108 twice, three hours after the send: %+v; want failed, the hold released", p)
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
	// VFD confirms the reference, as money going OUT: no deposit (§3.2).
	out := vfdRefNew()
	outNote := arrived(t, out, w.socAcct)
	f.set("/transactions", "requery_ok_payout")
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if n, _, _ := inflowsOf(t, out); n != 0 {
		t.Fatalf("BELIEVED — VFD reports the reference as no INFLOW and %d deposit(s) were recorded", n)
	}
	if a := inflowAttempts(t, outNote); len(a) != 1 || !strings.HasPrefix(a[0], "not_confirmed: ") {
		t.Fatalf("WRONG — a reference VFD does not call INFLOW: the attempts read %v; want not_confirmed", a)
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

// §3.4: VFD stopped notifying for days, four times, and never re-sent. A
// deposit in the account's history that no notification brought is found,
// and confirmed with VFD like any other: never believed from the history.
func TestWalletWorker12_ADepositNoNotificationBroughtIsFoundAndConfirmed(t *testing.T) {
	w := inflowWorld(t)
	ref := "Argon Financials Limited-" + vfdRefNew()
	f := newSwitchVFD(t, map[string]string{"/account/transactions": "history_inflow:" + w.socAcct + "|" + ref, "/transactions": "requery_108"})
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	var runOutcome string
	fixture(t, db.QueryRow(bg(), `SELECT coalesce((SELECT outcome FROM vfd_reconciliation_runs WHERE account_number = $1), 'no sweep recorded')`, w.socAcct).Scan(&runOutcome), "the sweep")
	if runOutcome != "missing_found" {
		t.Fatalf("UNSAID — the sweep reads %q; want missing_found", runOutcome)
	}
	if n, _, _ := inflowsOf(t, ref); n != 0 {
		t.Fatalf("BELIEVED — a deposit in the history was credited while VFD's requery says 108 (%d)", n)
	}
	f.set("/transactions", "requery_inflow:"+w.socAcct)
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	if n, kobo, acct := inflowsOf(t, ref); n != 1 || kobo != 600000 || acct != w.socAcct {
		t.Fatalf("NOT FOUND — the deposit only the history held: %d inflow(s) of %d to %s; want one, VFD's ₦6,000 to the society's account", n, kobo, acct)
	}
	var source string
	fixture(t, db.QueryRow(bg(), `SELECT source FROM vfd_notifications WHERE reference = $1`, ref).Scan(&source), "the notification")
	if source != "history" {
		t.Fatalf("UNSAID — the deposit's notification reads source %q; want history", source)
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

// §1.6.4: anything other than 00/00 or 108 (91, 25, 99) holds, and a person
// is told: the ₦50 at 91 on the other platform since 20 October was held
// rightly and told to nobody.
func TestWalletWorker14_AnythingElseHoldsAndTellsAPerson(t *testing.T) {
	for _, mode := range []string{"requery_91", "requery_25", "requery_99"} {
		w := newWalletWorld(t)
		hold := w.lastSignatureHold(t)
		tr, err := w.transfer(xfer{hold: hold})
		fixture(t, err, "the transfer")
		mustEvent(t, tr, "sent", "refused", "61", 0, "3 hours")
		f := newSwitchVFD(t, modes(map[string]string{"/transactions": mode}))
		r := tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
		if p := paymentOf(t, hold); p.outcome != "" || p.released {
			t.Fatalf("DECIDED — VFD answered %s: %+v; want held, no outcome (VFD may have sent it)", mode, p)
		}
		if r.NeedsVFD != 1 || !strings.Contains(walletLogs.String(), "needs VFD") {
			t.Fatalf("NOBODY TOLD — VFD answered %s: needs-VFD %d, log %q; want it counted and an error logged", mode, r.NeedsVFD, walletLogs.String())
		}
		var n int
		fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM wallet_exceptions WHERE kind = 'transfer_needs_vfd' AND subject_id = $1`, tr).Scan(&n), "the exceptions")
		if n != 1 {
			t.Fatalf("UNSEEN — VFD answered %s and wallet_exceptions shows %d transfer_needs_vfd for it; want one", mode, n)
		}
	}
}

// §1.6.4: asked first at FirstAsk, then every AskEvery, not on every tick.
func TestWalletWorker15_ATransferIsAskedAboutOnTheSchedule(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer": "slow", "/transactions": "requery_99"}))
	walletCfg = func(c *wallet.Config) { c.FirstAsk, c.AskEvery = 400*time.Millisecond, 400*time.Millisecond }
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	tick(t, wk)
	if n := f.count("/transactions"); n != 0 {
		t.Fatalf("ASKED TOO SOON — %d requeries straight after the send; want none before the first ask is due", n)
	}
	time.Sleep(450 * time.Millisecond)
	tick(t, wk)
	tick(t, wk)
	if n := f.count("/transactions"); n != 1 {
		t.Fatalf("WRONG PACE — %d requeries once the first was due; want exactly one, the next not before its interval", n)
	}
	time.Sleep(450 * time.Millisecond)
	tick(t, wk)
	if n := f.count("/transactions"); n != 2 {
		t.Fatalf("NOT ASKED AGAIN — %d requeries an interval later; want two", n)
	}
	_ = hold
}

// §1.6.1: every reply VFD gives a send or a requery is kept, as received.
func TestWalletWorker16_EveryReplyIsKept(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, modes(map[string]string{"/transfer": "transfer_refused", "/transactions": "requery_91"}))
	wk := walletWorkerFor(t, w.c.s.id, f.srv.URL)
	tick(t, wk)
	time.Sleep(5 * time.Millisecond)
	tick(t, wk)
	var sentReply, askReply string
	var sentStatus int
	fixture(t, db.QueryRow(bg(), `SELECT coalesce((SELECT reply FROM vfd_transfer_events e WHERE e.transfer_id = t.id AND e.kind = 'sent'), ''),
		coalesce((SELECT http_status FROM vfd_transfer_events e WHERE e.transfer_id = t.id AND e.kind = 'sent'), 0),
		coalesce((SELECT reply FROM vfd_transfer_events e WHERE e.transfer_id = t.id AND e.kind = 'requeried' ORDER BY at LIMIT 1), '')
		FROM vfd_transfers t WHERE t.hold_id = $1`, hold).Scan(&sentReply, &sentStatus, &askReply), "the replies")
	if sentStatus != 200 || !strings.Contains(sentReply, `"status":"61"`) || !strings.Contains(askReply, `"transactionStatus":"91"`) {
		t.Fatalf("NOT KEPT — the send's reply %d %q, the requery's %q; want both as VFD gave them", sentStatus, sentReply, askReply)
	}
}

// The sweep reads every page of the history, not the first alone.
func TestWalletWorker17_TheSweepReadsEveryPage(t *testing.T) {
	w := inflowWorld(t)
	f := newSwitchVFD(t, map[string]string{"/account/transactions": "history_two_pages", "/transactions": "requery_108"})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	pages := map[string]bool{}
	for _, c := range f.all() {
		if c.Path == "/account/transactions" {
			for _, kv := range strings.Split(c.Query, "&") {
				if strings.HasPrefix(kv, "page=") {
					pages[kv] = true
				}
			}
		}
	}
	if !pages["page=0"] || !pages["page=1"] || !pages["page=2"] {
		t.Fatalf("A PAGE UNREAD — pages asked %v; want 0, 1 and 2 (the empty page that ends it)", pages)
	}
}

// A sweep that could not read the history says so: never "nothing missing".
func TestWalletWorker18_ASweepThatCannotReadSaysSo(t *testing.T) {
	w := inflowWorld(t)
	f := newSwitchVFD(t, map[string]string{"/account/transactions": "history_down"})
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	var outcome, detail string
	err := db.QueryRow(bg(), `SELECT outcome, detail FROM vfd_reconciliation_runs WHERE account_number = $1`, w.socAcct).Scan(&outcome, &detail)
	if err != nil || outcome != "could_not_run" || !strings.Contains(detail, "history") {
		t.Fatalf("SAID NOTHING WAS MISSED — %s: %q (%v); want could_not_run, saying VFD did not give the history", outcome, detail, err)
	}
}

// bankCheckOf: what is recorded of VFD's answer on a set of bank details.
func bankCheckOf(t *testing.T, bankAccount string) (checked, found bool, name, check string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT k.id IS NOT NULL, coalesce(k.found, false), coalesce(k.vfd_name, ''), coalesce(k.name_check, '')
		FROM member_bank_accounts b LEFT JOIN bank_name_checks k ON k.bank_account_id = b.id WHERE b.id = $1`, bankAccount).
		Scan(&checked, &found, &name, &check), "VFD's answer on the details")
	return
}

// 0040: THE NAME CHECK IS THE GUARD. VFD's answer is recorded as it came,
// found or not found; no answer records nothing, and is asked again.
func TestWalletWorker19_BankDetailsAreCheckedWithVFD(t *testing.T) {
	w := newWalletWorld(t)
	for _, c := range []struct{ mode, what string }{{"enquiry_ok", "found"}, {"enquiry_none", "not found"}, {"enquiry_dead", "no answer"}} {
		var id string
		if c.mode == "enquiry_dead" {
			id = bankUnchecked(t, w.bMem, "0123456789")
			tick(t, walletWorkerFor(t, w.c.s.id, deadVFD(t)))
		} else {
			id = bankUnchecked(t, w.bMem, "0123456789")
			f := newSwitchVFD(t, modes(map[string]string{"/transfer/recipient": c.mode}))
			tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
		}
		checked, found, name, check := bankCheckOf(t, id)
		switch c.mode {
		case "enquiry_ok":
			if !checked || !found || name != "ADEBAYO FOLAKE" || check != "differs" {
				t.Fatalf("NOT RECORDED AS VFD SAID — %s: checked %v, found %v, name %q, check %q; want VFD's name recorded, and the database judging it against the register (\"Member\": differs)", c.what, checked, found, name, check)
			}
		case "enquiry_none":
			if !checked || found {
				t.Fatalf("NOT RECORDED AS VFD SAID — %s: checked %v, found %v; want VFD's no-such-account recorded (the member sets them again)", c.what, checked, found)
			}
		default:
			if checked {
				t.Fatalf("SILENCE RECORDED — %s: checked %v, found %v; VFD did not answer, nothing is recorded, and it is asked again", c.what, checked, found)
			}
		}
	}
}

// 0040: the worker asks again at the send; a name that is not the one
// recorded with the details is never paid: a person looks.
func TestWalletWorker20_ANameNotTheOneRecordedIsNeverPaid(t *testing.T) {
	w := newWalletWorld(t)
	id := bankUnchecked(t, w.bMem, w.memAcct)
	bankAnswered(t, id, "OKORO JAMES") // recorded then; the fake VFD now says ADEBAYO FOLAKE
	bankConfirmed(t, id)
	hold := w.lastSignatureHold(t)
	f := newSwitchVFD(t, okModes)
	tick(t, walletWorkerFor(t, w.c.s.id, f.srv.URL))
	if p := paymentOf(t, hold); p.transfer != "" || f.count("/transfer") != 0 {
		t.Fatalf("PAID ANOTHER NAME — the bank now names the account ADEBAYO FOLAKE, recorded as OKORO JAMES, and %+v, %d sends; it waits for a person", p, f.count("/transfer"))
	}
}

// deadVFD: a VFD that closes the connection on the name enquiry (no answer).
func deadVFD(t *testing.T) string {
	t.Helper()
	f := newFakeVFD(t, okModes)
	h := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/transfer/recipient" {
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
