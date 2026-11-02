package auth

import "strings"

// CheckNewPIN: WHAT A NEW PIN MAY BE (ruled 2026-10-30). Six digits, ASCII
// 0-9 only, and none of the closed list of trivial PINs:
//   - all one digit (000000);
//   - a straight run, up or down (123456, 987654), never wrapping past 9;
//   - doubled digits of a run (112233, 332211);
//   - the last six digits of any of the member's own phone numbers: the PIN a
//     member would choose deliberately, and the one an attacker who has the
//     number already holds.
//
// NOTHING BEYOND THAT LIST. Each rule removes combinations; a long blocklist
// makes PINs predictable. Do not extend it without a ruling.
//
// THIS IS THE ONLY PLACE THE RULE IS ENFORCED. The database sees only the
// argon2id hash and cannot check a format, so every path that sets a PIN (a
// PIN change, a reset, enrolment) must call this before hashing. A member who
// set a PIN under an older rule is not forced to change it; their next PIN
// must meet this one.
func CheckNewPIN(pin string, phones ...string) error {
	if len(pin) != 6 { // bytes: a non-ASCII digit is never six bytes of six digits
		return ErrPINNotAllowed
	}
	d := make([]int, 6)
	for i := 0; i < 6; i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return ErrPINNotAllowed
		}
		d[i] = int(pin[i] - '0')
	}
	if steps(d, 0) || steps(d, 1) || steps(d, -1) {
		return ErrPINNotAllowed
	}
	if d[0] == d[1] && d[2] == d[3] && d[4] == d[5] {
		if half := []int{d[0], d[2], d[4]}; steps(half, 1) || steps(half, -1) {
			return ErrPINNotAllowed
		}
	}
	for _, p := range phones {
		if strings.HasSuffix(p, pin) {
			return ErrPINNotAllowed
		}
	}
	return nil
}

// steps: each digit is the one before plus step.
func steps(d []int, step int) bool {
	for i := 1; i < len(d); i++ {
		if d[i] != d[i-1]+step {
			return false
		}
	}
	return true
}
