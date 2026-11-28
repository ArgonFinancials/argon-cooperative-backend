//go:build tier1

// ACCEPTANCE TESTS — THE VFD RELAY (cmd/vfd-proxy, internal/vfdproxy;
// decided 2026-11-28). It carries the cooperative's five VFD calls through the
// wallet's server, the one address VFD accepts, and holds VFD's token so the
// cooperative never does.
//
//   - a signed call on the allowlist passes ONCE, to VFD, with the token, and
//     VFD's status, content type and body come back byte for byte;
//   - unsigned, wrongly signed, a changed body, stale, replayed, or any other
//     path: refused by the relay, marked as the relay's, VFD never asked;
//   - VFD not reached and VFD too slow are said apart, never retried;
//   - every call is recorded, the BVN never in the record, nor the token;
//   - a previous key is accepted during a rotation;
//   - every answer carries the relay's clock.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/argon/backend/internal/vfdproxy"
	"github.com/argon/backend/internal/vfdsign"
)

var (
	pxKey      = []byte("proxy-key-for-the-tests-only-0123456789abcdef")
	pxOldKey   = []byte("proxy-old-key-for-the-tests-only-9876543210ab")
	pxToken    = "vfd-token-for-the-proxy-tests-not-a-secret-77aa"
	pxBVN      = "22212345678"
	pxVFDReply = []byte("{\"status\":\"00\",  \"message\":\"Account found\",\"data\":{\"name\":\"ADEBAYO FOLAKE\",\"bvn\":\"" + "22212345678" + "\"}}\n")
)

// upstream: VFD as the relay meets it. It records what it was sent.
type upstream struct {
	mu     sync.Mutex
	calls  []*http.Request
	bodies [][]byte
	srv    *httptest.Server
}

func newUpstream(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *upstream {
	t.Helper()
	u := &upstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.calls, u.bodies = append(u.calls, r), append(u.bodies, b)
		u.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) count() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.calls) }

