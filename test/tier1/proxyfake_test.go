//go:build tier1

// THE MUTATION VEHICLE FOR THE VFD RELAY (cmd/vfd-proxy) — test code, NOT the
// relay. GENERATED from internal/vfdproxy/proxy.go (genproxyfake.py), so it
// cannot drift, with one break per run (ARGON_TEST_PROXY_FAKE=<break>):
//
//	signature_unchecked     any call passed, signed or not
//	stale_ok                a call of any age passed
//	replay_ok               the same signed call passed twice
//	any_path                any VFD path passed, not only the cooperative's five
//	body_rewritten          VFD's answer trimmed on the way back
//	status_rewritten        VFD's status replaced by 200
//	retried                 a call VFD did not answer sent again
//	timeout_as_unreachable  VFD not answering in time said as "not reached" (it may have acted)
//	bvn_logged              the record holds the BVN
//	token_logged            the record holds VFD's token
//	time_header_missing     the relay's clock not on its answers
//	unmarked_refusal        the relay's own refusal not marked as the relay's
//	rotation_off            only the current key accepted, the previous refused mid-rotation
package tier1

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/argon/backend/internal/vfdproxy"
	"github.com/argon/backend/internal/vfdsign"
)

// refAllowed: the cooperative's calls, by method and path. Nothing else passes.
var refAllowed = map[string]bool{
	"GET /transfer/recipient":   true, // name enquiry
	"POST /transfer":            true, // send a transfer
	"GET /transactions":         true, // ask about a transfer
	"GET /account/enquiry":      true, // the pool's details
	"GET /account/transactions": true, // an account's history
	"GET /bank":                 true, // VFD's list of banks (0046, 2026-11-28)
}

// refBodyCap: the most of a request or an answer read (VFD's are a few hundred bytes).
const refBodyCap = 1 << 20

type refRelay struct {
	cfg    vfdproxy.Config
	brk    string
	mu     sync.Mutex
	seen   map[string]time.Time // signatures accepted in the last two minutes
	logMu  sync.Mutex
	client *http.Client
}

