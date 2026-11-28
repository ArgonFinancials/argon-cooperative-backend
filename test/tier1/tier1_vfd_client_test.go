//go:build tier1

// ACCEPTANCE TESTS — THE VFD CLIENT (stage 3, 2026-11-10). Re-implemented in
// Go here (LEDGER_CONTRACT §11), from docs/VFD_INTEGRATION.md: the running
// wallet platform's knowledge, none of its defects. Tested against a FAKE VFD
// inside the test: no network, no real token.
//
// What they pin:
//   - a timeout on every call; every answer read; a 2xx is not success;
//   - four outcomes kept apart: accepted, refused, not_reached, unknown; a
//     timeout, a 5xx and an unreadable answer are UNKNOWN (VFD may have
//     acted), never refused and never not_reached;
//   - a transfer's state by requery (docs/VFD_INTEGRATION.md §1.6, from 193
//     transfers looked up at VFD, 2026-11-24): succeeded ONLY on 00 with
//     transactionStatus 00; not_found ONLY on 108; anything else VFD says
//     (91, 25, 99, a word, another code) is pending, which the caller holds
//     and shows a person; an answer that cannot be read is unknown;
//   - every send reply and every requery reply kept as received;
//   - the pool account, from VFD's enquiry; an account's history, page by
//     page (the sweeper's recovery path for missed notifications);
//   - amounts exact: kobo to VFD's naira string and back with no float;
//   - the caller's reference, never one made here; none, nothing sent;
//   - intra-bank to VFD's own code, signed hex SHA-512(from + to);
//   - an account VFD numbered or no account: one request, never a fallback;
//   - the token, a BVN and a date of birth in no log line.
package tier1

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argon/backend/internal/vfd"
	"github.com/argon/backend/internal/vfdsign"
)

const (
	poolAcct     = "1000000001"
	vfdToken     = "test-vfd-access-token-not-a-secret-91c2"
	vfdBVN       = "22211133344"
	vfdDOBText   = "07-Mar-1984"
	societyAcct  = "1001234567"
	memberAcct   = "1007654321"
	otherBankAcc = "0123456789"
)

var vfdDOB = time.Date(1984, 3, 7, 0, 0, 0, 0, time.UTC)

type vfdCall struct {
	Method, Path, Query string
	Token               string
	Body                map[string]any
}

// fakeVFD answers each path by the test's mode.
type fakeVFD struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []vfdCall
	mode  map[string]string // path -> mode
}

