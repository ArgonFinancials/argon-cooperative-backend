//go:build tier1

// THE MUTATION VEHICLE FOR BROWSER ACCESS (CORS) — test code, NOT the API.
//
// A reference wrapper around the fake API, breakable one named way per run.
// It sits OUTSIDE authentication, as the real one must: a preflight carries no
// credentials by design.
//
// Breaks:
//
//	cors_after_auth       a preflight goes through authentication (401), as before 2026-10-22
//	cors_any_origin       every origin is allowed, its own name echoed back
//	cors_wildcard         Access-Control-Allow-Origin: * on every answer
//	cors_prefix_match     an origin is allowed when it merely begins with a listed one
//	cors_default_open     with nothing configured, every origin is allowed
//	cors_headers_missing  the preflight does not allow the Authorization header
//	cors_no_vary          a real call's answer does not vary by Origin
//	cors_skips_auth       a call from the allowed origin needs no session
package tier1

import (
	"net/http"
	"strings"
)

func fakeCORS(origins []string, brk string, next http.Handler) http.Handler {
	allowed := func(o string) bool {
		if o == "" {
			return false
		}
		switch brk {
		case "cors_any_origin":
			return true
		case "cors_default_open":
			if len(origins) == 0 {
				return true
			}
		}
		for _, a := range origins {
			if o == a || (brk == "cors_prefix_match" && strings.HasPrefix(o, a)) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := r.Header.Get("Origin")
		ok := allowed(o)
		allow := func() {
			if brk == "cors_wildcard" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if ok {
				w.Header().Set("Access-Control-Allow-Origin", o)
			}
			if brk != "cors_no_vary" {
				w.Header().Add("Vary", "Origin")
			}
		}
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight && brk != "cors_after_auth" {
			if !ok {
				if brk == "cors_wildcard" {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}
				w.WriteHeader(http.StatusForbidden)
				return
			}
			allow()
			hdrs := "Authorization, Content-Type, Idempotency-Key"
			if brk == "cors_headers_missing" {
				hdrs = "Content-Type, Idempotency-Key"
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
			w.Header().Set("Access-Control-Allow-Headers", hdrs)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		allow()
		if brk == "cors_skips_auth" && ok && r.Header.Get("Authorization") == "" {
			// An allowed origin treated as trusted: answered without a session.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"userId":"","fullName":"","memberships":[]}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
