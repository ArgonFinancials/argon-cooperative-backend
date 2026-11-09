package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Enrolment codes (0024–0026). A code is two halves of 8 Crockford base32
// characters, 40 bits each, drawn independently. A half is stored only as
// its HMAC under the code key; a code's digest is the SHA-256 of its two
// halves' digests in sorted order, exactly as the database assembles a code
// completed by a second signatory (enrolment_assembled). So a whole code and
// a code from two signatories redeem the same way, and the halves may be
// typed in either order.

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const halfLen = 8

func drawHalf() string {
	b := make([]byte, halfLen)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS random source failing is not a recoverable state
	}
	out := make([]byte, halfLen)
	for i, v := range b {
		out[i] = crockford[v&31] // 256 is a multiple of 32: uniform
	}
	return string(out)
}

func group(half string) string { return half[:4] + "-" + half[4:] }

func (s *service) halfDigest(half string) string { return s.codeDigest("enrolment-half:" + half) }

// assembled: the code's digest from its halves' digests, in either order.
func assembled(a, b string) string {
	if b < a {
		a, b = b, a
	}
	h := sha256.Sum256([]byte(a + b))
	return hex.EncodeToString(h[:])
}

func (s *service) NewEnrolmentCode() (string, string) {
	a, b := drawHalf(), drawHalf()
	return group(a) + "-" + group(b), assembled(s.halfDigest(a), s.halfDigest(b))
}

func (s *service) NewEnrolmentHalf() (string, string) {
	h := drawHalf()
	return group(h), s.halfDigest(h)
}

// normalizeCode reads what a person typed: case, spaces and dashes are
// ignored; I and L are 1, O is 0 (Crockford). ok is false unless sixteen
// valid characters remain.
func normalizeCode(typed string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(typed) {
		switch {
		case r == ' ' || r == '-' || r == '\t':
			continue
		case r == 'I' || r == 'L':
			r = '1'
		case r == 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), b.Len() == 2*halfLen
}

// enrolmentDigest: the digest of what was typed. Something that is not a
// code still yields a digest (of the text itself, under a separate label),
// so it is tried against the live code and costs a try like any wrong code,
// at the same cost.
func (s *service) enrolmentDigest(typed string) string {
	n, ok := normalizeCode(typed)
	if !ok {
		return assembled(s.codeDigest("enrolment-not-a-code:"+typed), s.codeDigest("enrolment-not-a-code"))
	}
	return assembled(s.halfDigest(n[:halfLen]), s.halfDigest(n[halfLen:]))
}