func newFakeVFD(t *testing.T, modes map[string]string) *fakeVFD {
	t.Helper()
	f := &fakeVFD{mode: modes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.calls = append(f.calls, vfdCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("AccessToken"), body})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/account/transactions" {
			vfdHistoryMode(w, r, f.mode[r.URL.Path])
			return
		}
		vfdAnswerMode(w, f.mode[r.URL.Path])
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func vfdAnswerMode(w http.ResponseWriter, mode string) {
	// requery_inflow:<account>: money arriving, ₦6,000, into <account> (the wallet's inflow tests).
	if acct, ok := strings.CutPrefix(mode, "requery_inflow:"); ok {
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"TxnId":"TXN-IN","amount":"6000.00","accountNo":"` + acct + `","transactionStatus":"00","transactionDate":"2026-11-10 10:00:00","sessionId":"SESS-IN","transactionType":"INFLOW"}}`))
		return
	}
	switch mode {
	case "transfer_ok":
		w.Write([]byte(`{"status":"00","message":"Successful Transfer","data":{"txnId":"TXN-77","sessionId":"SESS-77","reference":"ignored"}}`))
	case "transfer_refused":
		w.Write([]byte(`{"status":"61","message":"Insufficient balance","data":{}}`))
	case "status500_says_00":
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"txnId":"TXN-500"}}`))
	case "slow":
		time.Sleep(1500 * time.Millisecond)
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"txnId":"TXN-SLOW"}}`))
	case "garbage":
		w.Write([]byte(`<html>502 Bad Gateway</html>`))
	case "requery_ok":
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"TxnId":"TXN-77","amount":"4.35","accountNo":"` + memberAcct + `","fromAccountNo":"` + societyAcct + `","transactionStatus":"00","transactionDate":"2026-11-10 09:01:02","sessionId":"SESS-77"}}`))
	case "requery_ok_payout": // the wallet's payout of ₦6,000, succeeded
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"TxnId":"TXN-77","amount":"6000.00","transactionStatus":"00","transactionDate":"2026-11-10 09:01:02"}}`))
	case "requery_99":
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"TxnId":"TXN-77","amount":"4.35","accountNo":"` + memberAcct + `","transactionStatus":"99"}}`))
	case "requery_108":
		w.Write([]byte(`{"status":"108","message":"No Transaction!"}`))
	case "requery_91": // created and sent; the receiving bank refused it (2 of 32, §1.4)
		w.Write([]byte(`{"status":"00","message":"Successful Transaction Retrieval","data":{"TxnId":"TXN-77","amount":"6000.00","accountNo":"` + memberAcct + `","transactionStatus":"91","sessionId":"SESS-91","bankTransactionId":"BT-91","transactionType":"OUTFLOW"}}`))
	case "requery_25": // not delivered, shown 25 on the day (§1.4)
		w.Write([]byte(`{"status":"00","message":"Successful Transaction Retrieval","data":{"TxnId":"TXN-77","amount":"6000.00","accountNo":"` + memberAcct + `","transactionStatus":"25","transactionType":"OUTFLOW"}}`))
	case "requery_word_success": // a word, not VFD's 00: never seen in production, never taken
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"TxnId":"TXN-77","amount":"6000.00","transactionStatus":"SUCCESSFUL","transactionType":"OUTFLOW"}}`))
	case "pool_ok":
		w.Write([]byte(`{"status":"00","message":"Account Details","data":{"accountNo":"` + poolAcct + `","accountId":"P-ID","client":"ARGON FINANCIALS LIMITED","clientId":"P-CLIENT","accountBalance":"1000000.00"}}`))
	case "pool_no_number":
		w.Write([]byte(`{"status":"00","message":"Account Details","data":{"accountId":"P-ID","client":"ARGON FINANCIALS LIMITED","clientId":"P-CLIENT"}}`))
	case "pool_refused":
		w.Write([]byte(`{"status":"99","message":"Unauthorized"}`))
	case "banks_ok": // as VFD gives it, with one entry nameless and one code twice
		w.Write([]byte(`{"status":"00","message":"Banks","data":{"bank":[{"id":"1","code":"000013","name":"GUARANTY TRUST BANK"},{"id":"2","code":"000016","name":"FIRST BANK OF NIGERIA"},{"id":"3","code":"000099","name":" "},{"id":"4","code":"000013","name":"GTBANK AGAIN"}]}}`))
	case "banks_refused": // a list, under a status that is not 00: never taken
		w.Write([]byte(`{"status":"99","message":"Unauthorized","data":{"bank":[{"id":"1","code":"000013","name":"GUARANTY TRUST BANK"}]}}`))
	case "requery_no_txn":
		w.Write([]byte(`{"status":"01","message":"There is no transaction with this reference"}`))
	case "enquiry_ok":
		w.Write([]byte(`{"status":"00","message":"Account found","data":{"name":"ADEBAYO FOLAKE","clientId":"C-123","bvn":"` + vfdBVN + `","account":{"number":"` + memberAcct + `","id":"A-456"},"status":"active","currency":"NGN","bank":"VFD"}}`))
	case "enquiry_none":
		w.Write([]byte(`{"status":"104","message":"Account not found"}`))
	case "open_ok":
		w.Write([]byte(`{"status":"00","message":"Successful Creation","data":{"accountNo":"` + memberAcct + `","clientId":"C-900"}}`))
	case "open_no_number":
		w.Write([]byte(`{"status":"00","message":"Successful Creation","data":{"clientId":"C-900"}}`))
	case "open_number_float":
		w.Write([]byte(`{"status":"00","message":"Successful Creation","data":{"accountNo":1007654321,"clientId":"C-900"}}`))
	case "open_929":
		w.Write([]byte(`{"status":"929","message":"Client Account Exists","data":{}}`))
	case "open_another_ok":
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"accountNo":"1009990001","clientId":"C-900"}}`))
	case "":
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status":"404","message":"no such path in the fake"}`))
	}
}

func (f *fakeVFD) all() []vfdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]vfdCall(nil), f.calls...)
}

