//go:build tier1

// ACCEPTANCE TESTS — THE BROWSER SESSION (decided 2026-10-23,
// LEDGER_CONTRACT.md §12; reviewed as pieces 5-7 of that day). A browser holds
// its session in an httpOnly cookie and its device in another, never a
// credential in script; a cookie session's state changes carry the session's
// own CSRF token; non-browser clients (no Origin) keep the Bearer header.
//
// "Browser" here means the request carries an Origin, as every call from the
// app's page to the API does. Cookies are set and read by hand: the test
// server is plain http, and a cookie jar would (rightly) drop Secure cookies.
package tier1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

const appOrigin = "http://localhost:3000"

// sessionServer: the API with the app's origin allowed and the sign-in
// service, real or the reference fake, on the test CSRF key.
func sessionServer(t *testing.T) (*httptest.Server, *fakeSMS) {
	t.Helper()
	return sessionServerKey(t, testCSRFKey)
}

// startServer builds the server as cmd/api does, with the given CSRF key,
// and returns its refusal to start if it gives one.
func startServer(t *testing.T, sms *fakeSMS, key []byte) (http.Handler, error) {
	t.Helper()
	deps := auth.Deps{SMS: sms, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon}
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		deps.DB = db
		h, err := newFakeServer(&fakeAPI{db: db, brk: brk, deps: deps, csrfKey: key})
		if err != nil {
			return nil, err
		}
		return fakeCORS([]string{appOrigin}, brk, h), nil
	}
	app, authp := rolePools(t, 0)
	deps.DB = authp
	svc, err := auth.New(deps)
	fixture(t, err, "the auth service")
	return api.NewServer(api.Deps{DB: app, Auth: svc, AllowedOrigins: []string{appOrigin}, CSRFKey: key})
}

// sessionServerKey: two servers on two keys share one database: a rotation.
func sessionServerKey(t *testing.T, key []byte) (*httptest.Server, *fakeSMS) {
	t.Helper()
	sms := &fakeSMS{}
	h, err := startServer(t, sms, key)
	fixture(t, err, "the API")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, sms
}

type call struct {
	method, path    string
	origin          bool   // sent from the app's page
	bearer, csrf    string // Authorization: Bearer, X-CSRF-Token
	session, device string // the cookies' values
	body            any
	ctype           string // Content-Type: "" means application/json when there is a body; "-" sends none
}

type answer struct {
	status  int
	body    []byte
	header  http.Header
	cookies []*http.Cookie
}

func send(t *testing.T, srv *httptest.Server, c call) answer {
	t.Helper()
	var rd io.Reader
	if c.body != nil {
		raw, _ := json.Marshal(c.body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(c.method, srv.URL+c.path, rd)
	switch {
	case c.ctype == "-":
	case c.ctype != "":
		req.Header.Set("Content-Type", c.ctype)
	case c.body != nil:
		req.Header.Set("Content-Type", "application/json")
	}
	if c.origin {
		req.Header.Set("Origin", appOrigin)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.session})
	}
	if c.device != "" {
		req.AddCookie(&http.Cookie{Name: deviceCookie, Value: c.device})
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, c.method+" "+c.path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return answer{r.StatusCode, b, r.Header, r.Cookies()}
}

func (a answer) cookie(name string) *http.Cookie {
	for _, c := range a.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (a answer) field(t *testing.T, name string) (string, bool) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(a.body, &m); err != nil {
		t.Fatalf("WRONG SHAPE — the body is not JSON: %d %s", a.status, a.body)
	}
	v, ok := m[name]
	s, _ := v.(string)
	return s, ok
}

func (a answer) code(t *testing.T) string {
	t.Helper()
	c, _ := a.field(t, "code")
	return c
}

// browser is a signed-in browser: its session cookie and CSRF token.
type browser struct {
	session, csrf, device string
	signer                signer
}

// signInBrowser signs p in from the app's page on its trusted device (the
// device cookie), and returns what the browser holds afterwards.
func signInBrowser(t *testing.T, srv *httptest.Server, p signer) (browser, answer) {
	t.Helper()
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: p.device,
		body: map[string]string{"credential": p.number, "pin": signPIN}})
	if a.status != http.StatusOK {
		t.Fatalf("REFUSED — a browser's sign-in on a trusted device: %d %s", a.status, a.body)
	}
	c := a.cookie(sessionCookie)
	if c == nil || c.Value == "" {
		t.Fatalf("NO COOKIE — a browser's sign-in set no %s: headers %v, body %s", sessionCookie, a.header.Values("Set-Cookie"), a.body)
	}
	csrf, _ := a.field(t, "csrfToken")
	if csrf == "" {
		t.Fatalf("NO TOKEN — a browser's sign-in body carries no csrfToken: %s", a.body)
	}
	return browser{session: c.Value, csrf: csrf, device: p.device, signer: p}, a
}

