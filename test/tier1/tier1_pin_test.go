//go:build tier1

// ACCEPTANCE TESTS — WHAT A NEW PIN MAY BE (ruled 2026-10-30): six digits,
// numeric; refused if all one digit, a straight run up or down (123456,
// 987654), doubled digits of a run (112233, 332211), or the last six digits of
// the member's own phone (the one a member would choose deliberately and an
// attacker who has the number already holds). Nothing beyond: each rule
// removes combinations, and a long blocklist makes PINs predictable.
//
// The rule lives in the auth service ONLY (the database sees the argon2id hash
// and cannot check a format). A member who set a PIN under an older rule is
// not forced to change it; their next PIN must meet this one.
package tier1

import (
	"errors"
	"os"
	"testing"

	"github.com/argon/backend/internal/auth"
)

const pinPhone = "+2348031739264" // its last six (739264) are on no other list

// checkNewPIN: the real rule, or the reference (pinfake_test.go) under
// ARGON_TEST_PIN_FAKE.
func checkNewPIN(pin string, phones ...string) error {
	if brk, ok := os.LookupEnv("ARGON_TEST_PIN_FAKE"); ok {
		return refCheckNewPIN(brk, pin, phones...)
	}
	return auth.CheckNewPIN(pin, phones...)
}

func pinRefused(t *testing.T, pin, why string) {
	t.Helper()
	if err := checkNewPIN(pin, pinPhone); !errors.Is(err, auth.ErrPINNotAllowed) {
		t.Fatalf("ACCEPTED — %q (%s): %v; want ErrPINNotAllowed", pin, why, err)
	}
}

func TestPin01_SixDigitsNumeric(t *testing.T) {
	for pin, why := range map[string]string{
		"4827": "four digits", "48271": "five digits", "4827195": "seven digits", "": "empty",
		"48a719": "a letter", "48 719": "a space", "４８２７１９": "full-width digits", "-48271": "a sign",
	} {
		pinRefused(t, pin, why)
	}
}

func TestPin02_AllOneDigitIsRefused(t *testing.T) {
	for _, pin := range []string{"000000", "111111", "999999"} {
		pinRefused(t, pin, "all one digit")
	}
}

func TestPin03_AStraightRunIsRefused(t *testing.T) {
	for _, pin := range []string{"012345", "123456", "456789", "987654", "543210"} {
		pinRefused(t, pin, "a straight run")
	}
}

func TestPin04_DoubledDigitsOfARunAreRefused(t *testing.T) {
	for _, pin := range []string{"112233", "445566", "778899", "332211", "998877"} {
		pinRefused(t, pin, "doubled digits of a run")
	}
}

func TestPin05_TheLastSixOfTheMembersOwnPhoneAreRefused(t *testing.T) {
	pinRefused(t, "739264", "the last six digits of the member's own phone")
	if err := checkNewPIN("555123", pinPhone, "+2348090555123"); !errors.Is(err, auth.ErrPINNotAllowed) {
		t.Fatalf("ACCEPTED — the last six of the member's SECOND number: %v", err)
	}
}

// Nothing beyond the ruled list: ordinary PINs, near-misses of each rule, and
// another person's phone tail are all accepted.
func TestPin06_Control_OrdinaryPinsAreAccepted(t *testing.T) {
	for pin, why := range map[string]string{
		"482719": "ordinary", "135790": "ordinary", "112234": "a broken doubled run", "123457": "a broken run",
		"111112": "almost one digit", "121212": "a repeated pair (not on the list)", "246810": "a step of two",
		"901234": "a run that wraps", "739265": "one off the phone tail", "000001": "nearly one digit",
	} {
		if err := checkNewPIN(pin, pinPhone); err != nil {
			t.Fatalf("REFUSED — %q (%s): %v; the list is closed, nothing beyond it is refused", pin, why, err)
		}
	}
}