// vfdClientFor: the client under test: the real one, or under
// ARGON_TEST_VFD_FAKE the reference (vfdfake_client_test.go) with its break.
func vfdClientFor(t *testing.T, url string, logs *bytes.Buffer) vfd.Client {
	t.Helper()
	cfg := vfd.Config{BaseURL: url, AccessToken: vfdToken, Timeout: 500 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	c, err := newVFDClient(cfg)
	if err != nil {
		t.Fatalf("NOT BUILT — the VFD client refused a complete configuration: %v", err)
	}
	return c
}

func newVFDClient(cfg vfd.Config) (vfd.Client, error) {
	if brk, ok := os.LookupEnv("ARGON_TEST_VFD_FAKE"); ok {
		return newRefVFD(cfg, brk)
	}
	return vfd.New(cfg)
}

var (
	societyParty = vfd.Party{AccountNo: societyAcct, Bank: vfd.IntraBank, Name: "ARGON TEST SOCIETY", ClientID: "C-SOC", AccountID: "A-SOC"}
	memberParty  = vfd.Party{AccountNo: memberAcct, Bank: vfd.IntraBank, Name: "ADEBAYO FOLAKE", ClientID: "C-123", AccountID: "A-456"}
	outsideParty = vfd.Party{AccountNo: otherBankAcc, Bank: "000013", Name: "ADEBAYO FOLAKE", AccountID: "SESS-OUT", BVN: vfdBVN}
)

func vfdATransfer(to vfd.Party) vfd.Transfer {
	return vfd.Transfer{Reference: "Argon-HOLD-0b1f2e3d4c5b6a79", From: societyParty, To: to, AmountKobo: 435025, Remark: "withdrawal"}
}

func vfdSig(from, to string) string {
	s := sha512.Sum512([]byte(from + to))
	return hex.EncodeToString(s[:])
}

func vfdStr(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func vfdOnlyCall(t *testing.T, f *fakeVFD, path string) vfdCall {
	t.Helper()
	calls := f.all()
	if len(calls) != 1 {
		t.Fatalf("RETRIED OR FELL BACK — VFD received %d requests (%v); one call, never a second attempt", len(calls), vfdPaths(calls))
	}
	if calls[0].Path != path {
		t.Fatalf("WRONG ENDPOINT — %s %s; want %s", calls[0].Method, calls[0].Path, path)
	}
	if calls[0].Token != vfdToken {
		t.Fatalf("NO TOKEN — the AccessToken header is %q", calls[0].Token)
	}
	return calls[0]
}

func vfdPaths(cs []vfdCall) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func vfdDoTransfer(t *testing.T, c vfd.Client, tr vfd.Transfer) vfd.Sent {
	t.Helper()
	got, err := c.Transfer(context.Background(), tr)
	if err != nil {
		t.Fatalf("WRONG SHAPE — a transfer answered an error (%v); its outcome belongs in Sent", err)
	}
	return got
}

// ---- transfers ---------------------------------------------------------------

func TestVfdClient01_AnIntraBankTransferIsSentAsVFDExpectsAndAccepted(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "transfer_ok"})
	var logs bytes.Buffer
	got := vfdDoTransfer(t, vfdClientFor(t, f.srv.URL, &logs), vfdATransfer(memberParty))
	if got.Outcome != vfd.Accepted || got.TxnID != "TXN-77" || got.SessionID != "SESS-77" || got.Code != "00" {
		t.Fatalf("WRONG — %+v; want accepted, txn TXN-77, session SESS-77, code 00", got)
	}
	b := vfdOnlyCall(t, f, "/transfer").Body
	want := map[string]string{
		"fromAccount": societyAcct, "fromClientId": "C-SOC", "fromClient": "ARGON TEST SOCIETY", "fromSavingsId": "A-SOC",
		"toAccount": memberAcct, "toBank": vfd.IntraBank, "toClient": "ADEBAYO FOLAKE", "toClientId": "C-123", "toSavingsId": "A-456",
		"transferType": "intra", "signature": vfdSig(societyAcct, memberAcct),
		"amount": "4350.25", "remark": "withdrawal", "reference": "Argon-HOLD-0b1f2e3d4c5b6a79",
	}
	for k, v := range want {
		if vfdStr(b, k) != v {
			t.Fatalf("WRONG REQUEST — %s is %q; want %q (VFD-to-VFD is intra; the signature is hex SHA-512 of from+to; the amount is exact naira; the reference is the caller's)", k, vfdStr(b, k), v)
		}
	}
}

func TestVfdClient02_AnInterBankTransferCarriesTheSessionAndBVN(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "transfer_ok"})
	var logs bytes.Buffer
	vfdDoTransfer(t, vfdClientFor(t, f.srv.URL, &logs), vfdATransfer(outsideParty))
	b := vfdOnlyCall(t, f, "/transfer").Body
	for k, v := range map[string]string{"transferType": "inter", "toBank": "000013", "toAccount": otherBankAcc, "toSession": "SESS-OUT", "toBvn": vfdBVN, "toClient": "ADEBAYO FOLAKE", "signature": vfdSig(societyAcct, otherBankAcc)} {
		if vfdStr(b, k) != v {
			t.Fatalf("WRONG REQUEST — inter-bank %s is %q; want %q", k, vfdStr(b, k), v)
		}
	}
}

// A 2xx is not success: VFD's status is read.
func TestVfdClient03_A200ThatRefusesIsARefusal(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "transfer_refused"})
	var logs bytes.Buffer
	got := vfdDoTransfer(t, vfdClientFor(t, f.srv.URL, &logs), vfdATransfer(memberParty))
	if got.Outcome != vfd.Refused || got.Code != "61" || got.TxnID != "" {
		t.Fatalf("TAKEN AS SENT — a 200 whose status is 61: %+v; want refused, code 61, no txn", got)
	}
}

// A 5xx is VFD's own failure: it may have acted. Whatever its body says.
func TestVfdClient04_A5xxIsUnknownWhateverItsBodySays(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "status500_says_00"})
	var logs bytes.Buffer
	got := vfdDoTransfer(t, vfdClientFor(t, f.srv.URL, &logs), vfdATransfer(memberParty))
	if got.Outcome != vfd.Unknown {
		t.Fatalf("WRONG — a 500 with a body saying 00: %+v; want unknown (never accepted, never refused)", got)
	}
}

// No answer in time: unknown, and promptly. Never refused (the caller would
// free money that may be leaving), never not_reached (it may have arrived).
func TestVfdClient05_NoAnswerInTimeIsUnknownAndPrompt(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "slow"})
	var logs bytes.Buffer
	c := vfdClientFor(t, f.srv.URL, &logs)
	start := time.Now()
	got := vfdDoTransfer(t, c, vfdATransfer(memberParty))
	if took := time.Since(start); took > 1200*time.Millisecond {
		t.Fatalf("NO TIMEOUT — the call took %v against a 500ms timeout", took)
	}
	if got.Outcome != vfd.Unknown {
		t.Fatalf("WRONG CLASS — no answer in time: %+v; want unknown", got)
	}
}