func proxyFor(t *testing.T, upstreamURL string, keys [][]byte, log *bytes.Buffer, timeout time.Duration) *httptest.Server {
	t.Helper()
	cfg := vfdproxy.Config{Upstream: upstreamURL, Token: pxToken, Keys: keys, Log: log, Timeout: timeout}
	var h http.Handler
	var err error
	if brk, fake := os.LookupEnv("ARGON_TEST_PROXY_FAKE"); fake {
		h, err = newRefProxyWith(cfg, brk)
	} else {
		h, err = vfdproxy.New(cfg)
	}
	fixture(t, err, "the relay")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// pxCall: a call to the relay, signed with key over `signed` (nil: the body),
// at `at` (zero: now).
func pxCall(t *testing.T, srv *httptest.Server, method, pathQuery string, body, signed []byte, key []byte, at time.Time) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+pathQuery, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != nil {
		if at.IsZero() {
			at = time.Now()
		}
		if signed == nil {
			signed = body
		}
		ts := at.Unix()
		req.Header.Set(vfdsign.HeaderTime, strconv.FormatInt(ts, 10))
		req.Header.Set(vfdsign.HeaderSignature, vfdsign.Sign(key, method, req.URL.EscapedPath(), req.URL.RawQuery, ts, signed))
	}
	resp, err := http.DefaultClient.Do(req)
	fixture(t, err, "the call to the relay")
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func pxBody(r *http.Response) []byte { b, _ := io.ReadAll(r.Body); return b }

func okVFD(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	w.Write(pxVFDReply)
}

func TestVfdProxy01_ASignedCallPassesOnceAndVFDsAnswerComesBackUnchanged(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	req := []byte(`{"amount":"6000.00","toAccount":"0123456789"}`)
	resp := pxCall(t, px, http.MethodPost, "/transfer", req, nil, pxKey, time.Time{})
	got := pxBody(resp)
	if resp.StatusCode != http.StatusAccepted || !bytes.Equal(got, pxVFDReply) || resp.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("CHANGED — VFD answered 202 %q; the relay returned %d %q (%s); want VFD's status, body and content type byte for byte", pxVFDReply, resp.StatusCode, got, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get(vfdsign.HeaderProxy) != "" {
		t.Fatalf("MARKED — VFD's own answer came back marked as the relay's (%s)", resp.Header.Get(vfdsign.HeaderProxy))
	}
	if up.count() != 1 || up.calls[0].Header.Get("AccessToken") != pxToken || !bytes.Equal(up.bodies[0], req) || up.calls[0].URL.Path != "/transfer" {
		t.Fatalf("WRONG FORWARD — VFD saw %d calls; want one, to /transfer, the body as sent, with VFD's token", up.count())
	}
}

func TestVfdProxy02_AnUnsignedOrWronglySignedCallIsRefusedAndVFDNeverAsked(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	for _, c := range []struct {
		key  []byte
		what string
	}{{nil, "unsigned"}, {[]byte("some-other-key-0123456789abcdef-0123456789"), "signed with another key"}} {
		resp := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, c.key, time.Time{})
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get(vfdsign.HeaderProxy) != "bad_signature" {
			t.Fatalf("PASSED — a call %s: %d (%q); want 401, marked bad_signature as the relay's own", c.what, resp.StatusCode, resp.Header.Get(vfdsign.HeaderProxy))
		}
	}
	if up.count() != 0 {
		t.Fatalf("ASKED — VFD saw %d calls the relay should have refused", up.count())
	}
}

func TestVfdProxy03_TheSignatureCoversTheBody(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	resp := pxCall(t, px, http.MethodPost, "/transfer", []byte(`{"amount":"600000.00"}`), []byte(`{"amount":"6000.00"}`), pxKey, time.Time{})
	if resp.StatusCode != http.StatusUnauthorized || up.count() != 0 {
		t.Fatalf("PASSED — a transfer's amount changed in transit, signed for 6000: %d, VFD saw %d calls; want 401, none", resp.StatusCode, up.count())
	}
}

func TestVfdProxy04_AStaleOrReplayedCallIsRefused(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	if r := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxKey, time.Now().Add(-2*time.Minute)); r.StatusCode != http.StatusUnauthorized || r.Header.Get(vfdsign.HeaderProxy) != "stale" {
		t.Fatalf("PASSED — a call signed two minutes ago: %d (%q); want 401 stale", r.StatusCode, r.Header.Get(vfdsign.HeaderProxy))
	}
	at := time.Now()
	first := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxKey, at)
	again := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxKey, at)
	if first.StatusCode != http.StatusAccepted || again.StatusCode != http.StatusUnauthorized || again.Header.Get(vfdsign.HeaderProxy) != "replayed" || up.count() != 1 {
		t.Fatalf("REPLAYED — the same signed call twice: %d then %d (%q), VFD saw %d; want the second refused as replayed, VFD asked once", first.StatusCode, again.StatusCode, again.Header.Get(vfdsign.HeaderProxy), up.count())
	}
}

func TestVfdProxy05_OnlyTheCooperativesFiveCalls(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	for _, c := range [][2]string{{http.MethodPost, "/client/create"}, {http.MethodPost, "/credit"}, {http.MethodPost, "/transactions"}, {http.MethodGet, "/transfer"}, {http.MethodPost, "/virtualaccount"}} {
		r := pxCall(t, px, c[0], c[1], []byte(`{}`), nil, pxKey, time.Time{})
		if r.StatusCode != http.StatusForbidden || r.Header.Get(vfdsign.HeaderProxy) != "not_allowed" {
			t.Fatalf("PASSED — %s %s, signed: %d; want 403 not_allowed (the relay passes five calls and nothing else)", c[0], c[1], r.StatusCode)
		}
	}
	if up.count() != 0 {
		t.Fatalf("ASKED — VFD saw %d calls outside the five", up.count())
	}
}

