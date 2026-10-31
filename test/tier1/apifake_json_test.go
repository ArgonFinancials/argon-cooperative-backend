//go:build tier1

// A BODY IS JSON, OR IT IS REFUSED, in the API mutation vehicle — test code,
// NOT the API (ruled 2026-10-31, LEDGER_CONTRACT.md §12; tests: session_api
// 33-39). Runs inside fakeCORS, before anything else, as the real one must.
//
// Breaks (ARGON_TEST_API_FAKE=<break>):
//
//	json_unchecked          any body is decoded as JSON, whatever its type (the API before 2026-10-31)
//	json_signin_only        only /auth/sign-in is checked
//	json_plain_text_ok      text/plain is taken as JSON
//	json_form_ok            form-encoded and multipart bodies are accepted
//	json_missing_ok         a body with no Content-Type is accepted
//	json_after_handler      the request runs first, and is refused after: the PIN already checked and counted
//	json_no_cors            the refusal is written outside CORS: the app cannot read it
//	json_bodiless_refused   (over-strict) a POST with no body and no type is refused
//	json_params_refused     (over-strict) "application/json; charset=utf-8" is refused
package tier1

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
)

// jsonGate answers 415 and returns true when r must be refused; otherwise it
// leaves r for the caller.
func jsonGate(w http.ResponseWriter, r *http.Request, brk string, next http.Handler) bool {
	if brk == "json_unchecked" || (brk == "json_signin_only" && r.URL.Path != "/auth/sign-in") {
		return false
	}
	hasBody := r.ContentLength != 0
	ct := r.Header.Get("Content-Type")
	if !hasBody && !(brk == "json_bodiless_refused" && r.Method == http.MethodPost) {
		return false
	}
	ok := false
	if ct == "" {
		ok = brk == "json_missing_ok"
	} else if brk == "json_params_refused" {
		ok = ct == "application/json"
	} else if mt, _, err := mime.ParseMediaType(ct); err == nil {
		ok = mt == "application/json" ||
			(brk == "json_plain_text_ok" && mt == "text/plain") ||
			(brk == "json_form_ok" && (mt == "application/x-www-form-urlencoded" || strings.HasPrefix(mt, "multipart/")))
	}
	if ok {
		return false
	}
	if brk == "json_after_handler" {
		r.Header.Set("Content-Type", "application/json")
		next.ServeHTTP(httptest.NewRecorder(), r) // it ran, and its answer is thrown away
	}
	problem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request body as application/json.")
	return true
}