func TestVfdClient06_NoConnectionIsNotReached(t *testing.T) {
	var logs bytes.Buffer
	got := vfdDoTransfer(t, vfdClientFor(t, strings.TrimSuffix(deadURL(t), "/api/sms/send"), &logs), vfdATransfer(memberParty))
	if got.Outcome != vfd.NotReached {
		t.Fatalf("WRONG CLASS — no connection: %+v; want not_reached", got)
	}
}

func TestVfdClient07_AnUnreadable200IsUnknown(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "garbage"})
	var logs bytes.Buffer
	got := vfdDoTransfer(t, vfdClientFor(t, f.srv.URL, &logs), vfdATransfer(memberParty))
	if got.Outcome != vfd.Unknown {
		t.Fatalf("WRONG — an unreadable 200: %+v; want unknown", got)
	}
}

// The reference is the caller's, derived from what authorised the transfer.
// None, and nothing is sent: a reference made here would make a retry a
// second payment.
func TestVfdClient08_NoReferenceNothingSent(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "transfer_ok"})
	var logs bytes.Buffer
	tr := vfdATransfer(memberParty)
	tr.Reference = "  "
	got, err := vfdClientFor(t, f.srv.URL, &logs).Transfer(context.Background(), tr)
	if err == nil || got.Outcome == vfd.Accepted {
		t.Fatalf("SENT WITHOUT A REFERENCE — %+v, err %v; want an error and no request", got, err)
	}
	if n := len(f.all()); n != 0 {
		t.Fatalf("SENT WITHOUT A REFERENCE — VFD received %d requests", n)
	}
	f2 := newFakeVFD(t, map[string]string{"/transfer": "transfer_ok"})
	tr = vfdATransfer(memberParty)
	tr.AmountKobo = 0
	if _, err := vfdClientFor(t, f2.srv.URL, &logs).Transfer(context.Background(), tr); err == nil || len(f2.all()) != 0 {
		t.Fatalf("SENT NOTHING AS SOMETHING — a transfer of 0 kobo went to VFD")
	}
}

// ---- requery -----------------------------------------------------------------

func vfdRequery(t *testing.T, mode string) (vfd.Requeried, *fakeVFD) {
	t.Helper()
	f := newFakeVFD(t, map[string]string{"/transactions": mode})
	var logs bytes.Buffer
	got, err := vfdClientFor(t, f.srv.URL, &logs).Requery(context.Background(), "Argon-HOLD-0b1f2e3d4c5b6a79")
	if err != nil {
		t.Fatalf("WRONG SHAPE — a requery answered an error (%v); its state belongs in Requeried", err)
	}
	c := vfdOnlyCall(t, f, "/transactions")
	if c.Method != http.MethodGet || c.Query != "reference=Argon-HOLD-0b1f2e3d4c5b6a79" {
		t.Fatalf("WRONG REQUEST — %s ?%s; want GET ?reference=<the reference>", c.Method, c.Query)
	}
	return got, f
}

func TestVfdClient09_ASucceededTransferWithItsExactAmount(t *testing.T) {
	got, _ := vfdRequery(t, "requery_ok")
	if got.State != vfd.Succeeded || got.AmountKobo != 435 || got.TxnID != "TXN-77" || got.ToAcct != memberAcct || got.FromAcct != societyAcct || got.SessionID != "SESS-77" {
		t.Fatalf("WRONG — %+v; want succeeded, 435 kobo exactly (\"4.35\": a float makes it 434), txn TXN-77, to %s from %s", got, memberAcct, societyAcct)
	}
}

// 99 is not success: the one real 99 was money that never arrived (§0). Nor
// is 91 or 25: VFD created those transfers and sent them out. Nor a word.
func TestVfdClient10_OnlyZeroZeroIsSuccess(t *testing.T) {
	for mode, txn := range map[string]string{"requery_99": "99", "requery_91": "91", "requery_25": "25", "requery_word_success": "SUCCESSFUL"} {
		got, _ := vfdRequery(t, mode)
		if got.State != vfd.Pending || got.TxnStatus != txn {
			t.Fatalf("TAKEN AS PAID — transactionStatus %s: %+v; want pending with %s kept (only 00 with 00 is paid; a person sees the rest)", txn, got, txn)
		}
	}
}

// Only 108 has proven safe (29 of 29). Any other "not found"-sounding answer
// is something VFD said, held for a person, never a release.
func TestVfdClient11_OnlyOneOhEightIsNotFound(t *testing.T) {
	if got, _ := vfdRequery(t, "requery_108"); got.State != vfd.NotFound || got.Code != "108" {
		t.Fatalf("WRONG — 108: %+v; want not_found", got)
	}
	if got, _ := vfdRequery(t, "requery_no_txn"); got.State != vfd.Pending || got.Code != "01" {
		t.Fatalf("RELEASABLE — status 01 \"no transaction\": %+v; want pending (only 108 is proven safe to release on)", got)
	}
}

// Not knowing is never "not found": not found, after the window, releases a
// hold, and a hold released while the money left pays twice.
func TestVfdClient12_ARequeryThatCannotBeReadIsUnknownNeverNotFound(t *testing.T) {
	for _, mode := range []string{"garbage", "status500_says_00", "slow"} {
		got, _ := vfdRequery(t, mode)
		if got.State != vfd.StateUnknown {
			t.Fatalf("WRONG — a requery answered %s: %+v; want unknown", mode, got)
		}
	}
}

