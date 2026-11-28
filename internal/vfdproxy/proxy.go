// Package vfdproxy is the relay that carries the cooperative's VFD calls
// through the wallet platform's server, the one address VFD accepts
// (decided 2026-11-28). It runs there as its own service, under its own user,
// from cmd/vfd-proxy; it never touches the wallet's code or reads its config.
//
// THE POINT OF THIS SHAPE: THE COOPERATIVE NO LONGER HOLDS THE VFD TOKEN.
// The token that moves money from the pool lives here alone, in this
// service's own root-only env file. The cooperative's server holds only a key
// that signs requests to this relay. Someone who takes over the cooperative's
// server gets five calls, from one address, and nothing portable: no token to
// carry away and use from anywhere else. Approving a second address at VFD
// would have meant a second copy of the token. That is why the relay exists.
//
// WHAT IT ALLOWS: exactly the cooperative's five calls (allowed, below);
// anything else is refused before it reaches VFD.
//
// HOW IT KNOWS THE CALLER: nginx and the security group admit 54.85.168.60
// only; every call is signed (internal/vfdsign: HMAC-SHA256 over the method,
// path, query, time and body), checked in constant time, within 60 seconds
// of this server's clock, and never accepted twice.
//
// WHAT IT NEVER DOES: interpret, retry or decide. It passes the call to VFD
// once and returns VFD's status, content type and body BYTE FOR BYTE. Every
// rule about what VFD's answer means lives in the cooperative (internal/vfd,
// internal/wallet), tested with mutants; a relay with opinions would be a
// second place for them to drift. Its own answers (a refusal, VFD not
// reached, VFD too slow) carry the X-Argon-Proxy header and a body that says
// it is the proxy's, never shaped like VFD's.
//
// WHAT IT RECORDS: one line per call, what was asked and what was answered,
// verbatim except every BVN field, which is replaced BEFORE the line exists
// (redactBVN, in record): the line is built from an already-redacted copy and
// written with a single append, so no crash part-way through a write can leave
// a BVN on disk. The VFD token is added to the forwarded call after the line
// is built from the cooperative's request, and is never in it.
package vfdproxy

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

	"github.com/argon/backend/internal/vfdsign"
)

// allowed: the cooperative's calls, by method and path. Nothing else passes.
var allowed = map[string]bool{
	"GET /transfer/recipient":   true, // name enquiry
	"POST /transfer":            true, // send a transfer
	"GET /transactions":         true, // ask about a transfer
	"GET /account/enquiry":      true, // the pool's details
	"GET /account/transactions": true, // an account's history
}

// bodyCap: the most of a request or an answer read (VFD's are a few hundred bytes).
const bodyCap = 1 << 20

// Config: the relay's settings. Keys: the signing keys accepted, the current
// one first (two during a rotation).
type Config struct {
	Upstream string        // VFD's base URL, https
	Token    string        // VFD's access token: added to every forwarded call
	Keys     [][]byte      // at least one, each 32 bytes or more
	Log      io.Writer     // the record, one line per call (an append-only file)
	Timeout  time.Duration // how long VFD is given; zero: 50 seconds, inside the cooperative's 60
	Now      func() time.Time
	Client   *http.Client // optional: the client calls to VFD go through
}

type relay struct {
	cfg    Config
	mu     sync.Mutex
	seen   map[string]time.Time // signatures accepted in the last two minutes
	logMu  sync.Mutex
	client *http.Client
}

// New returns the relay's handler, or refuses a configuration it cannot run.
func New(cfg Config) (http.Handler, error) {
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
	return &relay{cfg: cfg, seen: map[string]time.Time{}, client: c}, nil
}

type entry struct {
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

func (p *relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := p.cfg.Now()
	id := newID()
	w.Header().Set(vfdsign.HeaderProxyTime, strconv.FormatInt(start.Unix(), 10))
	body, err := io.ReadAll(io.LimitReader(r.Body, bodyCap))
	if err != nil {
		p.refuse(w, r, nil, start, id, http.StatusBadRequest, "unreadable", "the request could not be read")
		return
	}
	path, query := r.URL.EscapedPath(), r.URL.RawQuery
	if !allowed[r.Method+" "+path] {
		p.refuse(w, r, body, start, id, http.StatusForbidden, "not_allowed", "this relay passes the cooperative's five VFD calls and nothing else")
		return
	}
	ts, err := strconv.ParseInt(r.Header.Get(vfdsign.HeaderTime), 10, 64)
	sig := r.Header.Get(vfdsign.HeaderSignature)
	if err != nil || sig == "" {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "bad_signature", "every call is signed")
		return
	}
	if skew := start.Unix() - ts; skew > vfdsign.MaxSkewSeconds || skew < -vfdsign.MaxSkewSeconds {
		p.refuse(w, r, body, start, id, http.StatusUnauthorized, "stale", "the call's time is more than 60 seconds from this server's: check both clocks")
		return
	}
	ok := false
	for _, k := range p.cfg.Keys {
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
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			// VFD may have acted: the cooperative must ask, never assume.
			p.refuse(w, r, body, start, id, http.StatusGatewayTimeout, "vfd_timeout", "VFD did not answer in time; it may have acted")
			return
		}
		p.refuse(w, r, body, start, id, http.StatusBadGateway, "vfd_unreachable", "VFD could not be reached")
		return
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, bodyCap))
	if err != nil {
		p.refuse(w, r, body, start, id, http.StatusGatewayTimeout, "vfd_timeout", "VFD's answer could not be read in full; it may have acted")
		return
	}
	p.record(r, body, start, id, resp.StatusCode, answer, "passed")
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(answer) // VFD's answer, unchanged
}

