# Run from the repo root:  python3 test/tier1/genproxyfake.py
# Generates test/tier1/proxyfake_test.go from internal/vfdproxy/proxy.go: the
# same code, renamed into package tier1, with one named break per run
# (ARGON_TEST_PROXY_FAKE=<break>). Every break must apply exactly once.
import re

src = open('internal/vfdproxy/proxy.go', encoding='utf-8').read()
body = src[src.index('\n)\n', src.index('import (')) + 3:]

# The Config type is the package's; the reference builds on it.
a = body.index('// Config: the relay')
b = body.index('\n}\n', a) + 3
body = body[:a] + body[b:]
for old, new in [('relay', 'refRelay'), ('entry', 'refEntry'), ('allowed', 'refAllowed'), ('bodyCap', 'refBodyCap'),
                 ('redactBVN', 'refRedactBVN'), ('scrub', 'refScrub'), ('redactQuery', 'refRedactQuery'), ('newID', 'refNewID'),
                 ('bvnKey', 'refBvnKey'), ('clean', 'refClean'), ('bvnInText', 'refBvnInText'), ('redacted', 'refRedacted')]:
    # never inside the text the relay writes: "[redacted]" stays as it is
    body = re.sub(r'(?<!\[)\b' + old + r'\b(?!\])', new, body)
body = body.replace('func New(cfg Config) (http.Handler, error) {', 'func newRefProxyWith(cfg vfdproxy.Config, brk string) (http.Handler, error) {')
body = body.replace('	cfg    Config\n', '	cfg    vfdproxy.Config\n	brk    string\n')
body = body.replace('return &refRelay{cfg: cfg, seen: map[string]time.Time{}, client: c}, nil', 'return &refRelay{cfg: cfg, brk: brk, seen: map[string]time.Time{}, client: c}, nil')

BREAKS = [
 ('signature_unchecked', '''	ok := false
	for _, k := range p.cfg.Keys {''', '''	ok := p.brk == "signature_unchecked"
	for _, k := range p.cfg.Keys {'''),
 ('stale_ok', '''	if skew := start.Unix() - ts; skew > vfdsign.MaxSkewSeconds || skew < -vfdsign.MaxSkewSeconds {''',
  '''	if skew := start.Unix() - ts; p.brk != "stale_ok" && (skew > vfdsign.MaxSkewSeconds || skew < -vfdsign.MaxSkewSeconds) {'''),
 ('replay_ok', '''	if _, dup := p.seen[sig]; dup {''', '''	if _, dup := p.seen[sig]; dup && p.brk != "replay_ok" {'''),
 ('any_path', '''	if !refAllowed[r.Method+" "+path] {''', '''	if !refAllowed[r.Method+" "+path] && p.brk != "any_path" {'''),
 ('body_rewritten', '''	w.Write(answer) // VFD's answer, unchanged''', '''	if p.brk == "body_rewritten" {
		answer = bytes.TrimSpace(answer)
	}
	w.Write(answer) // VFD's answer, unchanged'''),
 ('status_rewritten', '''	w.WriteHeader(resp.StatusCode)
''', '''	if p.brk == "status_rewritten" {
		resp.StatusCode = http.StatusOK
	}
	w.WriteHeader(resp.StatusCode)
'''),
 ('retried', '''	resp, err := p.client.Do(up)
	if err != nil {''', '''	resp, err := p.client.Do(up)
	if err != nil && p.brk == "retried" {
		again, _ := http.NewRequestWithContext(r.Context(), r.Method, u, bytes.NewReader(body))
		resp, err = p.client.Do(again)
	}
	if err != nil {'''),
 ('timeout_as_unreachable', '''		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {''',
  '''		if (errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)) && p.brk != "timeout_as_unreachable" {'''),
 ('bvn_logged', '''		Query: refRedactQuery(r.URL.RawQuery), Request: string(refRedactBVN(req)), Status: status,
		Response: string(refRedactBVN(resp)), Outcome: outcome, Ms: p.cfg.Now().Sub(start).Milliseconds(),
	}''', '''		Query: refRedactQuery(r.URL.RawQuery), Request: string(refRedactBVN(req)), Status: status,
		Response: string(refRedactBVN(resp)), Outcome: outcome, Ms: p.cfg.Now().Sub(start).Milliseconds(),
	}
	if p.brk == "bvn_logged" {
		e.Request, e.Response = string(req), string(resp)
	}
	if p.brk == "token_logged" {
		e.Request += " AccessToken=" + p.cfg.Token
	}'''),
 ('time_header_missing', '''	w.Header().Set(vfdsign.HeaderProxyTime, strconv.FormatInt(start.Unix(), 10))''', '''	if p.brk != "time_header_missing" {
		w.Header().Set(vfdsign.HeaderProxyTime, strconv.FormatInt(start.Unix(), 10))
	}'''),
 ('unmarked_refusal', '''	w.Header().Set(vfdsign.HeaderProxy, outcome)''', '''	if p.brk != "unmarked_refusal" {
		w.Header().Set(vfdsign.HeaderProxy, outcome)
	}'''),
 ('rotation_off', '''	for _, k := range p.cfg.Keys {
		if vfdsign.Verify(''', '''	for i, k := range p.cfg.Keys {
		if i > 0 && p.brk == "rotation_off" {
			break
		}
		if vfdsign.Verify('''),
]
for name, o, n in BREAKS:
    assert body.count(o) == 1, (name, body.count(o), o[:70])
    body = body.replace(o, n)

hdr = '''//go:build tier1

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
'''
open('test/tier1/proxyfake_test.go', 'w', encoding='utf-8', newline='\n').write(hdr + body)
print('ok')