func newBrowserMember(t *testing.T, post string) (*httptest.Server, *fakeSMS, browser) {
	t.Helper()
	srv, sms := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", post, post != "")
	b, _ := signInBrowser(t, srv, p)
	return srv, sms, b
}

// stepUp: a state-changing POST with an effect to look for (a minted proof).
func stepUpCall(b browser) call {
	return call{method: http.MethodPost, path: "/auth/pin-proofs", origin: true, session: b.session, csrf: b.csrf,
		body: map[string]string{"pin": signPIN, "act": "contribution_record"}}
}

func proofsOf(t *testing.T, user string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM auth_proofs WHERE user_id = $1`, user).Scan(&n), "proofs")
	return n
}

func failedAttemptsOf(t *testing.T, user string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1 AND NOT succeeded`, user).Scan(&n), "attempts")
	return n
}

func meWith(t *testing.T, srv *httptest.Server, c call) answer {
	t.Helper()
	c.method, c.path = http.MethodGet, "/me"
	return send(t, srv, c)
}

func wantRefused(t *testing.T, a answer, status int, code, what string) {
	t.Helper()
	if a.status != status || a.code(t) != code {
		t.Fatalf("ACCEPTED — %s: %d %s; want %d %s", what, a.status, a.body, status, code)
	}
}

// =============================================================================
// THE CSRF CHECK (piece 5)
// =============================================================================

func TestApiSession01_TheRightTokenLetsACookieSessionPost(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	if a := send(t, srv, stepUpCall(b)); a.status != http.StatusCreated {
		t.Fatalf("REFUSED — a cookie session's POST with its own CSRF token: %d %s; want 201", a.status, a.body)
	}
}

// Refused BEFORE the handler: its effect is absent, not merely its answer.
func TestApiSession02_ACookiePostWithoutTheTokenIsRefusedBeforeItsEffect(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	c := stepUpCall(b)
	c.csrf = ""
	before := proofsOf(t, b.signer.user)
	wantRefused(t, send(t, srv, c), http.StatusForbidden, "csrf_failed", "a cookie session's POST with no CSRF token")
	if after := proofsOf(t, b.signer.user); after != before {
		t.Fatalf("EFFECT — refused with 403, yet a proof was minted (%d → %d): the check ran after the handler", before, after)
	}
}

func TestApiSession03_ACookiePostWithAWrongTokenIsRefused(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	c := stepUpCall(b)
	c.csrf = randHex()
	wantRefused(t, send(t, srv, c), http.StatusForbidden, "csrf_failed", "a cookie session's POST with an invented CSRF token")
}

// The token is bound to ITS session: one lifted from another live session,
// even the same person's, is refused.
func TestApiSession04_AnotherSessionsTokenIsRefused(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	other, _ := signInBrowser(t, srv, b.signer) // a second live session, the same person's
	c := stepUpCall(b)
	c.csrf = other.csrf
	wantRefused(t, send(t, srv, c), http.StatusForbidden, "csrf_failed", "session A's cookie with session B's CSRF token")
}

// A cross-site page can neither sign a member out nor lock them out by
// failing on purpose.
func TestApiSession05_CsrfFailuresNeitherEndTheSessionNorCountTowardTheLockout(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	before := failedAttemptsOf(t, b.signer.user)
	for i := 0; i < 6; i++ {
		c := stepUpCall(b)
		c.csrf = randHex()
		send(t, srv, c)
	}
	if a := meWith(t, srv, call{origin: true, session: b.session}); a.status != http.StatusOK {
		t.Fatalf("SIGNED OUT — after six failed CSRF checks the session reads %d %s; a failure must end nothing", a.status, a.body)
	}
	if after := failedAttemptsOf(t, b.signer.user); after != before {
		t.Fatalf("COUNTED — six failed CSRF checks added %d failed sign-in attempts; a failure must count against nothing", after-before)
	}
}

func TestApiSession06_ACookieGetNeedsNoToken(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	if a := meWith(t, srv, call{origin: true, session: b.session}); a.status != http.StatusOK {
		t.Fatalf("REFUSED — GET /me with the session cookie and no CSRF token: %d %s; a GET changes nothing", a.status, a.body)
	}
}

