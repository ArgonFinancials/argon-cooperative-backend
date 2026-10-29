//go:build tier1

// ACCEPTANCE TESTS — A NEW DEVICE GETS A CODE THROUGH TERMII (the Termii
// sender, piece 2, approved 2026-10-28): what the member is told in each of the
// four outcomes, what is recorded, and that our failure never counts against
// them. Driven through POST /auth/sign-in with the Termii sender pointed at a
// fake Termii server.
package tier1

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

// smsServer: the API with the Termii sender at `termiiURL`, the sign-in
// service real or the reference fake.
func smsServer(t *testing.T, termiiURL string) *httptest.Server {
	t.Helper()
	var logs bytes.Buffer
	sender, err := auth.NewTermii(auth.TermiiConfig{URL: termiiURL, APIKey: termiiKey, SenderID: termiiSender, Timeout: 500 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(&logs, nil))})
	fixture(t, err, "the Termii sender")
	deps := auth.Deps{SMS: sender, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon}
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		deps.DB = db
		h = newFakeAPIWith(&fakeAPI{db: db, brk: brk, deps: deps})
	} else {
		app, authp := rolePools(t, 0)
		deps.DB = authp
		svc, err := auth.New(deps)
		fixture(t, err, "the auth service")
		h, err = api.NewServer(api.Deps{DB: app, Auth: svc, CSRFKey: testCSRFKey})
		fixture(t, err, "the API")
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type smsSignIn struct {
	Outcome            string
	Challenge, Message *string
	Reason             *string
}

// newDeviceSignIn: the member's right PIN on a device the server does not know.
func newDeviceSignIn(t *testing.T, srv *httptest.Server, p signer) (smsSignIn, int) {
	t.Helper()
	r := postJSON(t, srv, "/auth/sign-in", "", map[string]string{"credential": p.number, "pin": signPIN})
	var out smsSignIn
	json.Unmarshal(r.body, &out)
	return out, r.status
}

type smsRecord struct {
	outcome         string
	messageID, code *string
}

func lastSend(t *testing.T, user string) (smsRecord, bool) {
	t.Helper()
	var s smsRecord
	err := db.QueryRow(bg(), `SELECT s.outcome, s.provider_message_id, s.provider_code FROM sms_sends s JOIN auth_codes c ON c.id = s.code_id
		WHERE c.user_id = $1 ORDER BY s.at DESC LIMIT 1`, user).Scan(&s.outcome, &s.messageID, &s.code)
	return s, err == nil
}

var codeIn = regexp.MustCompile(`\b[0-9]{6}\b`)

// The code Termii was handed (the fake received it), and whether it signs in.
func (f *fakeTermii) codeSent(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) == 0 {
		t.Fatalf("FIXTURE: Termii received nothing")
	}
	text, _ := f.received[len(f.received)-1]["sms"].(string)
	return codeIn.FindString(text)
}

func verifies(t *testing.T, srv *httptest.Server, challenge, code string) bool {
	t.Helper()
	r := postJSON(t, srv, "/auth/codes/verify", "", map[string]string{"challenge": challenge, "code": code})
	var out smsSignIn
	json.Unmarshal(r.body, &out)
	return r.status == http.StatusOK && out.Outcome == "signed_in"
}

func smsMember(t *testing.T) signer {
	return newSigner(t, newSociety(t), "A member on a new phone", "", false)
}

