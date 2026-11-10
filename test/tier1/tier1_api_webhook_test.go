//go:build tier1

// ACCEPTANCE TESTS — VFD'S WEBHOOK (stage 3, 2026-11-10; docs/VFD_INTEGRATION.md
// §2, requirements 2–4). POST /webhooks/vfd: kept exactly as received before
// VFD is answered; never believed (nothing credited at the door: the wallet
// worker asks VFD, suite wallet); in hmac mode no unsigned path at all.
package tier1

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

var webhookSecret = []byte("test-webhook-secret-not-a-secret-5d1e")

func webhookServer(t *testing.T, hook api.Webhook) *httptest.Server {
	t.Helper()
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		h = newFakeAPIWith(&fakeAPI{db: db, brk: brk, webhook: hook})
	} else {
		app, authp := rolePools(t, 0)
		svc, err := auth.New(auth.Deps{DB: authp, SMS: &fakeSMS{}, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon})
		fixture(t, err, "the auth service")
		h, err = api.NewServer(api.Deps{DB: app, Auth: svc, CSRFKey: testCSRFKey, VFDWebhook: hook})
		fixture(t, err, "the API")
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func hookSign(body []byte) string {
	m := hmac.New(sha512.New, webhookSecret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func postHook(t *testing.T, srv *httptest.Server, body []byte, sig string) resp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/webhooks/vfd", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-VFD-Signature", sig)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "POST /webhooks/vfd")
	defer r.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(r.Body)
	return resp{r.StatusCode, b.Bytes()}
}

// A body as VFD sends it: its own spacing and field order, which the record keeps.
func hookBody(ref string) []byte {
	return []byte(`{ "reference":"` + ref + `",  "amount":"10000.00", "account_number":"1009998887",
  "originator_account_name":"ADEBAYO FOLAKE", "session_id":"S-1" }`)
}

func kept(t *testing.T, ref string) (n int, raw string, signed bool) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), `SELECT count(*), coalesce(max(raw), ''), coalesce(bool_or(signed), false) FROM vfd_notifications WHERE reference = $1`, ref).Scan(&n, &raw, &signed), "the notifications")
	return
}

func TestApiWebhook01_ANotificationIsKeptExactlyAsReceivedBeforeTheAnswer(t *testing.T) {
	srv := webhookServer(t, api.Webhook{Mode: "unsigned"})
	ref := vfdRefNew()
	body := hookBody(ref)
	r := postHook(t, srv, body, "")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"kept":true`) {
		t.Fatalf("REFUSED — %d %s; want 200, kept", r.status, r.body)
	}
	n, raw, signed := kept(t, ref)
	if n != 1 || raw != string(body) || signed {
		t.Fatalf("NOT AS RECEIVED — %d kept, signed %v, raw %q; want one, unsigned, byte for byte as VFD sent it", n, signed, raw)
	}
}

// hmac: there is no unsigned path.
func TestApiWebhook02_SignedModeHasNoUnsignedPath(t *testing.T) {
	srv := webhookServer(t, api.Webhook{Mode: "hmac", Secret: webhookSecret})
	for _, c := range []struct{ sig, code, what string }{
		{"", "signature_required", "a notification with no signature"},
		{hookSign([]byte("another body")), "signature_wrong", "a notification signed over another body"},
	} {
		ref := vfdRefNew()
		wantProblem(t, postHook(t, srv, hookBody(ref), c.sig), http.StatusUnauthorized, c.code, c.what)
		if n, _, _ := kept(t, ref); n != 0 {
			t.Fatalf("KEPT — %s was kept (%d)", c.what, n)
		}
	}
	ref := vfdRefNew()
	body := hookBody(ref)
	if r := postHook(t, srv, body, hookSign(body)); r.status != http.StatusOK {
		t.Fatalf("REFUSED — a correctly signed notification: %d %s", r.status, r.body)
	}
	if n, _, signed := kept(t, ref); n != 1 || !signed {
		t.Fatalf("WRONG — a signed notification: %d kept, signed %v; want one, signed", n, signed)
	}
}

// Never believed at the door: nothing is credited until VFD confirms it.
func TestApiWebhook03_NothingIsCreditedAtTheDoor(t *testing.T) {
	w := newWalletWorld(t)
	srv := webhookServer(t, api.Webhook{Mode: "unsigned"})
	ref := vfdRefNew()
	body := []byte(`{"reference":"` + ref + `","amount":"10000.00","account_number":"` + w.socAcct + `"}`)
	if r := postHook(t, srv, body, ""); r.status != http.StatusOK {
		t.Fatalf("FIXTURE: the notification: %d %s", r.status, r.body)
	}
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM vfd_inflows WHERE vfd_reference = $1`, ref).Scan(&n), "inflows")
	if n != 0 {
		t.Fatalf("BELIEVED — the notification itself was credited (%d inflow): anyone who can reach this URL could credit a society", n)
	}
}