// ---- accounts ----------------------------------------------------------------

func vfdOpen(t *testing.T, mode string) (vfd.Opened, *fakeVFD, *bytes.Buffer) {
	t.Helper()
	f := newFakeVFD(t, map[string]string{"/client/tier/individual": mode, "/client/create": "open_ok"})
	var logs bytes.Buffer
	got, err := vfdClientFor(t, f.srv.URL, &logs).OpenAccount(context.Background(), vfd.Person{Surname: "Adebayo", OtherNames: "Folake Ruth", BVN: vfdBVN, DOB: vfdDOB, Phone: "08031234567"})
	if err != nil {
		t.Fatalf("WRONG SHAPE — opening an account answered an error (%v)", err)
	}
	return got, f, &logs
}

func TestVfdClient13_AnAccountOpenedIsTheNumberVFDGave(t *testing.T) {
	got, f, _ := vfdOpen(t, "open_ok")
	if got.Outcome != vfd.Accepted || got.AccountNo != memberAcct || got.ClientID != "C-900" {
		t.Fatalf("WRONG — %+v; want accepted, %s, client C-900", got, memberAcct)
	}
	b := vfdOnlyCall(t, f, "/client/tier/individual").Body
	for k, v := range map[string]string{"lastname": "Adebayo", "firstname": "Folake Ruth", "bvn": vfdBVN, "dob": vfdDOBText, "phone": "08031234567"} {
		if vfdStr(b, k) != v {
			t.Fatalf("WRONG REQUEST — %s is %q; want %q (surname is lastname: the wallet sent them swapped for months)", k, vfdStr(b, k), v)
		}
	}
}

// Never invent an account number.
func TestVfdClient14_NoNumberFromVFDNoAccount(t *testing.T) {
	for _, mode := range []string{"open_no_number", "open_number_float"} {
		got, _, _ := vfdOpen(t, mode)
		if got.Outcome == vfd.Accepted || got.AccountNo != "" {
			t.Fatalf("INVENTED — VFD answered %s and the client reports %+v; want no account (not accepted, no number)", mode, got)
		}
	}
}

// An ambiguous answer is unknown, and NOTHING ELSE IS TRIED: a second
// endpoint after an answer that may have opened one opens a second account.
func TestVfdClient15_AnAmbiguousOpeningIsUnknownAndNotRetried(t *testing.T) {
	for _, mode := range []string{"status500_says_00", "garbage"} {
		got, f, _ := vfdOpen(t, mode)
		if got.Outcome != vfd.Unknown || got.AccountNo != "" {
			t.Fatalf("WRONG — opening answered %s: %+v; want unknown, no number", mode, got)
		}
		vfdOnlyCall(t, f, "/client/tier/individual")
	}
}

func TestVfdClient16_AnExistingClientIsExistsAndOpenAnotherNamesTheAccount(t *testing.T) {
	got, _, _ := vfdOpen(t, "open_929")
	if got.Outcome != vfd.Exists || got.AccountNo != "" || got.Code != "929" {
		t.Fatalf("WRONG — 929: %+v; want exists, code 929, no number", got)
	}
	f := newFakeVFD(t, map[string]string{"/client/individual": "open_another_ok"})
	var logs bytes.Buffer
	o, err := vfdClientFor(t, f.srv.URL, &logs).OpenAnother(context.Background(), memberAcct)
	if err != nil || o.Outcome != vfd.Accepted || o.AccountNo != "1009990001" {
		t.Fatalf("WRONG — another account: %+v, %v; want accepted, 1009990001", o, err)
	}
	if b := vfdOnlyCall(t, f, "/client/individual").Body; vfdStr(b, "previousAccountNo") != memberAcct {
		t.Fatalf("WRONG REQUEST — previousAccountNo %q", vfdStr(b, "previousAccountNo"))
	}
}

func TestVfdClient17_NameEnquiryFindsTheParty(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer/recipient": "enquiry_ok"})
	var logs bytes.Buffer
	got, err := vfdClientFor(t, f.srv.URL, &logs).NameEnquiry(context.Background(), memberAcct, vfd.IntraBank)
	want := vfd.Party{AccountNo: memberAcct, Bank: vfd.IntraBank, Name: "ADEBAYO FOLAKE", ClientID: "C-123", AccountID: "A-456", BVN: vfdBVN}
	if err != nil || got.Outcome != vfd.Accepted || got.Party != want {
		t.Fatalf("WRONG — %+v, %v; want accepted, %+v", got, err, want)
	}
	c := vfdOnlyCall(t, f, "/transfer/recipient")
	if !strings.Contains(c.Query, "transfer_type=intra") || !strings.Contains(c.Query, "accountNo="+memberAcct) || !strings.Contains(c.Query, "bank="+vfd.IntraBank) {
		t.Fatalf("WRONG REQUEST — ?%s; VFD's own code is asked as intra", c.Query)
	}
	f2 := newFakeVFD(t, map[string]string{"/transfer/recipient": "enquiry_none"})
	got, err = vfdClientFor(t, f2.srv.URL, &logs).NameEnquiry(context.Background(), memberAcct, vfd.IntraBank)
	if err != nil || got.Outcome != vfd.Refused || got.Party.Name != "" {
		t.Fatalf("WRONG — no such account: %+v, %v; want refused, no party", got, err)
	}
}