// The reload: /me gives a cookie session its token again, the same one;
// a Bearer session has no use for one and is given none.
func TestApiSession07_MeGivesACookieSessionItsTokenAndABearerSessionNone(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	a := meWith(t, srv, call{origin: true, session: b.session})
	if got, _ := a.field(t, "csrfToken"); got != b.csrf {
		t.Fatalf("LOST ON RELOAD — /me gives the cookie session csrfToken %q; sign-in gave %q", got, b.csrf)
	}
	bearer := signIn(t, b.signer.user)
	a = meWith(t, srv, call{bearer: bearer})
	if a.status != http.StatusOK {
		t.Fatalf("FIXTURE: a Bearer session's /me: %d %s", a.status, a.body)
	}
	if got, present := a.field(t, "csrfToken"); present {
		t.Fatalf("EXPOSED — a Bearer session's /me carries csrfToken %q; it has no use for one", got)
	}
}

func TestApiSession08_SignOutWithoutTheTokenIsRefused(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	wantRefused(t, send(t, srv, call{method: http.MethodPost, path: "/auth/sign-out", origin: true, session: b.session}),
		http.StatusForbidden, "csrf_failed", "a cookie session's sign-out with no CSRF token")
	if a := meWith(t, srv, call{origin: true, session: b.session}); a.status != http.StatusOK {
		t.Fatalf("SIGNED OUT — a refused sign-out ended the session: /me %d", a.status)
	}
}

// =============================================================================
// CREDENTIALS AND COOKIES (piece 6)
// =============================================================================

// Each attribute asserted on its own, so a missing one is named.
func TestApiSession09_ABrowserSignInSetsTheSessionCookieNeverABodyToken(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	_, a := signInBrowser(t, srv, p)
	c := a.cookie(sessionCookie)
	if !c.HttpOnly {
		t.Fatalf("READABLE — the session cookie is not HttpOnly: a script on the page can read it")
	}
	if !c.Secure {
		t.Fatalf("INSECURE — the session cookie is not Secure")
	}
	// Lax, ruled 2026-10-29 on the evidence available (Chromium and Firefox
	// passed Strict; the WebKit run was never completed): never a posture that
	// differs by browser. The CSRF token does the work; SameSite reinforces it.
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("WRONG SAMESITE — the session cookie's SameSite is %s; want Lax", sameSiteName(c.SameSite))
	}
	if c.Path != "/" {
		t.Fatalf("WRONG PATH — the session cookie's Path is %q; want /", c.Path)
	}
	if c.Domain != "" {
		t.Fatalf("DOMAIN — the session cookie names Domain %q; a __Host- cookie names none", c.Domain)
	}
	if tok, present := a.field(t, "sessionToken"); present {
		t.Fatalf("EXPOSED — a browser's sign-in body carries sessionToken %q: the page holds its session in script again", tok)
	}
}

func TestApiSession10_ANonBrowserSignInIsUnchanged(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "The probe", "", false)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in",
		body: map[string]string{"credential": p.number, "pin": signPIN, "deviceToken": p.device}})
	if tok, _ := a.field(t, "sessionToken"); a.status != http.StatusOK || tok == "" {
		t.Fatalf("CHANGED — a non-browser sign-in: %d %s; want 200 with sessionToken", a.status, a.body)
	}
	if c := a.cookie(sessionCookie); c != nil {
		t.Fatalf("COOKIE — a non-browser sign-in set %s", sessionCookie)
	}
	if csrf, present := a.field(t, "csrfToken"); present {
		t.Fatalf("EXPOSED — a non-browser sign-in carries csrfToken %q; it has no use for one", csrf)
	}
}

// The cookie lives as long as its session, read from the session itself:
// 30 days for a member, 8 hours for an officer, never a number written here.
func TestApiSession11_TheSessionCookieLivesAsLongAsItsSession(t *testing.T) {
	for _, post := range []string{"", "treasurer"} {
		srv, _ := sessionServer(t)
		p := newSigner(t, newSociety(t), "Holder", post, post != "")
		b, a := signInBrowser(t, srv, p)
		var left float64
		fixture(t, db.QueryRow(bg(), `SELECT extract(epoch FROM started_at + CASE WHEN privileged OR person_holds_post(user_id)
			THEN interval '8 hours' ELSE interval '30 days' END - now()) FROM auth_sessions WHERE token_digest = $1`, auth.TokenDigest(b.session)).Scan(&left), "the session's life")
		got := float64(a.cookie(sessionCookie).MaxAge)
		if got < left-120 || got > left+120 {
			who := "a member"
			if post != "" {
				who = "an officer"
			}
			t.Fatalf("WRONG LIFE — %s's session cookie has Max-Age %.0f s; the session has %.0f s left", who, got, left)
		}
	}
}