func TestApiWebhook04_OffTakesNothing(t *testing.T) {
	srv := webhookServer(t, api.Webhook{Mode: "off"})
	ref := vfdRefNew()
	wantProblem(t, postHook(t, srv, hookBody(ref), ""), http.StatusServiceUnavailable, "webhook_off", "a notification to a server that takes none")
	if n, _, _ := kept(t, ref); n != 0 {
		t.Fatalf("KEPT — a server that takes no notifications kept one")
	}
}

func TestApiWebhook05_ATooLargeBodyIsRefusedAndNotKept(t *testing.T) {
	srv := webhookServer(t, api.Webhook{Mode: "unsigned"})
	ref := vfdRefNew()
	body := []byte(`{"reference":"` + ref + `","amount":"1.00","pad":"` + strings.Repeat("x", api.MaxNotification) + `"}`)
	wantProblem(t, postHook(t, srv, body, ""), http.StatusRequestEntityTooLarge, "too_large", "a notification over 64 KiB")
	if n, _, _ := kept(t, ref); n != 0 {
		t.Fatalf("KEPT — a body over the limit was kept")
	}
}

// How VFD is trusted at the door is said, never inherited: unset is a refusal
// to start (forgetting the line must not quietly take every notification);
// hmac needs its secret, and no refusal repeats it.
func TestApiWebhook06_TheConfigurationNamesTheMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://nobody@127.0.0.1:1/none")
	t.Setenv("ARGON_CSRF_KEY", strings.Repeat("ab", 32))
	t.Setenv("AUTH_DATABASE_URL", "")
	t.Setenv("AUTH_CODE_KEY", "")
	secret := "a-webhook-secret-that-is-long-enough-1234"
	for _, c := range []struct{ mode, secret, what string }{
		{"", "", "no ARGON_VFD_WEBHOOK"}, {"yes", "", "an unknown mode"},
		{"hmac", "", "hmac with no secret"}, {"hmac", "short-secret", "hmac with a short secret"},
	} {
		t.Setenv("ARGON_VFD_WEBHOOK", c.mode)
		t.Setenv("VFD_WEBHOOK_SECRET", c.secret)
		_, err := loadConfig()
		if err == nil || !strings.Contains(err.Error(), "ARGON_VFD_WEBHOOK") {
			t.Fatalf("STARTED — %s: the configuration loaded (%v); it must refuse, naming ARGON_VFD_WEBHOOK", c.what, err)
		}
		if c.secret != "" && strings.Contains(err.Error(), c.secret) {
			t.Fatalf("LEAKED — the refusal repeats the secret: %v", err)
		}
	}
	t.Setenv("ARGON_VFD_WEBHOOK", "hmac")
	t.Setenv("VFD_WEBHOOK_SECRET", secret)
	cfg, err := loadConfig()
	if err != nil || cfg.VFDWebhook != "hmac" || string(cfg.VFDWebhookSecret) != secret {
		t.Fatalf("REFUSED — hmac with its secret: %v (%q)", err, cfg.VFDWebhook)
	}
	t.Setenv("ARGON_VFD_WEBHOOK", "off")
	if cfg, err = loadConfig(); err != nil || cfg.VFDWebhook != "off" {
		t.Fatalf("REFUSED — off: %v", err)
	}
}
