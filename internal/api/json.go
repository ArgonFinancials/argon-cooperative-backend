package api

import (
	"mime"
	"net/http"
)

// A BODY IS JSON, OR IT IS REFUSED (ruled 2026-10-31, LEDGER_CONTRACT.md §12).
// Written against test/tier1/tier1_api_session_test.go 33-39.
//
// The app and the API share a domain with the company's other platforms, so a
// page on any of them is same-site and the browser sends it the session
// cookie. An HTML form posts text/plain, form-encoded or multipart bodies
// with NO PREFLIGHT, and the generated layer decodes any body as JSON
// whatever its type. Sign-in has no CSRF token (there is no session yet), so
// such a page could sign a member's browser into an account the attacker
// controls (login CSRF). application/json cannot be sent cross-origin without
// a preflight, and the preflight passes only for the app's origin (cors.go).
//
// So a request WITH A BODY must say application/json (parameters such as
// charset allowed; media types are case-insensitive), or it is refused 415
// before anything runs: no session is looked up, no PIN is checked or
// counted. A request with no body is untouched. Wrapped INSIDE CORS, so the
// app's page can read the refusal.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 { // -1: a body of unknown length is still a body
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				writeProblem(w, http.StatusUnsupportedMediaType, Problem{Code: "unsupported_media_type", Message: "Send the request body as application/json."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