func TestApiSession12_TheCookieAuthenticates(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	a := meWith(t, srv, call{origin: true, session: b.session})
	if id, _ := a.field(t, "userId"); a.status != http.StatusOK || id != b.signer.user {
		t.Fatalf("REFUSED — GET /me with only the session cookie: %d %s; want 200 as %s", a.status, a.body, b.signer.user)
	}
}

// Not a boundary: enforcement that a page never holds a token in script.
func TestApiSession13_BearerFromABrowserIsRefused(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A page holding a token", "", false)
	tok := signIn(t, p.user)
	before := proofsOf(t, p.user)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/pin-proofs", origin: true, bearer: tok,
		body: map[string]string{"pin": signPIN, "act": "contribution_record"}})
	wantRefused(t, a, http.StatusUnauthorized, "bearer_from_browser", "a Bearer header from a page (an Origin)")
	if after := proofsOf(t, p.user); after != before {
		t.Fatalf("EFFECT — refused, yet a proof was minted")
	}
}

// One request, one identity: never a rule about which wins.
func TestApiSession14_ACookieAndABearerTogetherAreRefused(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	bearer := signIn(t, b.signer.user)
	for _, c := range []struct{ what, session, bearer string }{
		{"a valid cookie and an invented Bearer", b.session, randHex()},
		{"an invented cookie and a valid Bearer", randHex(), bearer},
		{"two valid sessions of the same person", b.session, bearer},
		{"the same token twice", b.session, b.session},
	} {
		wantRefused(t, meWith(t, srv, call{session: c.session, bearer: c.bearer}), http.StatusBadRequest, "ambiguous_credentials", c.what)
	}
}

// The probe's path: no Origin, a Bearer header, no CSRF token.
func TestApiSession15_ANonBrowserBearerPostsWithoutAToken(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "The probe", "", false)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/pin-proofs", bearer: signIn(t, p.user),
		body: map[string]string{"pin": signPIN, "act": "contribution_record"}})
	if a.status != http.StatusCreated {
		t.Fatalf("REFUSED — a non-browser Bearer POST with no CSRF token: %d %s; want 201 (CSRF is a browser's)", a.status, a.body)
	}
}

func sameSiteName(s http.SameSite) string {
	return map[http.SameSite]string{http.SameSiteDefaultMode: "unset", http.SameSiteLaxMode: "Lax",
		http.SameSiteStrictMode: "Strict", http.SameSiteNoneMode: "None"}[s]
}

func cleared(a answer) bool {
	c := a.cookie(sessionCookie)
	return c != nil && c.MaxAge < 0
}

func TestApiSession16_AnEndedSessionsCookieIsClearedOnTheRefusal(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(b.session)), "end the session")
	a := meWith(t, srv, call{origin: true, session: b.session})
	if a.status != http.StatusUnauthorized {
		t.Fatalf("ACCEPTED — an ended session's cookie: %d", a.status)
	}
	if !cleared(a) {
		t.Fatalf("KEPT — the 401 to an ended session's cookie does not clear it (Set-Cookie %v)", a.header.Values("Set-Cookie"))
	}
}

func TestApiSession17_AnInventedCookieIsClearedOnTheRefusal(t *testing.T) {
	srv, _ := sessionServer(t)
	a := meWith(t, srv, call{origin: true, session: randHex()})
	if a.status != http.StatusUnauthorized || !cleared(a) {
		t.Fatalf("KEPT — an invented session cookie: %d, Set-Cookie %v; want 401 clearing it", a.status, a.header.Values("Set-Cookie"))
	}
}

func preflightFrom(t *testing.T, srv *httptest.Server, origin string) answer {
	t.Helper()
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/auth/pin-proofs", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type,x-csrf-token")
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "preflight")
	r.Body.Close()
	return answer{status: r.StatusCode, header: r.Header}
}

func TestApiSession18_AnAllowedOriginMayCarryCredentials(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	pf := preflightFrom(t, srv, appOrigin)
	if pf.header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("NO CREDENTIALS — the allowed origin's preflight lacks Access-Control-Allow-Credentials: true; the browser drops the cookie")
	}
	if !listsAll(pf.header.Get("Access-Control-Allow-Headers"), "X-CSRF-Token") {
		t.Fatalf("BLOCKED — the preflight does not allow X-CSRF-Token: %q", pf.header.Get("Access-Control-Allow-Headers"))
	}
	if a := meWith(t, srv, call{origin: true, session: b.session}); a.header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("NO CREDENTIALS — the allowed origin's real answer lacks Access-Control-Allow-Credentials: true")
	}
}