// ---- what is never done --------------------------------------------------------

// An account's history (§4): the sweeper's way to recover what VFD never
// notified. Asked with the dates space-encoded as %20 and transactionType
// wallet; rows read exactly; page by page until a page is empty.
func TestVfdClient18_AnAccountsHistoryIsReadPageByPage(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/account/transactions": "history_two_pages"})
	var logs bytes.Buffer
	c := vfdClientFor(t, f.srv.URL, &logs)
	from := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	var rows []vfd.HistoryRow
	for page := 0; page < 5; page++ {
		h, err := c.History(context.Background(), societyAcct, from, from.Add(2*time.Hour), page)
		if err != nil || h.Outcome != vfd.Accepted {
			t.Fatalf("NOT READ — page %d: %+v, %v", page, h, err)
		}
		if len(h.Rows) == 0 {
			break
		}
		rows = append(rows, h.Rows...)
	}
	if len(rows) != 3 {
		t.Fatalf("WRONG ROWS — %d rows over the pages; want 3 (two, then one, then an empty page)", len(rows))
	}
	in := rows[0]
	if in.Type != "INFLOW" || in.TxnID != "Argon Financials Limited-00000000000000001" || in.AmountKobo != 600050 || in.ToAcct != societyAcct || in.SessionID != "SESS-H1" {
		t.Fatalf("WRONG ROW — %+v; want the INFLOW of 6000.50 into the society's account, its reference and session", in)
	}
	q := f.all()[0].Query
	if !strings.Contains(q, "accountNo="+societyAcct) || !strings.Contains(q, "transactionType=wallet") || !strings.Contains(q, "page=0") ||
		!strings.Contains(q, "startDate=2026-11-24%2010:00:00") && !strings.Contains(q, "startDate=2026-11-24%2010%3A00%3A00") {
		t.Fatalf("WRONG QUESTION — %s; want accountNo, transactionType=wallet, page, and the dates in VFD's time (WAT) with the space as %%20", q)
	}
}

// Every reply VFD gives a send or a requery is kept as received: the
// document could not say which send codes led where because nothing was.
func TestVfdClient21_EveryReplyIsKeptAsReceived(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "transfer_refused"})
	var logs bytes.Buffer
	c := vfdClientFor(t, f.srv.URL, &logs)
	s := vfdDoTransfer(t, c, vfdATransfer(memberParty))
	if s.HTTPStatus != 200 || !strings.Contains(s.Reply, `"status":"61"`) {
		t.Fatalf("NOT KEPT — the send's reply: %d %q; want 200 and VFD's body as it came", s.HTTPStatus, s.Reply)
	}
	q, _ := vfdRequery(t, "requery_91")
	if q.HTTPStatus != 200 || !strings.Contains(q.Reply, `"transactionStatus":"91"`) {
		t.Fatalf("NOT KEPT — the requery's reply: %d %q", q.HTTPStatus, q.Reply)
	}
}

// The pool (§1.1): every transfer goes out from it. Its details are VFD's
// enquiry, checked, never assumed.
func TestVfdClient22_ThePoolIsVFDsEnquiryChecked(t *testing.T) {
	var logs bytes.Buffer
	f := newFakeVFD(t, map[string]string{"/account/enquiry": "pool_ok"})
	p, err := vfdClientFor(t, f.srv.URL, &logs).Pool(context.Background())
	if err != nil || p.Outcome != vfd.Accepted || p.Party.AccountNo != poolAcct || p.Party.AccountID != "P-ID" || p.Party.ClientID != "P-CLIENT" || p.Party.Name != "ARGON FINANCIALS LIMITED" {
		t.Fatalf("WRONG POOL — %+v, %v; want VFD's pool account, id, client and client id", p, err)
	}
	for _, mode := range []string{"pool_no_number", "pool_refused"} {
		f := newFakeVFD(t, map[string]string{"/account/enquiry": mode})
		if p, err := vfdClientFor(t, f.srv.URL, &logs).Pool(context.Background()); err != nil || p.Outcome == vfd.Accepted {
			t.Fatalf("TAKEN — the pool enquiry answered %s: %+v, %v; want not accepted (no transfer may go out from an account VFD did not name)", mode, p, err)
		}
	}
}

func TestVfdClient19_TheTokenBVNAndBirthDateAreNeverLogged(t *testing.T) {
	f := newFakeVFD(t, map[string]string{"/transfer": "garbage", "/client/tier/individual": "garbage", "/transfer/recipient": "enquiry_ok", "/transactions": "status500_says_00"})
	var logs bytes.Buffer
	c := vfdClientFor(t, f.srv.URL, &logs)
	ctx := context.Background()
	c.Transfer(ctx, vfdATransfer(outsideParty))
	c.OpenAccount(ctx, vfd.Person{Surname: "Adebayo", OtherNames: "Folake", BVN: vfdBVN, DOB: vfdDOB, Phone: "08031234567"})
	c.NameEnquiry(ctx, memberAcct, vfd.IntraBank)
	c.Requery(ctx, "Argon-HOLD-0b1f2e3d4c5b6a79")
	dead := vfdClientFor(t, strings.TrimSuffix(deadURL(t), "/api/sms/send"), &logs)
	dead.Transfer(ctx, vfdATransfer(outsideParty))
	l := logs.String()
	if l == "" {
		t.Fatalf("SILENT — nothing was logged across four calls and a dead connection")
	}
	for name, secret := range map[string]string{"the token": vfdToken, "the BVN": vfdBVN, "the birth date": vfdDOBText, "the birth date (ISO)": "1984-03-07"} {
		if strings.Contains(l, secret) {
			t.Fatalf("LOGGED — %s appears in the logs:\n%s", name, l)
		}
	}
}

