package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/argon/backend/internal/auth"
)

// THE BROWSER SESSION (LEDGER_CONTRACT.md §12, decided 2026-10-23/24; written
// against test/tier1/tier1_api_session_test.go, reviewed in pieces and
// mutation-tested with apifake_session_test.go before this file existed).
//
// A browser holds its session in an httpOnly cookie and its device in another,
// never a credential in script. A cookie session's state changes carry the
// session's own CSRF token. Clients that are not browsers (no Origin) keep the
// Bearer header, unchanged.
//
// THE CSRF TOKEN is HMAC(the server's key, "argon-csrf:" + the session token):
// given out at sign-in and again by GET /me (the reload), and CHECKED BY
// RECOMPUTING it with the current key. Nothing stores it. Rotating the key
// refuses every live page's token; each page recovers by reloading and
// re-reading /me, no sign-in.

const (
	sessionCookie = "__Host-argon_session"  // Path=/, no Domain: the prefix makes the BROWSER refuse it otherwise
	deviceCookie  = "__Secure-argon_device" // Path=/auth/: rides only on sign-in calls
	deviceMaxAge  = 400 * 24 * 60 * 60      // the browsers' cap; renewed on every sign-in that uses it
)

type viaCookieKey struct{} // the request's session came from the session cookie

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func isBrowser(r *http.Request) bool { return r.Header.Get("Origin") != "" }

// csrfFor: the CSRF token of the session whose token is tok, under this
// server's key. Recomputed every time; never stored.
func (s *server) csrfFor(tok string) string {
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write([]byte("argon-csrf:" + tok))
	return hex.EncodeToString(m.Sum(nil))
}

// The session cookie is Lax (ruled 2026-10-29, the WebKit run never
// completed): one posture for every browser, the CSRF token doing the work.
func setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func clearSessionCookie(w http.ResponseWriter) { setSessionCookie(w, "", -1) } // Max-Age=0

func setDeviceCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: deviceCookie, Value: value, Path: "/auth/", MaxAge: deviceMaxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

// gate: every request, before any handler (reviewed as handler piece 1).
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		hasBearer = hasBearer && bearer != ""
		cookie := cookieValue(r, sessionCookie)

		// 1. One request, one identity: never a rule about which wins.
		if hasBearer && cookie != "" {
			writeProblem(w, http.StatusBadRequest, Problem{Code: "ambiguous_credentials", Message: "one request carries one credential"})
			return
		}
		// 2. A Bearer header from a page. THIS IS ENFORCEMENT OF THE DESIGN, NOT
		// A SECURITY BOUNDARY: a client that is not a browser sends whatever
		// Origin it likes, or none, so a stolen token used from curl is untouched.
		// What it does is make a page that keeps a token in script fail loudly on
		// its first call. Nothing may be built on it as a defence. It comes before
		// the session check, so a valid token held by a page fails too.
		if hasBearer && isBrowser(r) {
			writeProblem(w, http.StatusUnauthorized, Problem{Code: "bearer_from_browser", Message: "a browser signs in with its session cookie, never a token held by the page"})
			return
		}
		if public[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// 3. The session, from the cookie or the Bearer header, checked as before.
		tok, viaCookie := bearer, false
		if cookie != "" {
			tok, viaCookie = cookie, true
		}
		var user string
		if tok != "" {
			if err := s.db.QueryRow(r.Context(),
				`SELECT user_id::text FROM auth_sessions WHERE token_digest = $1 AND session_valid($1)`,
				auth.TokenDigest(tok)).Scan(&user); err != nil {
				user = ""
			}
		}
		if user == "" {
			if viaCookie {
				clearSessionCookie(w) // a dead cookie is useless; it should not keep being sent
			}
			writeProblem(w, http.StatusUnauthorized, Problem{Code: "unauthenticated", Message: "sign in again"})
			return
		}

		// 4. A cookie session's state change carries its own CSRF token. A
		// failure writes NOTHING: no session ended, no attempt counted, so a
		// cross-site page can neither sign a member out nor lock them out.
		// hmac.Equal, never ==: a constant-time comparison does not leak, by how
		// long it took, how much of a guess was right.
		if viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrfFor(tok))) {
				writeProblem(w, http.StatusForbidden, Problem{Code: "csrf_failed", Message: "this request did not come from the page that signed in"})
				return
			}
		}

		ctx := context.WithValue(r.Context(), callerKey{}, user)
		ctx = context.WithValue(ctx, tokenKey{}, tok)
		ctx = context.WithValue(ctx, viaCookieKey{}, viaCookie)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// browserSignIn: sign-in, code-verify and sign-out for a browser (reviewed as
// handler piece 2). The handlers have one path and never learn a browser
// exists: the device cookie goes IN as the request's device token, and the
// session and device leave in cookies instead of the body.
func (s *server) browserSignIn(f StrictHandlerFunc, operationID string) StrictHandlerFunc {
	switch operationID {
	case "PostSignIn", "PostVerifyCode", "PostSignOut":
	default:
		return f
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		if operationID == "PostSignOut" {
			res, err := f(ctx, w, r, request)
			// Cleared only once the handler has ended the session: the server's
			// half is the authoritative one. The device is left as it was.
			if _, ended := res.(PostSignOut204Response); ended && err == nil {
				if viaCookie, _ := ctx.Value(viaCookieKey{}).(bool); viaCookie {
					clearSessionCookie(w)
				}
			}
			return res, err
		}
		if !isBrowser(r) {
			return f(ctx, w, r, request) // a non-browser's sign-in is unchanged
		}
		dev := cookieValue(r, deviceCookie)
		if req, ok := request.(PostSignInRequestObject); ok && req.Body != nil {
			// Refused before the PIN is checked: nothing counts against the lockout.
			if req.Body.DeviceToken != nil {
				return PostSignIn400JSONResponse{Code: "device_token_from_browser", Message: "a browser's device is its device cookie, never a token in the body"}, nil
			}
			if dev != "" {
				body := *req.Body
				body.DeviceToken = &dev
				req.Body = &body
				request = req
			}
		}
		res, err := f(ctx, w, r, request)
		if err != nil {
			return res, err
		}
		switch out := res.(type) {
		case PostSignIn200JSONResponse:
			r := SignInResult(out)
			s.toCookies(w, &r, dev)
			return PostSignIn200JSONResponse(r), nil
		case PostVerifyCode200JSONResponse:
			r := SignInResult(out)
			s.toCookies(w, &r, dev)
			return PostVerifyCode200JSONResponse(r), nil
		}
		return res, nil
	}
}

// toCookies: a browser's signed_in result. The session and any newly trusted
// device go into cookies; the body carries the CSRF token instead. Any other
// outcome (code_sent, see_officer) sets nothing: a cookie before a session
// exists would be a credential for nothing.
func (s *server) toCookies(w http.ResponseWriter, r *SignInResult, presentedDevice string) {
	if r.Outcome != SignedIn || r.SessionToken == nil {
		return
	}
	maxAge := 0
	if r.ExpiresAt != nil {
		maxAge = int(time.Until(*r.ExpiresAt).Seconds()) // the session's own life: 8 h an officer, 30 d a member
	}
	setSessionCookie(w, *r.SessionToken, maxAge)
	switch {
	case r.DeviceToken != nil:
		setDeviceCookie(w, *r.DeviceToken) // a device trusted just now
	case presentedDevice != "":
		setDeviceCookie(w, presentedDevice) // the renewal: 400 days from each use, not from the first
	}
	csrf := s.csrfFor(*r.SessionToken)
	r.CsrfToken = &csrf
	r.SessionToken, r.DeviceToken = nil, nil
}
