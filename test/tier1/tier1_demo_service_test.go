//go:build tier1

// ACCEPTANCE TESTS — SIGN-IN FOR A DEMONSTRATION SOCIETY'S PEOPLE (0035,
// 2026-11-21). "No activation step and no slips": a person who belongs to
// demonstration societies and to nothing real signs in on any device with the
// PIN the operator gave them, and that device is trusted from then on. Anyone
// who also belongs to a real society, and every real member, takes the real
// path: a code, or an officer. A wrong PIN is refused as always.
//
// The service: default auth.New; ARGON_TEST_AUTH_FAKE=<break> runs the
// reference in authfake_test.go.
package tier1

import (
	"errors"
	"fmt"
	"testing"

	"github.com/argon/backend/internal/auth"
)

// demoWithPIN: a demonstration society's person with the operator's PIN.
func demoWithPIN(t *testing.T, pin string) demoPerson {
	t.Helper()
	p := personIn(t, demoSociety(t), "Demo person")
	fixture(t, setDemoPIN(p.user, hashPIN(pin), "Test Operator", "tester@host", "a test"), "the operator's PIN")
	return p
}

func TestDemoSignIn01_ADemonstrationsPersonSignsInOnANewDeviceWithThePinAlone(t *testing.T) {
	s := newSvc(t)
	p := demoWithPIN(t, "380915")
	if _, err := s.signIn(p.number, "380916", ""); !errors.Is(err, auth.ErrRefused) {
		t.Fatalf("LET IN — a wrong PIN on a new device: %v; want refused, as always", err)
	}
	r, err := s.signIn(p.number, "380915", "")
	if err != nil || r.SessionToken == "" || r.DeviceToken == "" || r.SeeOfficer {
		t.Fatalf("NOT SIGNED IN — a demonstration's person, the right PIN, a new device: %+v, %v; want a session and a trusted device", r, err)
	}
	again, err := s.signIn(p.number, "380915", r.DeviceToken)
	if err != nil || again.SessionToken == "" || again.DeviceToken != "" {
		t.Fatalf("NOT TRUSTED — the same device again: %+v, %v; want a session on the device it was given", again, err)
	}
}

func TestDemoSignIn02_ARealMemberOnANewDeviceIsNotTrustedOnThePin(t *testing.T) {
	s := newSvc(t)
	m := s.member("A real member") // a real society's, with a PIN
	r, err := s.signIn(m.number, m.pin, "")
	if err == nil && (r.SessionToken != "" || r.DeviceToken != "") {
		t.Fatalf("TRUSTED — a real member on a new device was signed in on the PIN alone: %+v", r)
	}
}

func TestDemoSignIn03_SomeoneInADemonstrationAndARealSocietyIsNotTrusted(t *testing.T) {
	s := newSvc(t)
	p := demoWithPIN(t, "380915") // given a PIN while demonstration-only
	real := newSociety(t)
	memberOf(t, real.id, p.user, fmt.Sprintf("R-%d", seq.Add(1)), fmt.Sprintf("+23481%08d", seq.Add(1)))
	r, err := s.signIn(p.number, "380915", "")
	if err == nil && (r.SessionToken != "" || r.DeviceToken != "") {
		t.Fatalf("TRUSTED — someone who also belongs to a real society was signed in on a new device on the PIN alone: %+v", r)
	}
}