// Accepted: code_sent, the send recorded with Termii's id, and the code works.
func TestApiSms01_AnAcceptedCodeIsSentRecordedAndWorks(t *testing.T) {
	f := newFakeTermii(t, "ok")
	srv := smsServer(t, f.srv.URL)
	p := smsMember(t)
	out, st := newDeviceSignIn(t, srv, p)
	if st != http.StatusOK || out.Outcome != "code_sent" || out.Challenge == nil || out.Message != nil {
		t.Fatalf("WRONG — accepted by Termii: %d %+v; want code_sent with a challenge and no message", st, out)
	}
	rec, ok := lastSend(t, p.user)
	if !ok || rec.outcome != "accepted" || rec.messageID == nil || *rec.messageID != termiiMsgID {
		t.Fatalf("UNRECORDED — the send: %+v (found %v); want accepted with Termii's message id %s", rec, ok, termiiMsgID)
	}
	if !verifies(t, srv, *out.Challenge, f.codeSent(t)) {
		t.Fatalf("UNUSABLE — the code Termii was handed does not sign the member in")
	}
}

// Refused: code_not_sent, no challenge, the words; recorded with Termii's
// refusal code; NOTHING counted against the lockout (the PIN was right).
func TestApiSms02_ARefusedCodeIsNotSentAndCountsNothing(t *testing.T) {
	f := newFakeTermii(t, "refused_body")
	srv := smsServer(t, f.srv.URL)
	p := smsMember(t)
	before := failedAttemptsOf(t, p.user)
	out, st := newDeviceSignIn(t, srv, p)
	if st != http.StatusOK || out.Outcome != "code_not_sent" || out.Challenge != nil {
		t.Fatalf("WRONG — refused by Termii: %d %+v; want code_not_sent and no challenge (no code is on its way)", st, out)
	}
	if out.Message == nil || !strings.Contains(*out.Message, "try again in a minute") || !strings.Contains(*out.Message, "officer") {
		t.Fatalf("UNSAID — %v; want: try again in a minute, an officer if it keeps failing", out.Message)
	}
	if rec, ok := lastSend(t, p.user); !ok || rec.outcome != "refused" || rec.code == nil || *rec.code != "insufficient_balance" || rec.messageID != nil {
		t.Fatalf("UNRECORDED — the refusal: %+v (found %v); want refused, insufficient_balance, no message id", rec, ok)
	}
	if after := failedAttemptsOf(t, p.user); after != before {
		t.Fatalf("COUNTED — Termii's refusal added %d failed attempts; the PIN was right and the failure was ours", after-before)
	}
}

func TestApiSms03_AnUnreachedProviderIsNotSentAndCountsNothing(t *testing.T) {
	srv := smsServer(t, deadURL(t))
	p := smsMember(t)
	before := failedAttemptsOf(t, p.user)
	out, _ := newDeviceSignIn(t, srv, p)
	if out.Outcome != "code_not_sent" || out.Challenge != nil {
		t.Fatalf("WRONG — Termii unreachable: %+v; want code_not_sent, no challenge", out)
	}
	if rec, ok := lastSend(t, p.user); !ok || rec.outcome != "not_reached" {
		t.Fatalf("UNRECORDED — %+v (found %v); want not_reached", rec, ok)
	}
	if after := failedAttemptsOf(t, p.user); after != before {
		t.Fatalf("COUNTED — an unreachable provider added %d failed attempts", after-before)
	}
}

// Unknown: the code may be on its way. code_sent WITH the challenge, and
// word that it may be late; recorded as unknown; and if it arrives, it works.
func TestApiSms04_AnUnknownSendKeepsTheChallengeAndSaysItMayBeLate(t *testing.T) {
	f := newFakeTermii(t, "slow")
	srv := smsServer(t, f.srv.URL)
	p := smsMember(t)
	out, _ := newDeviceSignIn(t, srv, p)
	if out.Outcome != "code_sent" || out.Challenge == nil {
		t.Fatalf("STRANDED — no answer from Termii in time: %+v; want code_sent with the challenge (the code may arrive)", out)
	}
	if out.Message == nil || !strings.Contains(*out.Message, "may take a little longer") {
		t.Fatalf("UNSAID — %v; want word that the code may be late", out.Message)
	}
	if rec, ok := lastSend(t, p.user); !ok || rec.outcome != "unknown" || rec.messageID != nil {
		t.Fatalf("UNRECORDED — %+v (found %v); want unknown, no message id", rec, ok)
	}
	if !verifies(t, srv, *out.Challenge, f.codeSent(t)) {
		t.Fatalf("UNUSABLE — the late code does not sign the member in")
	}
}