func TestApiSession19_AnUnlistedOriginGetsNoCredentials(t *testing.T) {
	srv, _ := sessionServer(t)
	pf := preflightFrom(t, srv, "http://evil.example")
	if v := pf.header.Get("Access-Control-Allow-Credentials"); v != "" {
		t.Fatalf("CREDENTIALS — an unlisted origin's preflight carries Access-Control-Allow-Credentials %q", v)
	}
}

// =============================================================================
// THE DEVICE, AND SIGN-OUT (piece 7)
// =============================================================================

// A new device, confirmed by the code, from the app's page: the device goes
// into its cookie, never into the body.
func TestApiSession20_ANewDeviceGetsADeviceCookieNeverABodyToken(t *testing.T) {
	srv, sms := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on a new phone", "", false)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, body: map[string]string{"credential": p.number, "pin": signPIN}})
	ch, _ := a.field(t, "challenge")
	if out, _ := a.field(t, "outcome"); out != "code_sent" || ch == "" {
		t.Fatalf("FIXTURE: a new device's sign-in did not send a code: %d %s", a.status, a.body)
	}
	a = send(t, srv, call{method: http.MethodPost, path: "/auth/codes/verify", origin: true, body: map[string]string{"challenge": ch, "code": sms.lastCode(t)}})
	c := a.cookie(deviceCookie)
	if a.status != http.StatusOK || c == nil || c.Value == "" {
		t.Fatalf("NO DEVICE COOKIE — a browser's code-verify: %d, Set-Cookie %v", a.status, a.header.Values("Set-Cookie"))
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Domain != "" {
		t.Fatalf("WRONG ATTRIBUTES — the device cookie: HttpOnly %v, Secure %v, SameSite %s, Domain %q", c.HttpOnly, c.Secure, sameSiteName(c.SameSite), c.Domain)
	}
	if c.Path != "/auth/" {
		t.Fatalf("WRONG PATH — the device cookie's Path is %q; want /auth/, so it rides only on sign-in", c.Path)
	}
	if c.MaxAge < deviceMaxAge-60 {
		t.Fatalf("SHORT-LIVED — the device cookie's Max-Age is %d s; want 400 days, or the phone forgets it is known", c.MaxAge)
	}
	if tok, present := a.field(t, "deviceToken"); present {
		t.Fatalf("EXPOSED — a browser's code-verify body carries deviceToken %q", tok)
	}
}

func TestApiSession21_ANonBrowserVerifyIsUnchanged(t *testing.T) {
	srv, sms := sessionServer(t)
	p := newSigner(t, newSociety(t), "The probe on a new device", "", false)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", body: map[string]string{"credential": p.number, "pin": signPIN}})
	ch, _ := a.field(t, "challenge")
	a = send(t, srv, call{method: http.MethodPost, path: "/auth/codes/verify", body: map[string]string{"challenge": ch, "code": sms.lastCode(t)}})
	if tok, _ := a.field(t, "deviceToken"); a.status != http.StatusOK || tok == "" {
		t.Fatalf("CHANGED — a non-browser code-verify: %d %s; want 200 with deviceToken", a.status, a.body)
	}
	if a.cookie(deviceCookie) != nil {
		t.Fatalf("COOKIE — a non-browser code-verify set %s", deviceCookie)
	}
}

func TestApiSession22_TheDeviceCookieSignsInAndIsRenewed(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	_, a := signInBrowser(t, srv, p) // the PIN alone: no code was needed
	c := a.cookie(deviceCookie)
	if c == nil || c.Value != p.device || c.MaxAge < deviceMaxAge-60 {
		t.Fatalf("NOT RENEWED — a sign-in with the device cookie: Set-Cookie %v; want %s again, 400 days", a.header.Values("Set-Cookie"), deviceCookie)
	}
}

// Refused before the PIN is checked: nothing counts against the lockout.
func TestApiSession23_ADeviceTokenInABrowsersBodyIsRefusedBeforeThePin(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A page holding a device token", "", false)
	before := failedAttemptsOf(t, p.user)
	var all int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1`, p.user).Scan(&all), "attempts")
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true,
		body: map[string]string{"credential": p.number, "pin": "0000", "deviceToken": p.device}})
	wantRefused(t, a, http.StatusBadRequest, "device_token_from_browser", "a browser's sign-in with the device token in its body")
	var allAfter int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1`, p.user).Scan(&allAfter), "attempts")
	if failedAttemptsOf(t, p.user) != before || allAfter != all {
		t.Fatalf("COUNTED — the refused request was checked as a sign-in (attempts %d → %d)", all, allAfter)
	}
}

