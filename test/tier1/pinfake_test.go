//go:build tier1

// The REFERENCE new-PIN rule (test code, not the service), breakable one named
// way per run (ARGON_TEST_PIN_FAKE=<break>):
//
//	four_digits_ok   a four-digit PIN is accepted (the dev seed's length)
//	letters_ok       any six characters are accepted
//	unicode_digits   any Unicode digit counts as a digit
//	same_digit_ok    all one digit is accepted
//	run_up_only      only an ascending run is refused
//	doubled_ok       doubled digits of a run are accepted
//	phone_first_only only the member's first number is compared
//	phone_ignored    the member's phone is never compared
//	repeated_pair    (over-strict) any repeated pair (121212) is refused too
//	wraps            (over-strict) a run that wraps (901234) is refused too
package tier1

import (
	"strings"
	"unicode"

	"github.com/argon/backend/internal/auth"
)

func refCheckNewPIN(brk, pin string, phones ...string) error {
	n := 6
	if brk == "four_digits_ok" && len(pin) == 4 {
		n = 4
	}
	size := len(pin)
	if brk == "unicode_digits" {
		size = len([]rune(pin)) // characters, so a full-width digit reaches the digit check
	}
	if size != n {
		return auth.ErrPINNotAllowed
	}
	for _, r := range pin {
		ok := r >= '0' && r <= '9'
		if brk == "letters_ok" {
			ok = true
		}
		if brk == "unicode_digits" {
			ok = unicode.IsDigit(r)
		}
		if !ok {
			return auth.ErrPINNotAllowed
		}
	}
	if n != 6 {
		return nil
	}
	d := []byte(pin)
	steps := func(seq []byte, step int, wrap bool) bool {
		for i := 1; i < len(seq); i++ {
			next := int(seq[i-1]-'0') + step
			if wrap {
				next = (next + 10) % 10
			}
			if int(seq[i]-'0') != next {
				return false
			}
		}
		return true
	}
	wrap := brk == "wraps"
	if brk != "same_digit_ok" && steps(d, 0, false) {
		return auth.ErrPINNotAllowed
	}
	if steps(d, 1, wrap) || (brk != "run_up_only" && steps(d, -1, wrap)) {
		return auth.ErrPINNotAllowed
	}
	if brk != "doubled_ok" && d[0] == d[1] && d[2] == d[3] && d[4] == d[5] {
		if half := []byte{d[0], d[2], d[4]}; steps(half, 1, false) || steps(half, -1, false) {
			return auth.ErrPINNotAllowed
		}
	}
	if brk == "repeated_pair" && pin[0:2] == pin[2:4] && pin[2:4] == pin[4:6] {
		return auth.ErrPINNotAllowed
	}
	if brk != "phone_ignored" {
		for i, p := range phones {
			if brk == "phone_first_only" && i > 0 {
				break
			}
			if len(p) >= 6 && strings.HasSuffix(p, pin) {
				return auth.ErrPINNotAllowed
			}
		}
	}
	return nil
}
