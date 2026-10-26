//go:build tier1

// Acceptance tests for browser access (CORS) — written before the code, for
// review in pieces. Every TestApiCors* name is here.
//
// Found by the frontend's first real connection (2026-10-22): the server sent
// no CORS headers, and a preflight from http://localhost:3000 answered 401,
// because it went through the auth middleware. A preflight carries no
// credentials BY DESIGN, so any preflight that meets authentication fails:
// correct headers would not have saved it. Decided 2026-10-22:
//
//   - A preflight is answered BEFORE authentication, without credentials.
//   - The allowed origins are CONFIGURED (ARGON_ALLOWED_ORIGINS), matched
//     EXACTLY, and anything not listed is refused. Never a wildcard: this API
//     takes PINs and moves money, and a permissive default is the kind of
//     thing nobody revisits. Nothing configured means no origin is allowed.
//   - A real request from an allowed origin succeeds and says so
//     (Access-Control-Allow-Origin: that origin; Vary: Origin).
package tier1

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

const allowedOrigin = "http://localhost:3000"

// corsServer: the API allowing `origins`, the real one or the fake (with its
// break) as for every API suite.
func corsServer(t *testing.T, origins ...string) *httptest.Server {
	t.Helper()
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		h = fakeCORS(origins, brk, newFakeAPI(db, brk))
	} else {
		app, authp := rolePools(t, 0)
		svc, err := auth.New(auth.Deps{DB: authp, SMS: &fakeSMS{}, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon})
		fixture(t, err, "the auth service")
		h, err = api.NewServer(api.Deps{DB: app, Auth: svc, AllowedOrigins: origins, CSRFKey: testCSRFKey})
		fixture(t, err, "the API")
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

type corsResp struct {
	status int
	h      http.Header
	body   string
}

// preflight: what a browser sends before a call carrying Authorization: no
// credentials, by design.
func preflight(t *testing.T, srv *httptest.Server, origin, path, method string) corsResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+path, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	return corsDo(t, req)
}

// fromOrigin: a real call, as a browser on `origin` makes it: the session in
// its cookie, never a Bearer header (2026-10-23: a request with an Origin and
// a Bearer header is refused; until then this sent Bearer, which is exactly
// the page-holds-a-token call the cookie removes).
func fromOrigin(t *testing.T, srv *httptest.Server, origin, path, token string) corsResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Origin", origin)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "__Host-argon_session", Value: token})
	}
	return corsDo(t, req)
}

func corsDo(t *testing.T, req *http.Request) corsResp {
	t.Helper()
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, req.Method+" "+req.URL.Path)
	defer r.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return corsResp{r.StatusCode, r.Header, b.String()}
}

