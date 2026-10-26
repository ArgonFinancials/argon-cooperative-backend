//go:build tier1

// THE BROWSER SESSION in the API mutation vehicle — test code, NOT the API
// (decided 2026-10-23, LEDGER_CONTRACT.md §12). It sits in front of the
// fake's own authentication and does what the real server must:
//
//   - a browser (a request with an Origin) holds its session in
//     __Host-argon_session and its device in __Secure-argon_device
//     (Path=/auth/), never a credential in a body or in script;
//   - a cookie session's state changes carry X-CSRF-Token, the session's
//     own, checked before anything runs; a failure ends nothing and counts
//     against nothing;
//   - a Bearer header with an Origin, or with a session cookie, is refused;
//   - sign-out ends the session and clears its cookie, the device untouched;
//   - any 401 to a request that carried a session cookie clears it.
//
// THE CSRF TOKEN is derived, and NOTHING STORES IT (decided 2026-10-24):
// HMAC(the server's key, "argon-csrf:" + the session token). The server gives
// it again from the cookie (GET /me, after a reload) and checks a presented one
// by recomputing it with its CURRENT key. Rotating the key invalidates every
// live page's token; each page recovers by reloading and re-reading /me, no
// sign-in. Only the server holds the key, configured, never generated: a key
// made at each start would log every page out on every restart.
//
// Breaks (ARGON_TEST_API_FAKE=<break>):
//
//	csrf_unchecked              a cookie session's POST needs no token
//	csrf_per_user               the token is the person's, not the session's: another session of theirs shares it
//	csrf_is_session_digest      the token is the session token's own digest: no key, the first factor restated
//	csrf_unkeyed                the token is keyed by a constant, not the server's configured key
//	csrf_first_key_kept         the key a server first started with is kept: a rotation changes nothing
//	csrf_key_generated          a server started with no key makes one up instead of refusing to start
//	csrf_key_default            the configuration supplies a default key when ARGON_CSRF_KEY is unset
//	csrf_after_handler          the token is checked after the request has had its effect
//	csrf_failure_ends_session   a failed check ends the session (a cross-site page signs the member out)
//	csrf_failure_counts_as_sign_in_failure  a failed check counts toward the lockout
//	csrf_on_get                 (over-strict) a GET needs the token too
//	me_omits_token              /me does not give a cookie session its token (lost on reload)
//	signout_without_csrf        sign-out needs no token
//	token_in_browser_body       a browser's sign-in body still carries the session token
//	cookie_for_non_browser      a non-browser's sign-in gets a cookie as well
//	cookie_lax                  the session cookie is SameSite=Lax
//	cookie_not_httponly         the session cookie is readable by script
//	cookie_with_domain          the session cookie names a Domain
//	max_age_fixed_30_days       the session cookie lives 30 days whoever holds it
//	bearer_with_origin_ok       a Bearer header with an Origin is accepted
//	cookie_wins                 a cookie and a Bearer header together: the cookie is used
//	bearer_wins                 a cookie and a Bearer header together: the Bearer is used
//	bearer_needs_csrf           (over-strict) a Bearer POST needs a CSRF token
//	dead_cookie_kept            a 401 leaves a dead session cookie in place
//	device_token_in_browser_body  a browser's verify body still carries the device token
//	device_cookie_path_root     the device cookie rides on every request (Path=/)
//	device_cookie_session_length  the device cookie lives as long as a session
//	device_cookie_not_renewed   a sign-in with the device cookie does not renew it
//	device_token_body_ok        a browser may send its device token in the body
//	device_cookie_authenticates the device cookie alone is taken as a session
//	signout_cookie_only         sign-out clears the cookie and leaves the session alive
//	signout_session_only        sign-out ends the session and leaves the cookie
//	signout_clears_device       sign-out clears the device cookie too
//	signout_revokes_device      sign-out revokes the device
//	csrf_never_matches          (over-strict) no token is ever accepted: a cookie session can change nothing
//	cookie_ignored              the session cookie is not read: a browser is never signed in
//	device_cookie_for_non_browser  a non-browser's code-verify gets a device cookie as well
//	revoked_device_cookie_ok    a device cookie is honoured after its device was revoked
package tier1

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
	"github.com/argon/backend/internal/config"
)

