//go:build tier1

// EVERY OPERATION SAYS WHETHER IT IS BUILT, AND IS RIGHT (2026-10-26).
//
// Found by the frontend: it built the member's home against
// getMemberWithdrawable, which the spec describes and the server did not
// implement; the answer was a 500 indistinguishable from a crash. So every
// operation carries x-argon-built, true or false, and this test holds the flag
// to the truth in both directions:
//
//  1. the flag is there, true or false: never a default either way;
//  2. it agrees with the spec itself: false exactly when a 501 is declared;
//  3. it agrees with the REAL SERVER: marked false, the operation answers 501
//     not_built; marked true, it answers anything else, which means a handler
//     ran. The test does not judge that answer, only whether the code exists.
//
// THE EXCEPTION, STATED: every other API suite runs the reference fake to test
// behaviour; this one asks which code exists, and only the real binary knows.
// So it builds the real server even in a fake-mode run, on the test database's
// own connection: which role runs it does not change which code exists.
package tier1

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/argon/backend/internal/api"
)

type specOp struct {
	method, path string
	op           *openapi3.Operation
}

// specOps: every operation in the spec the server serves, in a fixed order.
func specOps(t *testing.T) []specOp {
	t.Helper()
	doc, err := api.GetSwagger()
	fixture(t, err, "the served spec")
	var out []specOp
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			out = append(out, specOp{method, path, op})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].op.OperationID < out[j].op.OperationID })
	return out
}

// builtFlag: the operation's x-argon-built, as the test reads it. Names are
// compared without case: the served spec carries the generator's Go names
// (GetMe), the YAML its own (getMe). The breaks
// that prove the test can fail act here, on the reading, never on the spec.
func builtFlag(o specOp) (built, present, isBool bool) {
	v, present := o.op.Extensions["x-argon-built"]
	built, isBool = v.(bool)
	switch os.Getenv("ARGON_TEST_API_FAKE") {
	case "built_flag_missing":
		if strings.EqualFold(o.op.OperationID, "getMe") {
			return false, false, false
		}
	case "built_flag_flipped": // read as unbuilt, its responses unchanged (no 501): the spec contradicts itself
		if strings.EqualFold(o.op.OperationID, "getMe") {
			return false, true, true
		}
	case "not_built_answer_kept":
		if strings.EqualFold(o.op.OperationID, "getMe") {
			return false, true, true
		}
	}
	return built, present, isBool
}

// declares501: whether the operation declares its 501 not_built, as the test
// reads it. Two breaks read the flag AND the 501 together, consistently, so
// the spec agrees with itself and only the real server (step 3) can catch them.
func declares501(o specOp) bool {
	switch os.Getenv("ARGON_TEST_API_FAKE") {
	case "not_built_answer_kept": // written up as unbuilt while its handler exists: the quiet direction
		if strings.EqualFold(o.op.OperationID, "getMe") {
			return true
		}
	}
	return o.op.Responses != nil && o.op.Responses.Value("501") != nil
}

func TestApiSpec02_EveryOperationSaysWhetherItIsBuiltAndIsRight(t *testing.T) {
	ops := specOps(t)
	var missing, contradicts []string
	for _, o := range ops {
		built, present, isBool := builtFlag(o)
		if !present || !isBool {
			missing = append(missing, o.op.OperationID)
			continue
		}
		if built == declares501(o) {
			contradicts = append(contradicts, o.op.OperationID)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("UNSAID — %d of %d operations do not say whether they are built (x-argon-built, true or false): %v", len(missing), len(ops), missing)
	}
	if len(contradicts) > 0 {
		t.Fatalf("CONTRADICTS ITSELF — marked built but declaring a 501, or marked unbuilt without one: %v", contradicts)
	}

	// 3. The real server: the code itself.
	h, err := api.NewServer(api.Deps{DB: db, CSRFKey: testCSRFKey})
	fixture(t, err, "the real server")
	if os.Getenv("ARGON_TEST_API_FAKE") == "built_without_handler" {
		h = notBuiltStub(h) // the spec says built; the handler was never written
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	user := newSociety(t).user("Who calls every operation")
	for _, o := range ops {
		built, _, _ := builtFlag(o)
		status, code := callOnce(t, srv, o, signIn(t, user)) // a fresh session each: sign-out ends its own
		if status == http.StatusUnauthorized {
			t.Fatalf("FIXTURE: %s %s did not reach its handler (401): the test proves nothing about it", o.method, o.path)
		}
		notBuilt := status == http.StatusNotImplemented && code == "not_built"
		switch {
		case built && notBuilt:
			t.Fatalf("NOT BUILT — %s (%s %s) is marked built and answers 501 not_built", o.op.OperationID, o.method, o.path)
		case !built && !notBuilt:
			t.Fatalf("BUILT — %s (%s %s) is marked unbuilt, yet a handler answered %d %q: mark it built, remove its 501, delete its not-built handler, together",
				o.op.OperationID, o.method, o.path, status, code)
		}
	}
}

// callOnce calls an operation with every required parameter filled from the
// spec's own schemas, so the request reaches the handler rather than stopping
// at the router, and returns the status and the Problem code, if any.
func callOnce(t *testing.T, srv *httptest.Server, o specOp, token string) (int, string) {
	t.Helper()
	path := o.path
	q := []string{}
	headers := map[string]string{}
	for _, pr := range o.op.Parameters {
		p := pr.Value
		if p == nil || (!p.Required && p.In != "path") {
			continue
		}
		v := sampleFor(p.Schema)
		switch p.In {
		case "path":
			path = strings.ReplaceAll(path, "{"+p.Name+"}", v)
		case "query":
			q = append(q, p.Name+"="+v)
		case "header":
			headers[p.Name] = v
		}
	}
	if len(q) > 0 {
		path += "?" + strings.Join(q, "&")
	}
	var body io.Reader
	if o.op.RequestBody != nil {
		body = bytes.NewReader([]byte("{}"))
	}
	req, _ := http.NewRequest(o.method, srv.URL+path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, o.method+" "+path)
	defer r.Body.Close()
	var p struct {
		Code string `json:"code"`
	}
	raw, _ := io.ReadAll(r.Body)
	json.Unmarshal(raw, &p)
	return r.StatusCode, p.Code
}

// sampleFor: a value the router accepts for a parameter's schema.
func sampleFor(s *openapi3.SchemaRef) string {
	if s == nil || s.Value == nil {
		return "x"
	}
	switch s.Value.Format {
	case "uuid":
		b := make([]byte, 16)
		rand.Read(b)
		b[6], b[8] = (b[6]&0x0f)|0x40, (b[8]&0x3f)|0x80
		h := hex.EncodeToString(b)
		return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	case "date":
		return "2026-01-01"
	}
	if len(s.Value.Enum) > 0 {
		if v, ok := s.Value.Enum[0].(string); ok {
			return v
		}
	}
	if s.Value.Type != nil && (s.Value.Type.Is("integer") || s.Value.Type.Is("number")) {
		return "1"
	}
	return "K-" + digest()[:20] // any string: an idempotency key, a reference
}

// notBuiltStub (break built_without_handler): the real server with one
// operation's handler replaced by a not-built answer, as if it had been marked
// built and its 501 removed while the handler was never written. The spec
// agrees with itself; only step 3, the server, can catch it.
func notBuiltStub(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/members/") && strings.HasSuffix(r.URL.Path, "/withdrawable") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(`{"code":"not_built","message":"This is not available yet."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