// The configuration (piece 1): with an auth service, ARGON_SMS names the
// sender, termii or refusing, and unset refuses to start: nobody inherits
// "no codes" by forgetting a line. termii needs TERMII_URL (https: the key
// travels in the body), TERMII_API_KEY and TERMII_SENDER_ID, none defaulted;
// and no refusal repeats the key.
func TestApiSms06_TheConfigurationNamesTheSender(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://nobody@127.0.0.1:1/none")
	t.Setenv("ARGON_CSRF_KEY", strings.Repeat("ab", 32))
	t.Setenv("AUTH_DATABASE_URL", "postgres://nobody@127.0.0.1:1/none")
	t.Setenv("AUTH_CODE_KEY", strings.Repeat("cd", 32))
	termii := map[string]string{"TERMII_URL": "https://termii.example/api/sms/send", "TERMII_API_KEY": termiiKey, "TERMII_SENDER_ID": termiiSender}
	set := func(sms string, drop string, over map[string]string) {
		t.Setenv("ARGON_SMS", sms)
		for k, v := range termii {
			if k == drop {
				v = ""
			}
			if o, ok := over[k]; ok {
				v = o
			}
			t.Setenv(k, v)
		}
	}
	refuses := func(what, names string) {
		t.Helper()
		_, err := loadConfig()
		if err == nil || !strings.Contains(err.Error(), names) {
			t.Fatalf("STARTED — %s: the configuration loaded (%v); it must refuse, naming %s", what, err, names)
		}
		if strings.Contains(err.Error(), termiiKey) {
			t.Fatalf("LEAKED — %s: the refusal repeats the Termii key", what)
		}
	}
	set("", "", nil)
	refuses("ARGON_SMS unset", "ARGON_SMS")
	set("twilio", "", nil)
	refuses("ARGON_SMS=twilio", "ARGON_SMS")
	for _, k := range []string{"TERMII_URL", "TERMII_API_KEY", "TERMII_SENDER_ID"} {
		set("termii", k, nil)
		refuses("termii without "+k, k)
	}
	set("termii", "", map[string]string{"TERMII_URL": "http://termii.example/api/sms/send"})
	refuses("termii over plain http", "TERMII_URL")

	set("termii", "", nil)
	cfg, err := loadConfig()
	if err != nil || cfg.SMS != "termii" || cfg.TermiiURL != termii["TERMII_URL"] || cfg.TermiiAPIKey != termiiKey || cfg.TermiiSenderID != termiiSender {
		t.Fatalf("REFUSED — a complete termii configuration: %v (sms %q, sender %q)", err, cfg.SMS, cfg.TermiiSenderID)
	}
	set("refusing", "", map[string]string{"TERMII_URL": "", "TERMII_API_KEY": "", "TERMII_SENDER_ID": ""})
	if cfg, err := loadConfig(); err != nil || cfg.SMS != "refusing" {
		t.Fatalf("REFUSED — ARGON_SMS=refusing: %v (sms %q)", err, cfg.SMS)
	}
}

// A refusal is something to retry, not an officer's case: it is never
// routed to see_officer. That stays for no single agreed number.
func TestApiSms05_ARefusalIsNeverRoutedToAnOfficer(t *testing.T) {
	f := newFakeTermii(t, "refused_4xx")
	srv := smsServer(t, f.srv.URL)
	out, _ := newDeviceSignIn(t, srv, smsMember(t))
	if out.Outcome == "see_officer" {
		t.Fatalf("ROUTED — Termii's refusal sent the member to an officer: %+v; an outage is retried, a shared handset goes to an officer", out)
	}
}
