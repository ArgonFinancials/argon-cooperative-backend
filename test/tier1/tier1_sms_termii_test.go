//go:build tier1

// ACCEPTANCE TESTS — THE TERMII SENDER (reviewed in four pieces, 2026-10-28).
// Re-implemented in Go here, not called through the wallet or loan services;
// tested against a FAKE Termii server inside the test: no network, no real key.
//
// What they pin, from the review:
//   - "sent" means accepted by Termii: a 2xx, a readable body reporting
//     success, and a message id. Never "delivered".
//   - four outcomes kept apart: accepted, refused, not_reached, unknown; a dead
//     connection is never reported as a parsing error;
//   - one call per code, never a retry inside the sender;
//   - only +234 E.164 numbers; anything else refused before sending;
//   - the code, the key, the full number and the text appear in NO log line,
//     asserted against the actual code sent, not a pattern;
//   - no URL, key or sender ID, no sender.
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argon/backend/internal/auth"
)

const (
	termiiKey    = "test-termii-key-not-a-secret-7f3a"
	termiiSender = "ArgonTest"
	memberPhone  = "+2348031234567"
	sentCode     = "482913"
	sentText     = "Your Argon code is " + sentCode + ". It expires in 5 minutes. Nobody from Argon will ask you for it."
	termiiMsgID  = "9122821270554876574"
)

// fakeTermii: a Termii send endpoint inside the test, answering one way.
type fakeTermii struct {
	srv      *httptest.Server
	mu       sync.Mutex
	received []map[string]any
}