// The device cookie is not a session: alone, it is 401, not partly signed in.
func TestApiSession24_TheDeviceCookieIsNotASession(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	if a := meWith(t, srv, call{origin: true, device: b.device}); a.status != http.StatusUnauthorized || a.code(t) != "unauthenticated" {
		t.Fatalf("AUTHENTICATED — GET /me with only the device cookie: %d %s; want 401 unauthenticated", a.status, a.body)
	}
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/pin-proofs", origin: true, device: b.device,
		body: map[string]string{"pin": signPIN, "act": "contribution_record"}})
	if a.status != http.StatusUnauthorized || a.code(t) != "unauthenticated" {
		t.Fatalf("AUTHENTICATED — a POST with only the device cookie: %d %s; want 401 unauthenticated", a.status, a.body)
	}
}

func signOut(t *testing.T, srv *httptest.Server, b browser) answer {
	t.Helper()
	return send(t, srv, call{method: http.MethodPost, path: "/auth/sign-out", origin: true, session: b.session, csrf: b.csrf, device: b.device})
}

// Both halves: the session is ended (its old value reaches nothing) AND its
// cookie is cleared. A cleared cookie on a live session looks exactly like a
// sign-out from the browser, and leaves anyone holding the value signed in.
func TestApiSession25_SignOutEndsTheSessionAndClearsItsCookie(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	a := signOut(t, srv, b)
	if a.status != http.StatusNoContent {
		t.Fatalf("REFUSED — sign-out with the cookie and the token: %d %s", a.status, a.body)
	}
	if !cleared(a) {
		t.Fatalf("KEPT — sign-out did not clear %s (Set-Cookie %v)", sessionCookie, a.header.Values("Set-Cookie"))
	}
	if m := meWith(t, srv, call{session: b.session}); m.status != http.StatusUnauthorized {
		t.Fatalf("STILL SIGNED IN — after sign-out the old cookie value reads /me %d: the session was never ended", m.status)
	}
}

// The device stays known: a member signing out of their own phone signs back
// in with the PIN, no code, no officer.
func TestApiSession26_SignOutLeavesTheDeviceKnown(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	a := signOut(t, srv, b)
	if c := a.cookie(deviceCookie); c != nil && c.MaxAge < 0 {
		t.Fatalf("FORGOTTEN — sign-out cleared %s", deviceCookie)
	}
	again := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: b.device,
		body: map[string]string{"credential": b.signer.number, "pin": signPIN}})
	if out, _ := again.field(t, "outcome"); out != "signed_in" {
		t.Fatalf("FORGOTTEN — after signing out of their own phone, the member signing back in with PIN and device cookie gets %q (%s): the device is no longer known", out, again.body)
	}
}