const (
	sessionCookie = "__Host-argon_session"
	deviceCookie  = "__Secure-argon_device"
	deviceMaxAge  = 400 * 24 * 3600 // the browsers' cap; renewed on every sign-in that uses it
)

// testCSRFKey: the key test servers are configured with, unless a test sets
// its own (rotation).
var testCSRFKey = []byte("test-csrf-key-not-a-secret-0123456789")

// firstKey: csrf_first_key_kept's memory of the first key any fake started with.
var firstKey []byte

func hmacHex(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// csrfFor: the CSRF token of the session whose token is tok, under this
// server's key, recomputed every time; never stored.
func (f *fakeAPI) csrfFor(ctx context.Context, tok string) string {
	key := f.csrfKey
	switch f.brk {
	case "csrf_is_session_digest":
		return sha(tok)
	case "csrf_unkeyed":
		key = []byte("argon")
	case "csrf_first_key_kept":
		key = firstKey
	case "csrf_per_user":
		var user string
		f.db.QueryRow(ctx, `SELECT user_id::text FROM auth_sessions WHERE token_digest = $1`, sha(tok)).Scan(&user)
		return hmacHex(key, "argon-csrf-user:"+user)
	}
	return hmacHex(key, "argon-csrf:"+tok)
}

// loadConfig: the server's configuration as cmd/api loads it: the real
// loader, or (in a fake run) a reference loader with the same rule on
// ARGON_CSRF_KEY, breakable.
func loadConfig() (config.Config, error) {
	brk, fake := os.LookupEnv("ARGON_TEST_API_FAKE")
	if !fake {
		return config.Load()
	}
	c := config.Config{DatabaseURL: os.Getenv("DATABASE_URL")}
	key := os.Getenv("ARGON_CSRF_KEY")
	if key == "" && brk == "csrf_key_default" {
		key = strings.Repeat("00", 32) // a default anyone can read here
	}
	b, err := hex.DecodeString(key)
	if err != nil || len(b) < 32 {
		return c, errors.New("ARGON_CSRF_KEY must be at least 32 bytes as hex (openssl rand -hex 32)")
	}
	c.CSRFKey = b
	return c, nil
}

// newFakeServer: the fake as cmd/api starts it, with its CSRF key; no key is
// a refusal to start, never a generated default.
func newFakeServer(f *fakeAPI) (http.Handler, error) {
	if len(f.csrfKey) == 0 {
		if f.brk != "csrf_key_generated" {
			return nil, errors.New("no CSRF key: the server refuses to start")
		}
		f.csrfKey = []byte(token())
	}
	return newFakeAPIWith(f), nil
}

type ctxCookie struct{} // the request's session came from the cookie

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func problem(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(api.Problem{Code: code, Message: msg})
}

func (f *fakeAPI) sessionCookieHeader(value string, maxAge int) string {
	c := fmt.Sprintf("%s=%s; Path=/; Max-Age=%d", sessionCookie, value, maxAge)
	if f.brk != "cookie_not_httponly" {
		c += "; HttpOnly"
	}
	c += "; Secure"
	if f.brk == "cookie_lax" {
		c += "; SameSite=Lax"
	} else {
		c += "; SameSite=Strict"
	}
	if f.brk == "cookie_with_domain" {
		c += "; Domain=argon.test"
	}
	return c
}

func (f *fakeAPI) deviceCookieHeader(value string, maxAge int) string {
	path := "/auth/"
	if f.brk == "device_cookie_path_root" {
		path = "/"
	}
	return fmt.Sprintf("%s=%s; Path=%s; Max-Age=%d; HttpOnly; Secure; SameSite=Strict", deviceCookie, value, path, maxAge)
}

func clearSession(w http.ResponseWriter, f *fakeAPI) {
	w.Header().Add("Set-Cookie", f.sessionCookieHeader("", 0))
}

// sessionLayer: the browser session, in front of the fake's authentication.
func (f *fakeAPI) sessionLayer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin") != ""
		bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		hasBearer = hasBearer && bearer != ""
		cookie := cookieValue(r, sessionCookie)
		viaCookie := false

		if hasBearer && cookie != "" {
			switch f.brk {
			case "cookie_wins":
				r.Header.Del("Authorization")
				hasBearer = false
			case "bearer_wins":
				cookie = ""
			default:
				problem(w, http.StatusBadRequest, "ambiguous_credentials", "one request carries one credential")
				return
			}
		}
		if hasBearer && origin && f.brk != "bearer_with_origin_ok" {
			problem(w, http.StatusUnauthorized, "bearer_from_browser", "a browser signs in with its session cookie, never a token held by the page")
			return
		}
		if cookie != "" && !hasBearer && f.brk != "cookie_ignored" {
			r.Header.Set("Authorization", "Bearer "+cookie) // the fake's authentication reads this
			viaCookie = true
			r = r.WithContext(context.WithValue(r.Context(), ctxCookie{}, true))
		}

		// CSRF: a cookie session's state change carries its own token.
		public := fakePublic[r.URL.Path]
		needsCSRF := !public && ((viaCookie && (r.Method == http.MethodPost || f.brk == "csrf_on_get")) ||
			(hasBearer && f.brk == "bearer_needs_csrf" && r.Method == http.MethodPost))
		if needsCSRF && f.brk == "signout_without_csrf" && r.URL.Path == "/auth/sign-out" {
			needsCSRF = false
		}
		if needsCSRF && f.brk != "csrf_unchecked" {
			tok := cookie
			if !viaCookie {
				tok = bearer
			}
			ok := f.csrfMatches(r.Context(), tok, r.Header.Get("X-CSRF-Token"))
			if !ok {
				if f.brk == "csrf_after_handler" {
					next.ServeHTTP(&recorder{header: http.Header{}}, r) // the effect happens; the answer is discarded
				}
				f.csrfFailed(r.Context(), tok)
				problem(w, http.StatusForbidden, "csrf_failed", "this request did not come from the page that signed in")
				return
			}
		}

		switch {
		case (r.URL.Path == "/auth/sign-in" || r.URL.Path == "/auth/codes/verify") && r.Method == http.MethodPost:
			f.signInLayer(w, r, next, origin)
			return
		case r.URL.Path == "/auth/sign-out" && viaCookie:
			f.signOutLayer(w, r, next, cookie)
			return
		}

		rec := &recorder{header: http.Header{}}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		if status == http.StatusUnauthorized && cookie != "" && f.brk != "dead_cookie_kept" {
			clearSession(w, f)
		}
		w.WriteHeader(status)
		w.Write(rec.buf.Bytes())
	})
}