func listsAll(header string, want ...string) bool {
	have := map[string]bool{}
	for _, v := range strings.Split(header, ",") {
		have[strings.ToLower(strings.TrimSpace(v))] = true
	}
	for _, w := range want {
		if !have[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// A member with a session, for the real calls.
func corsMember(t *testing.T) (token string) {
	t.Helper()
	s := newSociety(t)
	return signIn(t, s.user("A member of the browser's society"))
}

// =============================================================================
// THE PREFLIGHT: ANSWERED BEFORE AUTHENTICATION
// =============================================================================

// No credentials, as a browser sends it: answered, for the allowed origin,
// naming the methods and the headers a call carries.
func TestApiCors01_APreflightIsAnsweredWithoutCredentials(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	r := preflight(t, srv, allowedOrigin, "/me", http.MethodGet)
	if r.status != http.StatusNoContent || r.h.Get("Access-Control-Allow-Origin") != allowedOrigin {
		t.Fatalf("REFUSED — a preflight from the allowed origin: %d, Allow-Origin %q, body %s; want 204 and %q (a preflight carries no credentials: it is answered before authentication)",
			r.status, r.h.Get("Access-Control-Allow-Origin"), r.body, allowedOrigin)
	}
	if !listsAll(r.h.Get("Access-Control-Allow-Headers"), "Authorization", "Content-Type", "Idempotency-Key") ||
		!listsAll(r.h.Get("Access-Control-Allow-Methods"), "GET", "POST") {
		t.Fatalf("INCOMPLETE — Allow-Headers %q, Allow-Methods %q; want Authorization, Content-Type, Idempotency-Key and GET, POST",
			r.h.Get("Access-Control-Allow-Headers"), r.h.Get("Access-Control-Allow-Methods"))
	}
}

// An origin not listed: refused, and nothing in the answer allows it.
func TestApiCors02_AnUnlistedOriginsPreflightIsRefused(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	r := preflight(t, srv, "http://evil.example", "/me", http.MethodGet)
	if r.status != http.StatusForbidden || r.h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("ACCEPTED — a preflight from an unlisted origin: %d, Allow-Origin %q; want 403 and none", r.status, r.h.Get("Access-Control-Allow-Origin"))
	}
}

// EXACTLY the listed origin: not one that merely begins or ends like it, not
// another port, not another scheme.
func TestApiCors03_OnlyTheExactOrigin(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	for _, o := range []string{"http://localhost:3000.evil.example", "http://evil.localhost:3000", "http://localhost:30000", "https://localhost:3000", "http://localhost:3001"} {
		if r := preflight(t, srv, o, "/me", http.MethodGet); r.h.Get("Access-Control-Allow-Origin") != "" || r.status != http.StatusForbidden {
			t.Fatalf("ACCEPTED — %q, which is not %q: %d, Allow-Origin %q", o, allowedOrigin, r.status, r.h.Get("Access-Control-Allow-Origin"))
		}
	}
}

// Nothing configured: no origin is allowed. Closed by default, never open.
func TestApiCors04_NothingConfiguredAllowsNothing(t *testing.T) {
	srv := corsServer(t)
	r := preflight(t, srv, allowedOrigin, "/me", http.MethodGet)
	if r.status != http.StatusForbidden || r.h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("OPEN BY DEFAULT — with no origin configured, a preflight from %q: %d, Allow-Origin %q; want 403 and none", allowedOrigin, r.status, r.h.Get("Access-Control-Allow-Origin"))
	}
}

// =============================================================================
// THE REAL CALL
// =============================================================================

// From the allowed origin, with a session: it succeeds and says which origin
// may read it (never a wildcard), varying by origin for any cache between.
func TestApiCors05_ARealCallFromTheAllowedOriginSucceeds(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	r := fromOrigin(t, srv, allowedOrigin, "/me", corsMember(t))
	if r.status != http.StatusOK || !strings.Contains(r.body, `"memberships"`) {
		t.Fatalf("FAILED — GET /me from the allowed origin: %d %s", r.status, r.body)
	}
	if r.h.Get("Access-Control-Allow-Origin") != allowedOrigin || !listsAll(r.h.Get("Vary"), "Origin") {
		t.Fatalf("UNREADABLE — Allow-Origin %q, Vary %q; want %q and Origin", r.h.Get("Access-Control-Allow-Origin"), r.h.Get("Vary"), allowedOrigin)
	}
}

// From an unlisted origin: the server still answers (it cannot stop a client
// that is not a browser), but nothing allows a browser page to read it.
func TestApiCors06_AnUnlistedOriginCannotReadACall(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	r := fromOrigin(t, srv, "http://evil.example", "/me", corsMember(t))
	if a := r.h.Get("Access-Control-Allow-Origin"); a != "" {
		t.Fatalf("READABLE — a call from an unlisted origin carries Allow-Origin %q", a)
	}
}

// Authentication is unchanged: CORS answers preflights, never real calls
// without a session.
func TestApiCors07_ARealCallStillNeedsASession(t *testing.T) {
	srv := corsServer(t, allowedOrigin)
	if r := fromOrigin(t, srv, allowedOrigin, "/me", ""); r.status != http.StatusUnauthorized {
		t.Fatalf("ACCEPTED — GET /me from the allowed origin with no session: %d %s; want 401", r.status, r.body)
	}
}
