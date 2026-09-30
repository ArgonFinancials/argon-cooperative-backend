// Package money holds the one representation of money in this service.
//
// MONEY IS int64 KOBO. ₦1 = 100 kobo. Never a float, at any layer.
//
// On the wire money is a STRING, not a JSON number. A JSON number is an
// IEEE-754 double, and a JSON number parsed in JavaScript is a float — which
// would violate the rule at the one layer nobody inspects. It would never
// visibly fail; precision goes somewhere past ₦90 trillion, so the rule would
// simply be quietly broken. protobuf's JSON mapping encodes int64 as a string
// for exactly this reason; we adopt the discipline without adopting protobuf.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Kobo is a signed amount in kobo. Positive is a debit, negative a credit.
type Kobo int64

var wireShape = regexp.MustCompile(`^-?[0-9]+$`)

var ErrNotAnInteger = errors.New("money must be an integer string of kobo")

// MarshalJSON writes money as a quoted integer string.
func (k Kobo) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(k), 10) + `"`), nil
}

// UnmarshalJSON accepts ONLY a quoted integer string. A bare JSON number is
// rejected rather than coerced: accepting one would let a float reach this
// type silently, which is the failure this whole type exists to prevent.
func (k *Kobo) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("money must be a quoted string, got %s: %w", b, ErrNotAnInteger)
	}
	if !wireShape.MatchString(s) {
		return fmt.Errorf("money %q is not an integer string of kobo: %w", s, ErrNotAnInteger)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("money %q overflows int64: %w", s, ErrNotAnInteger)
	}
	*k = Kobo(n)
	return nil
}

// Naira renders for humans: ₦480,000. Never abbreviated — members read these
// as receipts, so "480k" in a balance or a statement is wrong.
func (k Kobo) Naira() string {
	n := int64(k) / 100
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-₦" + string(out)
	}
	return "₦" + string(out)
}

// Sum adds amounts. A balanced entry sums to exactly zero.
func Sum(amounts ...Kobo) Kobo {
	var total Kobo
	for _, a := range amounts {
		total += a
	}
	return total
}

// Balanced reports whether a set of line amounts forms a valid double entry.
// The database enforces this too — this is the early, cheap check, not the
// authority.
func Balanced(amounts ...Kobo) bool {
	return Sum(amounts...) == 0
}