func newFakeTermii(t *testing.T, mode string) *fakeTermii {
	t.Helper()
	f := &fakeTermii{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.received = append(f.received, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "ok":
			w.Write([]byte(`{"code":"ok","message_id":"` + termiiMsgID + `","message":"Successfully Sent","balance":9,"user":"Argon"}`))
		case "refused_body":
			w.Write([]byte(`{"code":"insufficient_balance","message":"Your balance is too low to send this message"}`))
		case "refused_4xx":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"invalid_sender","message":"ApplicationSenderId not found"}`))
		case "slow":
			time.Sleep(1500 * time.Millisecond)
			w.Write([]byte(`{"code":"ok","message_id":"` + termiiMsgID + `"}`))
		case "garbage":
			w.Write([]byte(`<html>502 Bad Gateway</html>`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTermii) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.received) }

// deadURL: an address nothing listens on (the connection is never made).
func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	fixture(t, err, "a free port")
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/api/sms/send"
}

// termiiSenderFor: the sender under test: the real one, or under
// ARGON_TEST_SMS_FAKE the reference (smsfake_termii_test.go) with its break.
func termiiSenderFor(t *testing.T, url string, logs *bytes.Buffer) auth.SMS {
	t.Helper()
	cfg := auth.TermiiConfig{URL: url, APIKey: termiiKey, SenderID: termiiSender, Timeout: 500 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	s, err := newTermiiSender(cfg)
	if err != nil {
		t.Fatalf("NOT BUILT — the Termii sender refused a complete configuration: %v", err)
	}
	return s
}

func newTermiiSender(cfg auth.TermiiConfig) (auth.SMS, error) {
	if brk, ok := os.LookupEnv("ARGON_TEST_SMS_FAKE"); ok {
		return newRefTermii(cfg, brk)
	}
	return auth.NewTermii(cfg)
}

func sendCode(t *testing.T, s auth.SMS, to string) auth.Sent {
	t.Helper()
	got, err := s.Send(context.Background(), to, sentText)
	if err != nil {
		t.Fatalf("WRONG SHAPE — a Termii send answered an error (%v); its outcome belongs in Sent, the error is for a sender that cannot send at all", err)
	}
	return got
}

func TestSmsTermii01_AnAcceptedSendCarriesTermiisMessageId(t *testing.T) {
	f := newFakeTermii(t, "ok")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), memberPhone)
	if got.Provider != "termii" || got.Outcome != auth.SendAccepted || got.MessageID != termiiMsgID {
		t.Fatalf("WRONG — %+v; want termii, accepted, message id %s", got, termiiMsgID)
	}
	if f.count() != 1 {
		t.Fatalf("RETRIED — Termii received %d requests for one code; one call per code", f.count())
	}
	b := f.received[0]
	for k, want := range map[string]string{"to": memberPhone, "from": termiiSender, "sms": sentText, "type": "plain", "channel": "dnd", "api_key": termiiKey} {
		if got, _ := b[k].(string); got != want {
			t.Fatalf("WRONG REQUEST — %s is %q; want %q (the DND route, the configured sender, the text as given)", k, got, want)
		}
	}
}

// A 2xx is not success: Termii's body is read.
func TestSmsTermii02_ARefusalInTheBodyIsARefusal(t *testing.T) {
	f := newFakeTermii(t, "refused_body")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), memberPhone)
	if got.Outcome != auth.SendRefused || got.ProviderCode != "insufficient_balance" || got.MessageID != "" {
		t.Fatalf("TAKEN AS SENT — a 200 whose body refuses: %+v; want refused, code insufficient_balance, no message id", got)
	}
	if f.count() != 1 {
		t.Fatalf("RETRIED — %d requests for one code", f.count())
	}
}

func TestSmsTermii03_ARefusalByStatusIsARefusal(t *testing.T) {
	f := newFakeTermii(t, "refused_4xx")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), memberPhone)
	if got.Outcome != auth.SendRefused || got.ProviderCode != "invalid_sender" {
		t.Fatalf("WRONG — a 400 refusal: %+v; want refused, code invalid_sender", got)
	}
}

// The connection never made: not_reached, its real error kept, never a
// parsing error (how an outage gets diagnosed as a bug).
func TestSmsTermii04_NoConnectionIsNotReachedAndSaysSo(t *testing.T) {
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, deadURL(t), &logs), memberPhone)
	if got.Outcome != auth.SendNotReached {
		t.Fatalf("WRONG CLASS — no connection: %+v; want not_reached", got)
	}
	l := strings.ToLower(logs.String())
	if !strings.Contains(l, "connect") || strings.Contains(l, "parse") || strings.Contains(l, "unmarshal") || strings.Contains(l, "invalid character") {
		t.Fatalf("MISDIAGNOSED — the log for a dead connection: %q; want the connection error, never a parsing one", logs.String())
	}
}

// No answer in time: the request may have reached Termii, so unknown.
func TestSmsTermii05_NoAnswerInTimeIsUnknown(t *testing.T) {
	f := newFakeTermii(t, "slow")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), memberPhone)
	if got.Outcome != auth.SendUnknown {
		t.Fatalf("WRONG CLASS — no answer in time: %+v; want unknown (the code may be on its way)", got)
	}
}

func TestSmsTermii06_AnUnreadableAnswerIsUnknown(t *testing.T) {
	f := newFakeTermii(t, "garbage")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), memberPhone)
	if got.Outcome != auth.SendUnknown || got.MessageID != "" {
		t.Fatalf("WRONG CLASS — a 200 with an unreadable body: %+v; want unknown, no message id", got)
	}
}

// Only +234 E.164, refused before sending: no guessing at a format.
func TestSmsTermii07_ANumberNotInE164IsRefusedBeforeSending(t *testing.T) {
	f := newFakeTermii(t, "ok")
	var logs bytes.Buffer
	got := sendCode(t, termiiSenderFor(t, f.srv.URL, &logs), "08031234567")
	if got.Outcome != auth.SendRefused || f.count() != 0 {
		t.Fatalf("GUESSED — 08031234567: %+v, %d requests to Termii; want refused before sending", got, f.count())
	}
}

// NEVER LOGGED, as tests not intentions: the code (the actual one sent), the
// key, the full number and the text appear in no line, in every outcome.
func TestSmsTermii08_TheCodeTheKeyTheNumberAndTheTextAreNeverLogged(t *testing.T) {
	for _, mode := range []string{"ok", "refused_body", "refused_4xx", "garbage", "dead", "slow"} {
		var logs bytes.Buffer
		url := deadURL(t)
		if mode != "dead" {
			url = newFakeTermii(t, mode).srv.URL
		}
		sendCode(t, termiiSenderFor(t, url, &logs), memberPhone)
		out := logs.String()
		for what, secret := range map[string]string{"the code": sentCode, "the API key": termiiKey, "the full number": strings.TrimPrefix(memberPhone, "+"), "the text": "It expires in 5 minutes"} {
			if strings.Contains(out, secret) {
				t.Fatalf("LOGGED — %s appears in the log (%s): %q", what, mode, out)
			}
		}
		if mode == "ok" && !strings.Contains(out, termiiMsgID) {
			t.Fatalf("UNTRACEABLE — an accepted send's log line lacks Termii's message id: %q", out)
		}
	}
}

// No URL, key or sender ID: no sender.
func TestSmsTermii09_NoKeyNoSender(t *testing.T) {
	full := auth.TermiiConfig{URL: "http://127.0.0.1:1/api/sms/send", APIKey: termiiKey, SenderID: termiiSender}
	for what, c := range map[string]auth.TermiiConfig{
		"no URL":       {APIKey: full.APIKey, SenderID: full.SenderID},
		"no API key":   {URL: full.URL, SenderID: full.SenderID},
		"no sender ID": {URL: full.URL, APIKey: full.APIKey},
	} {
		if s, err := newTermiiSender(c); err == nil || s != nil {
			t.Fatalf("STARTED — a Termii sender with %s; it must refuse", what)
		}
	}
}