// New returns the refRelay's handler, or refuses a configuration it cannot run.
func newRefProxyWith(cfg vfdproxy.Config, brk string) (http.Handler, error) {
	switch {
	case !strings.HasPrefix(cfg.Upstream, "https://") && !strings.HasPrefix(cfg.Upstream, "http://127.0.0.1"):
		return nil, errors.New("vfdproxy: VFD's base URL must be https")
	case cfg.Token == "":
		return nil, errors.New("vfdproxy: VFD's access token must be set")
	case len(cfg.Keys) == 0:
		return nil, errors.New("vfdproxy: at least one signing key")
	case cfg.Log == nil:
		return nil, errors.New("vfdproxy: a record to write every call to")
	}
	for _, k := range cfg.Keys {
		if len(k) < 32 {
			return nil, errors.New("vfdproxy: every signing key is 32 bytes or more")
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 50 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := cfg.Client
	if c == nil {
		// No keep-alive: a call that fails is never resent on a fresh
		// connection by the transport. One call, one try.
		c = &http.Client{Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
	}
	c.Timeout = cfg.Timeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &refRelay{cfg: cfg, brk: brk, seen: map[string]time.Time{}, client: c}, nil
}

type refEntry struct {
	Time     string `json:"time"`
	ID       string `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Query    string `json:"query,omitempty"`
	Request  string `json:"request,omitempty"`
	Status   int    `json:"status"`
	Response string `json:"response,omitempty"`
	Outcome  string `json:"outcome"`
	Ms       int64  `json:"ms"`
}

func (p *refRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := p.cfg.Now()
	id := refNewID()
	if p.brk != "time_header_missing" {
		w.Header().Set(vfdsign.HeaderProxyTime, strconv.FormatInt(start.Unix(), 10))
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, refBodyCap))
	if err != nil {
		p.refuse(w, r, nil, start, id, http.StatusBadRequest, "unreadable", "the request could not be read")
		return
	}
	path, query := r.URL.EscapedPath(), r.URL.RawQuery
	if !refAllowed[r.Method+" "+path] && p.brk != "any_path" {
		p.refuse(w, r, body, start, id, http.StatusForbidden, "not_allowed", "this refRelay passes the cooperative's six VFD calls and nothing else")
		return
	}
	ts, err := strconv.ParseInt(r.Header.Get(vfdsign.HeaderTime), 10, 64)
	sig := r.Header.Get(vfdsign.HeaderSignature)
	if err != nil || sig == "" {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "bad_signature", "every call is signed")
		return
	}
	if skew := start.Unix() - ts; p.brk != "stale_ok" && (skew > vfdsign.MaxSkewSeconds || skew < -vfdsign.MaxSkewSeconds) {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "stale", "the call's time is more than 60 seconds from this server's: check both clocks")
		return
	}
	ok := p.brk == "signature_unchecked"
	for i, k := range p.cfg.Keys {
		if i > 0 && p.brk == "rotation_off" {
			break
		}
		if vfdsign.Verify(k, r.Method, path, query, ts, body, sig) {
			ok = true
			break
		}
	}
	if !ok {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "bad_signature", "the signature does not verify")
		return
	}
	if !p.once(sig, start) {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "replayed", "this call was already made")
		return
	}

	// Forward it, once.
	u := strings.TrimRight(p.cfg.Upstream, "/") + path
	if query != "" {
		u += "?" + query
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.cfg.Timeout)
	defer cancel()
	up, err := http.NewRequestWithContext(ctx, r.Method, u, bytes.NewReader(body))
	if err != nil {
		p.refuse(w, r, body, start, id, http.StatusBadGateway, "vfd_unreachable", "the call to VFD could not be built")
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		up.Header.Set("Content-Type", ct)
	}
	up.Header.Set("AccessToken", p.cfg.Token)
	resp, err := p.client.Do(up)
	if err != nil && p.brk == "retried" {
		again, _ := http.NewRequestWithContext(r.Context(), r.Method, u, bytes.NewReader(body))
		resp, err = p.client.Do(again)
	}
	if err != nil {
		var ne net.Error
		if (errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)) && p.brk != "timeout_as_unreachable" {
			// VFD may have acted: the cooperative must ask, never assume.
			p.refuse(w, r, body, start, id, http.StatusGatewayTimeout, "vfd_timeout", "VFD did not answer in time; it may have acted")
			return
		}
		p.refuse(w, r, body, start, id, http.StatusBadGateway, "vfd_unreachable", "VFD could not be reached")
		return
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, refBodyCap))
	if err != nil {
		p.refuse(w, r, body, start, id, http.StatusGatewayTimeout, "vfd_timeout", "VFD's answer could not be read in full; it may have acted")
		return
	}
	p.record(r, body, start, id, resp.StatusCode, answer, "passed")
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if p.brk == "status_rewritten" {
		resp.StatusCode = http.StatusOK
	}
	w.WriteHeader(resp.StatusCode)
	if p.brk == "body_rewritten" {
		answer = bytes.TrimSpace(answer)
	}
	w.Write(answer) // VFD's answer, unchanged
}

// once: true the first time a signature is seen in the last two minutes.
func (p *refRelay) once(sig string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for s, at := range p.seen {
		if now.Sub(at) > 2*time.Minute {
			delete(p.seen, s)
		}
	}
	if _, dup := p.seen[sig]; dup && p.brk != "replay_ok" {
		return false
	}
	p.seen[sig] = now
	return true
}

// refuse: the proxy's own answer, marked as the proxy's, and recorded.
func (p *refRelay) refuse(w http.ResponseWriter, r *http.Request, body []byte, start time.Time, id string, status int, outcome, message string) {
	out, _ := json.Marshal(map[string]string{"proxy": "argon-vfd-proxy", "outcome": outcome, "message": message})
	p.record(r, body, start, id, status, out, outcome)
	if p.brk != "unmarked_refusal" {
		w.Header().Set(vfdsign.HeaderProxy, outcome)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(out)
}

// record: one line, what was asked and what was answered. THE BVN IS
// REMOVED BEFORE THE LINE EXISTS: the request, the query and the answer are
// each refRedacted into new copies first, the line is built from those copies
// only, and it is written in one append. Nothing that carried a BVN is ever
// handed to the writer, so a crash mid-write cannot leave one behind.
func (p *refRelay) record(r *http.Request, req []byte, start time.Time, id string, status int, resp []byte, outcome string) {
	e := refEntry{
		Time: start.UTC().Format(time.RFC3339Nano), ID: id, Method: r.Method, Path: r.URL.EscapedPath(),
		Query: refRedactQuery(r.URL.RawQuery), Request: string(refRedactBVN(req)), Status: status,
		Response: string(refRedactBVN(resp)), Outcome: outcome, Ms: p.cfg.Now().Sub(start).Milliseconds(),
	}
	if p.brk == "bvn_logged" {
		e.Request, e.Response = string(req), string(resp)
	}
	if p.brk == "token_logged" {
		e.Request += " AccessToken=" + p.cfg.Token
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')
	p.logMu.Lock()
	p.cfg.Log.Write(line)
	p.logMu.Unlock()
}

const refRedacted = "[redacted]"

var refBvnKey = regexp.MustCompile(`(?i)bvn`)

// refRedactBVN: a copy of a body with the value of every field whose name
// contains "bvn" (bvn, toBvn, ...) replaced, at any depth, and EVERY OTHER BYTE
// AS IT CAME: the values are replaced in the text, not by re-encoding. The
// copy is then read back: if any BVN field still holds anything but the
// redaction (a shape the pattern missed: an object, a number, an escaped
// key), the copy is re-encoded with those fields scrubbed instead, verbatim
// lost but never the BVN. A body that is not JSON is refRedacted by pattern.
func refRedactBVN(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := refBvnInText.ReplaceAll(b, []byte(`"$1":"`+refRedacted+`"`))
	var v any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return out // not JSON: the pattern is all there is
	}
	if refClean(v) {
		return out
	}
	again, err := json.Marshal(refScrub(v))
	if err != nil {
		return []byte(`"` + refRedacted + `: unreadable"`)
	}
	return again
}

var refBvnInText = regexp.MustCompile(`(?i)"([^"\\]*bvn[^"\\]*)"\s*:\s*("(?:[^"\\]|\\.)*"|-?[0-9][0-9.eE+-]*)`)

// refClean: no field named for a BVN holds anything but the redaction.
func refClean(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if refBvnKey.MatchString(k) {
				if x != refRedacted {
					return false
				}
			} else if !refClean(x) {
				return false
			}
		}
	case []any:
		for _, x := range t {
			if !refClean(x) {
				return false
			}
		}
	}
	return true
}

func refScrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if refBvnKey.MatchString(k) {
				t[k] = refRedacted
			} else {
				t[k] = refScrub(x)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = refScrub(t[i])
		}
		return t
	}
	return v
}

// refRedactQuery: the query as sent, with the value of any parameter named for
// a BVN replaced; the other parameters, their order and encoding, untouched.
func refRedactQuery(raw string) string {
	parts := strings.Split(raw, "&")
	for i, kv := range parts {
		k, _, _ := strings.Cut(kv, "=")
		if dk, err := url.QueryUnescape(k); err != nil || refBvnKey.MatchString(dk) || refBvnKey.MatchString(k) {
			parts[i] = k + "=" + refRedacted
		}
	}
	return strings.Join(parts, "&")
}

func refNewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