func TestVfdProxy06_UnreachableAndSlowAreSaidApartAndNeverRetried(t *testing.T) {
	var log bytes.Buffer
	// VFD down: the connection is refused.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + ln.Addr().String()
	ln.Close()
	px := proxyFor(t, dead, [][]byte{pxKey}, &log, time.Second)
	if r := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxKey, time.Time{}); r.StatusCode != http.StatusBadGateway || r.Header.Get(vfdsign.HeaderProxy) != "vfd_unreachable" {
		t.Fatalf("WRONG — VFD down: %d (%q); want 502 vfd_unreachable", r.StatusCode, r.Header.Get(vfdsign.HeaderProxy))
	}
	// VFD slow: it took the send and did not answer in time. It may have acted.
	slow := newUpstream(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(600 * time.Millisecond) })
	px2 := proxyFor(t, slow.srv.URL, [][]byte{pxKey}, &log, 200*time.Millisecond)
	r := pxCall(t, px2, http.MethodPost, "/transfer", []byte(`{"amount":"6000.00"}`), nil, pxKey, time.Time{})
	if r.StatusCode != http.StatusGatewayTimeout || r.Header.Get(vfdsign.HeaderProxy) != "vfd_timeout" {
		t.Fatalf("WRONG — VFD slow: %d (%q); want 504 vfd_timeout (it may have acted: the cooperative asks, never assumes)", r.StatusCode, r.Header.Get(vfdsign.HeaderProxy))
	}
	time.Sleep(700 * time.Millisecond)
	if n := slow.count(); n != 1 {
		t.Fatalf("RETRIED — VFD saw the send %d times; want once (a send retried is a payment made twice)", n)
	}
}

func TestVfdProxy07_EveryCallIsRecordedNeverTheBVNNorTheToken(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	pxCall(t, px, http.MethodGet, "/transfer/recipient?accountNo=0123456789&bank=000013&transfer_type=inter", nil, nil, pxKey, time.Time{})
	pxCall(t, px, http.MethodPost, "/transfer", []byte(`{"amount":"6000.00","toBvn":"`+pxBVN+`","toAccount":"0123456789"}`), nil, pxKey, time.Time{})
	pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, nil, time.Time{}) // refused, recorded too
	rec := log.String()
	if n := strings.Count(rec, "\n"); n != 3 {
		t.Fatalf("UNRECORDED — %d lines for 3 calls: %s", n, rec)
	}
	var lines []struct{ Path, Query, Request, Response, Outcome string }
	for _, l := range strings.Split(strings.TrimSpace(rec), "\n") {
		var e struct{ Path, Query, Request, Response, Outcome string }
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("UNREADABLE — a record line is not one JSON object: %s", l)
		}
		lines = append(lines, e)
	}
	// Verbatim but for the BVN: the same bytes, key order and spacing kept.
	wantReq := `{"amount":"6000.00","toBvn":"[redacted]","toAccount":"0123456789"}`
	wantResp := strings.Replace(string(pxVFDReply), `"bvn":"`+pxBVN+`"`, `"bvn":"[redacted]"`, 1)
	if lines[0].Path != "/transfer/recipient" || lines[0].Query != "accountNo=0123456789&bank=000013&transfer_type=inter" ||
		lines[1].Request != wantReq || lines[1].Response != wantResp || lines[2].Outcome != "bad_signature" {
		t.Fatalf("NOT VERBATIM — the record does not hold what was asked and answered, as it was, but for the BVN:\n%s\nwant request %s\nwant answer %q", rec, wantReq, wantResp)
	}
	if strings.Contains(rec, pxBVN) {
		t.Fatalf("BVN RECORDED — the record holds a BVN: %s", rec)
	}
	if strings.Contains(rec, pxToken) {
		t.Fatalf("TOKEN RECORDED — the record holds VFD's token")
	}
}

func TestVfdProxy08_ThePreviousKeyIsAcceptedDuringARotation(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey, pxOldKey}, &log, time.Second)
	if r := pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxOldKey, time.Time{}); r.StatusCode != http.StatusAccepted {
		t.Fatalf("REFUSED — a call signed with the previous key, mid-rotation: %d; want it passed", r.StatusCode)
	}
}

func TestVfdProxy09_EveryAnswerCarriesTheRelaysClock(t *testing.T) {
	up := newUpstream(t, okVFD)
	var log bytes.Buffer
	px := proxyFor(t, up.srv.URL, [][]byte{pxKey}, &log, time.Second)
	for _, r := range []*http.Response{
		pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, pxKey, time.Time{}),
		pxCall(t, px, http.MethodGet, "/account/enquiry", nil, nil, nil, time.Time{}),
	} {
		at, err := strconv.ParseInt(r.Header.Get(vfdsign.HeaderProxyTime), 10, 64)
		if err != nil || at < time.Now().Unix()-5 {
			t.Fatalf("NO CLOCK — an answer (%d) carries the relay's time as %q; want it on every answer, so drift is seen before signatures fail", r.StatusCode, r.Header.Get(vfdsign.HeaderProxyTime))
		}
	}
}