func (f *fakeAPI) csrfMatches(ctx context.Context, sessionTok, presented string) bool {
	if presented == "" || f.brk == "csrf_never_matches" {
		return false
	}
	return hmac.Equal([]byte(presented), []byte(f.csrfFor(ctx, sessionTok)))
}

func (f *fakeAPI) csrfFailed(ctx context.Context, sessionTok string) {
	switch f.brk {
	case "csrf_failure_ends_session":
		f.db.Exec(ctx, `UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1 AND ended_at IS NULL`, sha(sessionTok))
	case "csrf_failure_counts_as_sign_in_failure":
		f.db.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded)
			SELECT user_id, $2, false FROM auth_sessions WHERE token_digest = $1`, sha(sessionTok), digest())
	}
}

// signInLayer: a browser's sign-in or code-verify. Its device rides in the
// device cookie, never the body; its session leaves in the session cookie,
// and its body carries the CSRF token instead of any credential.
func (f *fakeAPI) signInLayer(w http.ResponseWriter, r *http.Request, next http.Handler, origin bool) {
	raw, _ := io.ReadAll(r.Body)
	dev := cookieValue(r, deviceCookie)
	if origin {
		var body map[string]any
		if json.Unmarshal(raw, &body) == nil {
			if _, sent := body["deviceToken"]; sent && f.brk != "device_token_body_ok" {
				problem(w, http.StatusBadRequest, "device_token_from_browser", "a browser's device is its device cookie, never a token in the body")
				return
			}
			if dev != "" && r.URL.Path == "/auth/sign-in" {
				body["deviceToken"] = dev
				raw, _ = json.Marshal(body)
			}
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	rec := &recorder{header: http.Header{}}
	next.ServeHTTP(rec, r)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	out := rec.buf.Bytes()
	var res map[string]any
	if status == http.StatusOK && json.Unmarshal(out, &res) == nil && res["outcome"] == "signed_in" {
		tok, _ := res["sessionToken"].(string)
		newDev, _ := res["deviceToken"].(string)
		if origin || f.brk == "cookie_for_non_browser" {
			maxAge := 0
			if s, ok := res["expiresAt"].(string); ok {
				if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
					maxAge = int(time.Until(at).Seconds())
				}
			}
			if f.brk == "max_age_fixed_30_days" {
				maxAge = 30 * 24 * 3600
			}
			w.Header().Add("Set-Cookie", f.sessionCookieHeader(tok, maxAge))
		}
		if !origin && newDev != "" && f.brk == "device_cookie_for_non_browser" {
			w.Header().Add("Set-Cookie", f.deviceCookieHeader(newDev, deviceMaxAge))
		}
		if origin {
			devAge := deviceMaxAge
			if f.brk == "device_cookie_session_length" {
				devAge = 8 * 3600
			}
			if newDev != "" {
				w.Header().Add("Set-Cookie", f.deviceCookieHeader(newDev, devAge))
			} else if dev != "" && f.brk != "device_cookie_not_renewed" {
				w.Header().Add("Set-Cookie", f.deviceCookieHeader(dev, devAge))
			}
			if f.brk != "token_in_browser_body" {
				delete(res, "sessionToken")
			}
			if f.brk != "device_token_in_browser_body" {
				delete(res, "deviceToken")
			}
			res["csrfToken"] = f.csrfFor(r.Context(), tok)
			out, _ = json.Marshal(res)
		}
	}
	for k, v := range rec.header {
		if k != "Content-Length" {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(status)
	w.Write(out)
}

// signOutLayer: a cookie session's sign-out ends the session (the handler)
// AND clears its cookie, in one response; the device is left as it was.
func (f *fakeAPI) signOutLayer(w http.ResponseWriter, r *http.Request, next http.Handler, cookie string) {
	status := http.StatusNoContent
	var body []byte
	if f.brk != "signout_cookie_only" {
		rec := &recorder{header: http.Header{}}
		next.ServeHTTP(rec, r)
		status, body = rec.status, rec.buf.Bytes()
		if status == 0 {
			status = http.StatusOK
		}
		for k, v := range rec.header {
			w.Header()[k] = v
		}
	}
	if f.brk == "signout_revokes_device" {
		f.db.Exec(r.Context(), `UPDATE auth_devices SET revoked_at = auth_now() WHERE revoked_at IS NULL
			AND id = (SELECT device_id FROM auth_sessions WHERE token_digest = $1)`, sha(cookie))
	}
	if (status == http.StatusNoContent || status == http.StatusUnauthorized) && f.brk != "signout_session_only" {
		clearSession(w, f)
	}
	if f.brk == "signout_clears_device" {
		w.Header().Add("Set-Cookie", f.deviceCookieHeader("", 0))
	}
	w.WriteHeader(status)
	w.Write(body)
}

// deviceCaller: device_cookie_authenticates takes the device cookie alone as
// the device holder's session.
func (f *fakeAPI) deviceCaller(r *http.Request) (string, bool) {
	dev := cookieValue(r, deviceCookie)
	if f.brk != "device_cookie_authenticates" || dev == "" {
		return "", false
	}
	var user string
	if f.db.QueryRow(r.Context(), `SELECT user_id::text FROM auth_devices WHERE token_digest = $1 AND revoked_at IS NULL`, auth.TokenDigest(dev)).Scan(&user) != nil {
		return "", false
	}
	return user, true
}
