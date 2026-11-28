// Package vfdsign is the one definition of how the cooperative signs a call
// to the VFD proxy (cmd/vfd-proxy), shared by the proxy that checks it and the
// client that makes it, so the two cannot disagree.
//
// A signature is HMAC-SHA256, keyed by a secret only the two servers hold,
// over the method, the path as sent, the raw query, the time (Unix seconds)
// and the SHA-256 of the body. A request copied from a log, or changed in
// transit, does not verify; one replayed is refused by the proxy.
package vfdsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const (
	// HeaderTime and HeaderSignature carry the signature on a call.
	HeaderTime      = "X-Argon-Timestamp"
	HeaderSignature = "X-Argon-Signature"
	// HeaderProxy marks an answer that is the PROXY's own, never VFD's:
	// refused (bad_signature, stale, replayed, not_allowed) or VFD not reached
	// (vfd_unreachable) or not answering in time (vfd_timeout). VFD's own
	// answers pass through without it.
	HeaderProxy = "X-Argon-Proxy"
	// HeaderProxyTime: the proxy's clock (Unix seconds), on every answer, so
	// the cooperative can see drift before signatures start failing.
	HeaderProxyTime = "X-Argon-Proxy-Time"

	// MaxSkew: how far apart the two clocks may be before a call is refused.
	MaxSkewSeconds = 60
	// AlertSkew: drift worth a person's attention, well before MaxSkew.
	AlertSkewSeconds = 10
)

// Sign returns the hex signature of one call.
func Sign(key []byte, method, path, rawQuery string, unix int64, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(method + "\n" + path + "\n" + rawQuery + "\n" + strconv.FormatInt(unix, 10) + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(m.Sum(nil))
}

// Verify: the signature is this call's under key, compared in constant time.
func Verify(key []byte, method, path, rawQuery string, unix int64, body []byte, signature string) bool {
	want := Sign(key, method, path, rawQuery, unix, body)
	return hmac.Equal([]byte(want), []byte(signature))
}