// once: true the first time a signature is seen in the last two minutes.
func (p *relay) once(sig string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for s, at := range p.seen {
		if now.Sub(at) > 2*time.Minute {
			delete(p.seen, s)
		}
	}
	if _, dup := p.seen[sig]; dup {
		return false
	}
	p.seen[sig] = now
	return true
}

// refuse: the proxy's own answer, marked as the proxy's, and recorded.
func (p *relay) refuse(w http.ResponseWriter, r *http.Request, body []byte, start time.Time, id string, status int, outcome, message string) {
	out, _ := json.Marshal(map[string]string{"proxy": "argon-vfd-proxy", "outcome": outcome, "message": message})
	p.record(r, body, start, id, status, out, outcome)
	w.Header().Set(vfdsign.HeaderProxy, outcome)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(out)
}

// record: one line, what was asked and what was answered. THE BVN IS
// REMOVED BEFORE THE LINE EXISTS: the request, the query and the answer are
// each redacted into new copies first, the line is built from those copies
// only, and it is written in one append. Nothing that carried a BVN is ever
// handed to the writer, so a crash mid-write cannot leave one behind.
func (p *relay) record(r *http.Request, req []byte, start time.Time, id string, status int, resp []byte, outcome string) {
	e := entry{
		Time: start.UTC().Format(time.RFC3339Nano), ID: id, Method: r.Method, Path: r.URL.EscapedPath(),
		Query: redactQuery(r.URL.RawQuery), Request: string(redactBVN(req)), Status: status,
		Response: string(redactBVN(resp)), Outcome: outcome, Ms: p.cfg.Now().Sub(start).Milliseconds(),
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

const redacted = "[redacted]"

var bvnKey = regexp.MustCompile(`(?i)bvn`)

// redactBVN: a copy of a body with the value of every field whose name
// contains "bvn" (bvn, toBvn, ...) replaced, at any depth, and EVERY OTHER BYTE
// AS IT CAME: the values are replaced in the text, not by re-encoding. The
// copy is then read back: if any BVN field still holds anything but the
// redaction (a shape the pattern missed: an object, a number, an escaped
// key), the copy is re-encoded with those fields scrubbed instead, verbatim
// lost but never the BVN. A body that is not JSON is redacted by pattern.
func redactBVN(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := bvnInText.ReplaceAll(b, []byte(`"$1":"`+redacted+`"`))
	var v any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return out // not JSON: the pattern is all there is
	}
	if clean(v) {
		return out
	}
	again, err := json.Marshal(scrub(v))
	if err != nil {
		return []byte(`"` + redacted + `: unreadable"`)
	}
	return again
}

var bvnInText = regexp.MustCompile(`(?i)"([^"\\]*bvn[^"\\]*)"\s*:\s*("(?:[^"\\]|\\.)*"|-?[0-9][0-9.eE+-]*)`)

// clean: no field named for a BVN holds anything but the redaction.
func clean(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if bvnKey.MatchString(k) {
				if x != redacted {
					return false
				}
			} else if !clean(x) {
				return false
			}
		}
	case []any:
		for _, x := range t {
			if !clean(x) {
				return false
			}
		}
	}
	return true
}

func scrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if bvnKey.MatchString(k) {
				t[k] = redacted
			} else {
				t[k] = scrub(x)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = scrub(t[i])
		}
		return t
	}
	return v
}

// redactQuery: the query as sent, with the value of any parameter named for
// a BVN replaced; the other parameters, their order and encoding, untouched.
func redactQuery(raw string) string {
	parts := strings.Split(raw, "&")
	for i, kv := range parts {
		k, _, _ := strings.Cut(kv, "=")
		if dk, err := url.QueryUnescape(k); err != nil || bvnKey.MatchString(dk) || bvnKey.MatchString(k) {
			parts[i] = k + "=" + redacted
		}
	}
	return strings.Join(parts, "&")
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