// The stolen-phone path: a session ended FROM ANOTHER session revokes its
// device. No endpoint does this yet (a recorded gap), so it is done here the
// way the schema allows; what is tested is what the cookie path does after.
func TestApiSession27_ADeviceRevokedFromAnotherSessionGoesToTheNewDevicePath(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	elsewhere := signIn(t, b.signer.user)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(),
		ended_by_session = (SELECT id FROM auth_sessions WHERE token_digest = $2) WHERE token_digest = $1`,
		auth.TokenDigest(b.session), auth.TokenDigest(elsewhere)), "end the phone's session from elsewhere")
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: b.device,
		body: map[string]string{"credential": b.signer.number, "pin": signPIN}})
	if out, _ := a.field(t, "outcome"); out == "signed_in" {
		t.Fatalf("STILL KNOWN — the phone whose session was ended from elsewhere signed straight in with its device cookie")
	}
}

// =============================================================================
// THE TOKEN IS DERIVED AND NOTHING STORES IT (2026-10-24)
// =============================================================================

// Never the session token, nor its plain digest: either would be the first
// factor restated, and the second would be computable by anyone holding the
// cookie, with no key at all.
func TestApiSession28_TheCsrfTokenIsNeitherTheSessionTokenNorItsDigest(t *testing.T) {
	_, _, b := newBrowserMember(t, "")
	if b.csrf == b.session {
		t.Fatalf("RESTATED — the CSRF token is the session token itself")
	}
	if b.csrf == auth.TokenDigest(b.session) {
		t.Fatalf("UNKEYED — the CSRF token is the session token's plain digest: anyone with the cookie can compute it")
	}
}

// The token depends on a key only the server holds: the same session, under
// two servers' keys, has two different tokens.
func TestApiSession29_TheCsrfTokenDependsOnTheServersKey(t *testing.T) {
	srvA, _, b := newBrowserMember(t, "")
	srvB, _ := sessionServerKey(t, []byte("a-different-csrf-key-not-a-secret-987654"))
	ta, _ := meWith(t, srvA, call{origin: true, session: b.session}).field(t, "csrfToken")
	tb, _ := meWith(t, srvB, call{origin: true, session: b.session}).field(t, "csrfToken")
	if ta == "" || tb == "" {
		t.Fatalf("NO TOKEN — /me under key A gives %q, under key B %q", ta, tb)
	}
	if ta == tb {
		t.Fatalf("UNKEYED — the same session has the same CSRF token under two different server keys (%q): the token does not depend on the server's key", ta)
	}
}

// ROTATION, as the contract says (2026-10-24): a token minted under the old
// key is refused; the same session recovers by re-reading /me under the new
// one, with no sign-in. What someone will rely on during an incident.
func TestApiSession30_ARotatedKeyRefusesTheOldTokenAndThePageRecoversThroughMe(t *testing.T) {
	_, _, b := newBrowserMember(t, "")
	rotated, _ := sessionServerKey(t, []byte("the-rotated-csrf-key-not-a-secret-24680"))
	wantRefused(t, send(t, rotated, stepUpCall(b)), http.StatusForbidden, "csrf_failed", "a token minted under the old key, after the rotation")
	fresh, _ := meWith(t, rotated, call{origin: true, session: b.session}).field(t, "csrfToken")
	if fresh == "" || fresh == b.csrf {
		t.Fatalf("NOT RECOVERABLE — after the rotation /me gives %q (the old token was %q)", fresh, b.csrf)
	}
	c := stepUpCall(b)
	c.csrf = fresh
	if a := send(t, rotated, c); a.status != http.StatusCreated {
		t.Fatalf("NOT RECOVERED — the same session with the token /me gave after the rotation: %d %s; want 201, no sign-in", a.status, a.body)
	}
}

// No key is a refusal to start, never a generated default: a key made at each
// start would silently log every page out on every restart.
func TestApiSession31_WithNoCsrfKeyTheServerRefusesToStart(t *testing.T) {
	for _, key := range [][]byte{nil, {}} {
		if _, err := startServer(t, &fakeSMS{}, key); err == nil {
			t.Fatalf("STARTED — the server started with no CSRF key (%v); it must refuse", key)
		}
	}
}

// The configuration: ARGON_CSRF_KEY is required, hex, at least 32 bytes, with
// no default.
func TestApiSession32_TheConfigurationRequiresTheCsrfKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://nobody@127.0.0.1:1/none")
	t.Setenv("AUTH_DATABASE_URL", "")
	t.Setenv("AUTH_CODE_KEY", "")
	for _, c := range []struct{ what, value string }{
		{"unset", ""},
		{"not hex", "this-is-not-hex-at-all-and-is-long-enough-0123456789abcdef"},
		{"16 bytes", "00112233445566778899aabbccddeeff"},
	} {
		t.Setenv("ARGON_CSRF_KEY", c.value)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "ARGON_CSRF_KEY") {
			t.Fatalf("STARTED — ARGON_CSRF_KEY %s: the configuration loaded (%v); it must refuse, naming ARGON_CSRF_KEY", c.what, err)
		}
	}
	t.Setenv("ARGON_CSRF_KEY", strings.Repeat("ab", 32))
	cfg, err := loadConfig()
	if err != nil || len(cfg.CSRFKey) != 32 {
		t.Fatalf("REFUSED — a 32-byte ARGON_CSRF_KEY: %v (key %d bytes)", err, len(cfg.CSRFKey))
	}
}

// =============================================================================
// A BODY IS JSON, OR IT IS REFUSED (ruled 2026-10-31, LEDGER_CONTRACT.md §12)
// =============================================================================
//
// The app and the API share a domain with the company's other platforms, so a
// page on any of them is SAME-SITE: the browser sends it the Lax session
// cookie. A form can post text/plain, application/x-www-form-urlencoded or
// multipart/form-data with NO PREFLIGHT, and the API decoded any body as JSON
// whatever its type. Sign-in carries no CSRF token (there is no session yet),
// so such a page could sign a member's browser into an account the attacker
// controls; the member then sees a working app and pays into someone else's
// society (login CSRF: session fixation).
//
// application/json cannot be sent cross-origin without a preflight, and the
// preflight passes only for the app's origin. So a request WITH A BODY that is
// not application/json is refused, 415 unsupported_media_type: before
// authentication and before the PIN (nothing counted), inside CORS (the app
// can read the refusal). A request with no body is untouched.

// The three types an HTML form can send, none of which needs a preflight.
var formTypes = []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"}

func TestApiSession33_AFormSignInIsRefusedAndSetsNoCookie(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	for _, ct := range formTypes {
		a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: p.device, ctype: ct,
			body: map[string]string{"credential": p.number, "pin": signPIN}})
		wantRefused(t, a, http.StatusUnsupportedMediaType, "unsupported_media_type", "a sign-in with the RIGHT PIN, sent as "+ct)
		if c := a.cookie(sessionCookie); c != nil && c.Value != "" {
			t.Fatalf("SIGNED IN — a sign-in sent as %s set %s: a form on another subdomain can sign a browser in", ct, sessionCookie)
		}
	}
}

// Refused BEFORE the PIN is checked: a form's wrong PINs count against nothing,
// so another site's page cannot lock a member out either.
func TestApiSession34_AFormSignInIsRefusedBeforeThePinCounts(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	before := failedAttemptsOf(t, p.user)
	for i := 0; i < 6; i++ {
		send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: p.device, ctype: "text/plain",
			body: map[string]string{"credential": p.number, "pin": "000000"}})
	}
	if after := failedAttemptsOf(t, p.user); after != before {
		t.Fatalf("COUNTED — six form sign-ins with a wrong PIN added %d failed attempts; the refusal must come before the PIN", after-before)
	}
}

// Code-verify takes no session either: the same opening, the same refusal.
func TestApiSession35_AFormCodeVerifyIsRefused(t *testing.T) {
	srv, _ := sessionServer(t)
	for _, ct := range formTypes {
		a := send(t, srv, call{method: http.MethodPost, path: "/auth/codes/verify", origin: true, ctype: ct,
			body: map[string]string{"challenge": "00000000-0000-0000-0000-000000000000", "code": "123456"}})
		wantRefused(t, a, http.StatusUnsupportedMediaType, "unsupported_media_type", "a code-verify sent as "+ct)
	}
}

// A cookie session's state change as a form is refused even WITH the right
// CSRF token, and has no effect: the rule is about the request, not the token.
func TestApiSession36_AFormStateChangeIsRefusedEvenWithTheToken(t *testing.T) {
	srv, _, b := newBrowserMember(t, "")
	before := proofsOf(t, b.signer.user)
	for _, ct := range formTypes {
		c := stepUpCall(b)
		c.ctype = ct
		wantRefused(t, send(t, srv, c), http.StatusUnsupportedMediaType, "unsupported_media_type", "a step-up sent as "+ct)
	}
	if after := proofsOf(t, b.signer.user); after != before {
		t.Fatalf("EFFECT — a refused form step-up minted %d proofs", after-before)
	}
}

// A body with no Content-Type at all is not JSON by default.
func TestApiSession37_ABodyWithNoContentTypeIsRefused(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: p.device, ctype: "-",
		body: map[string]string{"credential": p.number, "pin": signPIN}})
	wantRefused(t, a, http.StatusUnsupportedMediaType, "unsupported_media_type", "a sign-in body with no Content-Type")
}

// What must still work: JSON with parameters or in another case (media types
// are case-insensitive), and a state change with NO body and no type.
func TestApiSession38_Control_JsonWithParametersAndBodilessPostsAreAccepted(t *testing.T) {
	srv, _ := sessionServer(t)
	p := newSigner(t, newSociety(t), "A member on their phone", "", false)
	for _, ct := range []string{"application/json; charset=utf-8", "Application/JSON"} {
		a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, device: p.device, ctype: ct,
			body: map[string]string{"credential": p.number, "pin": signPIN}})
		if a.status != http.StatusOK {
			t.Fatalf("REFUSED — a sign-in sent as %q: %d %s; that IS application/json", ct, a.status, a.body)
		}
	}
	b, _ := signInBrowser(t, srv, p)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-out", origin: true, session: b.session, csrf: b.csrf, ctype: "-"})
	if a.status != http.StatusNoContent {
		t.Fatalf("REFUSED — a sign-out with no body and no Content-Type: %d %s; there is no body to be JSON", a.status, a.body)
	}
}

// The app can READ the refusal: the API's error format, with its CORS headers.
func TestApiSession39_TheRefusalCarriesTheAppsCorsHeaders(t *testing.T) {
	srv, _ := sessionServer(t)
	a := send(t, srv, call{method: http.MethodPost, path: "/auth/sign-in", origin: true, ctype: "text/plain",
		body: map[string]string{"credential": "x", "pin": "000000"}})
	wantRefused(t, a, http.StatusUnsupportedMediaType, "unsupported_media_type", "a form sign-in")
	if got := a.header.Get("Access-Control-Allow-Origin"); got != appOrigin {
		t.Fatalf("UNREADABLE — the 415 carries Access-Control-Allow-Origin %q; the app's page cannot read it and shows a network error", got)
	}
}