func TestVfdClient20_NoTokenOrNoURLNoClient(t *testing.T) {
	for _, cfg := range []vfd.Config{{BaseURL: "https://vfd.example.com"}, {AccessToken: vfdToken}} {
		if c, err := newVFDClient(cfg); err == nil || c != nil {
			t.Fatalf("BUILT — a client with %+v; want a refusal (no default that works)", vfd.Config{BaseURL: cfg.BaseURL, AccessToken: strings.Repeat("*", len(cfg.AccessToken))})
		}
	}
}

// vfdHistoryMode: an account's history, by page (§4: 20 a page, newest first,
// an empty page at the end).
func vfdHistoryMode(w http.ResponseWriter, r *http.Request, mode string) {
	page := r.URL.Query().Get("page")
	switch {
	case mode == "history_two_pages" && page == "0":
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"content":[` +
			`{"transactionId":"Argon Financials Limited-00000000000000001","amount":"6000.50","fromAccountNo":"0123456789","toAccountNo":"` + societyAcct + `","fromBank":"000013","toBank":"090110","sessionId":"SESS-H1","time":"2026-11-24 10:30:00","transactionType":"INFLOW","transactionResponse":"00","walletName":"ARGON"},` +
			`{"transactionId":"POOL-1","amount":"6000.50","fromAccountNo":"` + societyAcct + `","toAccountNo":"` + poolAcct + `","sessionId":"","time":"2026-11-24 10:30:05","transactionType":"POOL TRANSFER","transactionResponse":"00","walletName":"ARGON"}]}}`))
	case mode == "history_two_pages" && page == "1":
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"content":[` +
			`{"transactionId":"Argon-out-1","amount":2500,"fromAccountNo":"` + poolAcct + `","toAccountNo":"` + memberAcct + `","sessionId":"SESS-O1","time":"2026-11-24 09:10:00","transactionType":"OUTFLOW","transactionResponse":"91","walletName":"ARGON"}]}}`))
	case strings.HasPrefix(mode, "history_inflow:"): // one INFLOW into <account> (the wallet's sweeper tests)
		parts := strings.SplitN(strings.TrimPrefix(mode, "history_inflow:"), "|", 2)
		if page == "0" {
			w.Write([]byte(`{"status":"00","message":"Successful","data":{"content":[{"transactionId":"` + parts[1] + `","amount":"6000.00","fromAccountNo":"0123456789","toAccountNo":"` + parts[0] + `","fromBank":"000013","toBank":"090110","sessionId":"SESS-IN","time":"2026-11-24 10:30:00","transactionType":"INFLOW","transactionResponse":"00","walletName":"ARGON"}]}}`))
			return
		}
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"content":[]}}`))
	case mode == "history_down":
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`<html>502</html>`))
	default:
		w.Write([]byte(`{"status":"00","message":"Successful","data":{"content":[]}}`))
	}
}

// ---- THROUGH THE RELAY (cmd/vfd-proxy, 2026-11-28) --------------------------------

var relayKey = []byte("relay-key-for-the-tests-only-0123456789abcdef")

// fakeRelay: the relay as the client meets it. Every call must be signed with
// relayKey and carry no token; then it answers as `mode` says. Its clock is
// `skew` seconds behind.
func fakeRelay(t *testing.T, mode string, skew int64) (*httptest.Server, *int) {
	t.Helper()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		body, _ := io.ReadAll(r.Body)
		w.Header().Set(vfdsign.HeaderProxyTime, strconv.FormatInt(time.Now().Unix()-skew, 10))
		ts, _ := strconv.ParseInt(r.Header.Get(vfdsign.HeaderTime), 10, 64)
		if r.Header.Get("AccessToken") != "" || !vfdsign.Verify(relayKey, r.Method, r.URL.EscapedPath(), r.URL.RawQuery, ts, body, r.Header.Get(vfdsign.HeaderSignature)) {
			w.Header().Set(vfdsign.HeaderProxy, "bad_signature")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"proxy":"argon-vfd-proxy","outcome":"bad_signature"}`))
			return
		}
		switch mode {
		case "refuse":
			w.Header().Set(vfdsign.HeaderProxy, "not_allowed")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"proxy":"argon-vfd-proxy","outcome":"not_allowed"}`))
		case "unauthorised":
			w.Header().Set(vfdsign.HeaderProxy, "stale")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"proxy":"argon-vfd-proxy","outcome":"stale"}`))
		case "timeout":
			w.Header().Set(vfdsign.HeaderProxy, "vfd_timeout")
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte(`{"proxy":"argon-vfd-proxy","outcome":"vfd_timeout"}`))
		default: // VFD's own answer, passed through
			w.Write([]byte(`{"status":"00","message":"Account found","data":{"name":"ADEBAYO FOLAKE","clientId":"C-123","account":{"number":"` + memberAcct + `","id":"A-456"}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func relayClient(t *testing.T, url string, logs *bytes.Buffer) vfd.Client {
	t.Helper()
	c, err := newVFDClient(vfd.Config{BaseURL: url, ProxyKey: relayKey, Timeout: 2 * time.Second,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatalf("NOT BUILT — a client through the relay, with its key and no token: %v", err)
	}
	return c
}

// THE RISKIEST PIECE (the reviewer, 2026-11-28): the relay's own answer is
// never VFD's. A relay 401 read as VFD's refusal would tell a member their
// bank details are wrong when nothing was ever asked.
func TestVfdClient23_ARelayRefusalIsNeverVFDsAnswer(t *testing.T) {
	for _, mode := range []string{"refuse", "unauthorised"} {
		srv, _ := fakeRelay(t, mode, 0)
		var logs bytes.Buffer
		c := relayClient(t, srv.URL, &logs)
		l, err := c.NameEnquiry(context.Background(), otherBankAcc, "000013")
		if err != nil || l.Outcome != vfd.NotReached {
			t.Fatalf("WRONG OUTCOME — %s: a name enquiry the relay refused reads %q (%v); want not_reached (nothing was asked of VFD: never \"no such account\")", mode, l.Outcome, err)
		}
		q, err := c.Requery(context.Background(), "COOP-0b1f2e3d4c5b6a79")
		if err != nil || q.State != vfd.StateUnknown {
			t.Fatalf("WRONG STATE — %s: a requery the relay refused reads %q (%v); want unknown (never not_found: that releases money)", mode, q.State, err)
		}
	}
}

// The relay gave up waiting for VFD: VFD may have acted. Unknown, never "not sent".
func TestVfdClient24_ARelayTimeoutMayHaveActed(t *testing.T) {
	srv, _ := fakeRelay(t, "timeout", 0)
	var logs bytes.Buffer
	s, err := relayClient(t, srv.URL, &logs).Transfer(context.Background(), vfdATransfer(memberParty))
	if err != nil || s.Outcome != vfd.Unknown {
		t.Fatalf("WRONG OUTCOME — a send the relay timed out on reads %q (%v); want unknown (VFD may have sent it: asked about, never resent)", s.Outcome, err)
	}
}

// Through the relay, every call is signed and no token is sent (this server
// holds none); a client with both a token and a key is not built.
func TestVfdClient25_ThroughTheRelayCallsAreSignedAndCarryNoToken(t *testing.T) {
	srv, calls := fakeRelay(t, "pass", 0)
	var logs bytes.Buffer
	l, err := relayClient(t, srv.URL, &logs).NameEnquiry(context.Background(), otherBankAcc, "000013")
	if err != nil || l.Outcome != vfd.Accepted || *calls != 1 {
		t.Fatalf("REFUSED — a signed name enquiry through the relay reads %q (%v, %d calls); want accepted, VFD's answer", l.Outcome, err, *calls)
	}
	if c, err := newVFDClient(vfd.Config{BaseURL: srv.URL, AccessToken: vfdToken, ProxyKey: relayKey}); err == nil || c != nil {
		t.Fatalf("BUILT — a client with both VFD's token and the relay's key; through the relay this server holds no token")
	}
}

// The relay's clock 30 seconds off: told now, long before signed calls fail at 60.
func TestVfdClient26_TheRelaysClockDriftIsTold(t *testing.T) {
	srv, _ := fakeRelay(t, "pass", 30)
	var logs bytes.Buffer
	relayClient(t, srv.URL, &logs).NameEnquiry(context.Background(), otherBankAcc, "000013")
	if !strings.Contains(logs.String(), "clock skew") {
		t.Fatalf("UNTOLD — the relay's clock is 30 seconds off and the log says nothing of it: %s", logs.String())
	}
}

// ---- VFD'S LIST OF BANKS (0046, 2026-11-28) ------------------------------------

// The list is VFD's only on its 00; an entry with no name, or a code seen
// already, is left out, never guessed.
func TestVfdClient27_TheBankListIsVFDsOnlyOnZeroZero(t *testing.T) {
	srv := newFakeVFD(t, map[string]string{"/bank": "banks_ok"})
	var logs bytes.Buffer
	l, err := vfdClientFor(t, srv.srv.URL, &logs).Banks(context.Background())
	if err != nil || l.Outcome != vfd.Accepted || len(l.Banks) != 2 || l.Banks[0] != (vfd.Bank{Code: "000013", Name: "GUARANTY TRUST BANK"}) || l.Skipped != 2 {
		t.Fatalf("WRONG LIST — %+v (%v); want accepted, the two whole banks as VFD named them, two entries left out", l, err)
	}
	srv2 := newFakeVFD(t, map[string]string{"/bank": "banks_refused"})
	l, err = vfdClientFor(t, srv2.srv.URL, &logs).Banks(context.Background())
	if err != nil || l.Outcome != vfd.Refused || len(l.Banks) != 0 {
		t.Fatalf("TAKEN — a list under status 99 reads %+v (%v); want refused, no banks", l, err)
	}
}
