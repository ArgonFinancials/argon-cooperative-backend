package api

import (
	"net/http"
	"strings"
)

// BROWSER ACCESS (CORS). Written against the approved acceptance tests,
// test/tier1/tier1_api_cors_test.go (2026-10-22, found by the frontend's first
// real connection: no CORS headers, and a preflight answered 401).
//
//   - A PREFLIGHT IS ANSWERED HERE, BEFORE AUTHENTICATION. It carries no
//     credentials by design, so anything that authenticates a preflight is
//     wrong by construction, not by configuration. This wraps the whole server.
//   - ONLY THE CONFIGURED ORIGINS, EXACTLY (ARGON_ALLOWED_ORIGINS). Not one that
//     begins or ends like a listed origin, not another port or scheme. Never a
//     wildcard: this API takes PINs and moves money. NOTHING CONFIGURED MEANS NO
//     ORIGIN IS ALLOWED: closed by default, as every quorum, floor and charge.
//   - AN ALLOWED ORIGIN IS NOT A TRUSTED ONE. The allowlist decides nothing
//     about the caller: a real call still needs its session, checked by the
//     authentication inside.
//   - CORS PROTECTS THE PAGE, NOT THE ENDPOINT. A call from an unlisted origin
//     is still ANSWERED (a client that is not a browser sends whatever Origin
//     it likes, or none); it only carries nothing that lets a browser page on
//     that origin read it. Never build on "an unlisted origin is blocked".
//   - Vary: Origin on every answer, so a cache between (a CDN, a corporate
//     proxy) never hands one origin's answer to another.

var (
	corsMethods = "GET, POST"
	corsHeaders = "Authorization, Content-Type, Idempotency-Key" // what our calls carry; the money endpoints need Idempotency-Key
)

// CORS wraps next with browser access for exactly the given origins.
func CORS(origins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o = strings.TrimSpace(o); o != "" {
			allowed[o] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		ok := origin != "" && allowed[origin]
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if !ok {
				writeProblem(w, http.StatusForbidden, Problem{Code: "origin_not_allowed", Message: "this origin may not call this API from a browser"})
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", corsMethods)
			h.Set("Access-Control-Allow-Headers", corsHeaders)
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		next.ServeHTTP(w, r)
	})
}

// ParseOrigins reads ARGON_ALLOWED_ORIGINS: comma-separated exact origins.
func ParseOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
