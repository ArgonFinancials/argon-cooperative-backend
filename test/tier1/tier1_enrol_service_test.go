//go:build tier1

// ACCEPTANCE TESTS — redeeming an enrolment code in the SERVICE:
// auth.Service.Enrol. Written before it, for review. Same harness as the
// sign-in and step-up service tests: the real service by default, the fake
// (authfake_enrol_test.go) with ARGON_TEST_AUTH_FAKE=<break>.
//
// What it must make true (approved 2026-11-07, LEDGER_CONTRACT.md §12):
//
//   - THE CODE NEVER STANDS IN FOR A PIN RESET (D-15). It sets a first PIN
//     only for someone who has never had one. Anyone who has a PIN must give
//     it: the code replaces the SMS step, never the PIN.
//
//   - A WRONG PIN NEVER TOUCHES THE CODE, and counts against the PIN lockout
//     as at sign-in. A locked account does not enrol.
//
//   - A WRONG CODE costs one of the code's five tries, NEVER a PIN try, and
//     answers exactly as an unknown number does.
//
//   - A REFUSED OR MISSING NEW PIN LEAVES THE CODE LIVE: nothing is spent
//     until everything the enrolment needs is right.
//
//   - THE USED SLIP SAYS SO to its holder: "This code has already been used.
//     Speak to your secretary." (the accepted residual, 0025).
//
//   - The device handed back is the device the code trusted, and the
//     person's other devices are told a new one signed in.
package tier1

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/argon/backend/internal/auth"
)

// A first PIN that passes CheckNewPIN, and one that does not (a run).
const (
	enrolNewPIN = "380915"
	enrolBadPIN = "123456"
)

// svcCodeDigest: the digest the service stores a code under — HMAC with the
// test CodeKey (newSvc), as the service's own SMS codes.
func svcCodeDigest(code string) string {
	m := hmac.New(sha256.New, []byte("test-code-key-not-a-secret"))
	m.Write([]byte(code))
	return hex.EncodeToString(m.Sum(nil))
}

// enrollee: a person who can be issued a code. pin == "" means they have
// never had one.
type enrollee struct {
	user, member, number, pin, deviceToken string
}

// issueTo: a current treasurer of the same society issues `e` a code on their
// step-up. Returns the code's id and its text.
func (s *svcT) issueTo(e enrollee) (string, string) {
	s.t.Helper()
	officer := s.s.user("Treasurer (issuer)")
	om := s.s.member(officer)
	fixture(s.t, s.s.appoint(om, "treasurer", true, "2020-01-01", ""), "the treasurer")
	code, _ := slip()
	id, err := issueBy(s.t, issueSpec{actor: officer, actMember: e.member, user: e.user, member: e.member, digest: svcCodeDigest(code)})
	fixture(s.t, err, "an officer's code")
	return id, code
}

// newcomer: a registered member who has never had a PIN or a device.
func (s *svcT) newcomer(name string) enrollee {
	s.t.Helper()
	p := s.who(name)
	e := enrollee{user: p.user, member: p.member}
	fixture(s.t, db.QueryRow(bg(), `SELECT member_number FROM members WHERE id = $1`, p.member).Scan(&e.number), "member number")
	return e
}

// holder: a member with a PIN and a trusted device, enrolling another device.
func (s *svcT) holder(name string) enrollee {
	s.t.Helper()
	m := s.member(name)
	return enrollee{user: m.user, member: m.member, number: m.number, pin: m.pin, deviceToken: m.deviceToken}
}

func (s *svcT) enrol(cred, code, pin, newPIN string) (auth.Result, error) {
	return s.svc.Enrol(bg(), auth.EnrolRequest{Credential: cred, Code: code, PIN: pin, NewPIN: newPIN})
}

func pinHashOf(t *testing.T, user string) string {
	t.Helper()
	var h *string
	fixture(t, db.QueryRow(bg(), `SELECT pin_hash FROM auth_credentials WHERE user_id = $1`, user).Scan(&h), "PIN")
	if h == nil {
		return ""
	}
	return *h
}

func hasPINRow(t *testing.T, user string) bool {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM auth_credentials WHERE user_id = $1`, user).Scan(&n), "PIN rows")
	return n > 0
}

func attemptsOf(t *testing.T, user string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1`, user).Scan(&n), "attempts")
	return n
}

// wantLive: the code is exactly as issued — unused, unvoided, `tries` wrong tries.
func wantLive(t *testing.T, id string, tries int, what string) {
	t.Helper()
	c := codeState(t, id)
	switch {
	case c.used:
		t.Fatalf("SLIP SPENT — %s, and the code is now used: the member loses their slip for nothing", what)
	case c.voided:
		t.Fatalf("SLIP VOIDED — %s, and the code is void (%s)", what, c.voidReason)
	case c.tries != tries:
		t.Fatalf("TRIES WRONG — %s; the code shows %d wrong tries, want %d", what, c.tries, tries)
	}
}

// =============================================================================
// A FIRST ENROLMENT
// =============================================================================

// Someone who has never had a PIN redeems a right code with an allowed new
// PIN: signed in, the PIN set, the code spent and bound to the device handed back.
func TestRedeem01_AFirstEnrolmentSetsThePinAndTrustsTheDevice(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (first enrolment)")
	id, code := s.issueTo(e)
	r, err := s.enrol(e.number, code, "", enrolNewPIN)
	if err != nil {
		t.Fatalf("REFUSED — a right code with an allowed first PIN: %v", err)
	}
	if r.SessionToken == "" || r.DeviceToken == "" || r.Challenge != "" {
		t.Fatalf("NOT SIGNED IN — a first enrolment returned %+v; want a session and a device token, no challenge", r)
	}
	if h := pinHashOf(t, e.user); h == "" || !verifyPHC(h, enrolNewPIN) {
		t.Fatalf("NO PIN — the enrolment did not set the PIN the member chose")
	}
	var used bool
	var dev *string
	fixture(t, db.QueryRow(bg(), `SELECT c.used_at IS NOT NULL, d.token_digest FROM enrolment_codes c
		LEFT JOIN auth_devices d ON d.id = c.used_device_id WHERE c.id = $1`, id).Scan(&used, &dev), "the code")
	switch {
	case !used:
		t.Fatalf("SLIP LIVE — the code enrolled a device and is still unused")
	case dev == nil || *dev != auth.TokenDigest(r.DeviceToken):
		t.Fatalf("UNBOUND — the device token handed back is not the device the code names (0025)")
	}
}

// A first PIN goes through the same rule as any new PIN. A refused one sets
// nothing and leaves the slip live.
func TestRedeem02_ARefusedFirstPinLeavesTheCodeLive(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (weak PIN)")
	id, code := s.issueTo(e)
	if _, err := s.enrol(e.number, code, "", enrolBadPIN); !errors.Is(err, auth.ErrPINNotAllowed) {
		t.Fatalf("WRONG ANSWER — a first PIN of %s answered %v; want ErrPINNotAllowed (CheckNewPIN)", enrolBadPIN, err)
	}
	if hasPINRow(t, e.user) {
		t.Fatalf("PIN SET — a refused first PIN was stored")
	}
	wantLive(t, id, 0, "a first PIN was refused")
	if devicesOf(t, e.user) != 0 {
		t.Fatalf("DEVICE TRUSTED — a refused enrolment left a trusted device")
	}
}

// Someone with no PIN must choose one; until they do the code stays live.
func TestRedeem03_AMissingFirstPinLeavesTheCodeLive(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (no PIN chosen)")
	id, code := s.issueTo(e)
	if _, err := s.enrol(e.number, code, "", ""); !errors.Is(err, auth.ErrNeedsNewPIN) {
		t.Fatalf("WRONG ANSWER — a right code with no PIN chosen answered %v; want ErrNeedsNewPIN", err)
	}
	wantLive(t, id, 0, "no first PIN was chosen")
	if devicesOf(t, e.user) != 0 {
		t.Fatalf("DEVICE TRUSTED — an unfinished enrolment left a trusted device")
	}
}

// =============================================================================
// SOMEONE WHO ALREADY HAS A PIN — the code is the second factor, never a reset
// =============================================================================

// The right PIN and the right code: a new trusted device, the PIN unchanged,
// and the person's other devices told.
func TestRedeem04_AHolderEnrolsWithTheirPinAndIsTold(t *testing.T) {
	s := newSvc(t)
	e := s.holder("Member (new phone)")
	id, code := s.issueTo(e)
	before := pinHashOf(t, e.user)
	r, err := s.enrol(e.number, code, e.pin, "")
	if err != nil {
		t.Fatalf("REFUSED — a holder's right PIN and right code: %v", err)
	}
	if r.SessionToken == "" || r.DeviceToken == "" || r.DeviceToken == e.deviceToken {
		t.Fatalf("NOT SIGNED IN — a holder's enrolment returned %+v; want a session and a NEW device token", r)
	}
	if pinHashOf(t, e.user) != before {
		t.Fatalf("PIN CHANGED — an enrolment changed the PIN of someone who has one")
	}
	if !codeState(t, id).used {
		t.Fatalf("SLIP LIVE — the code enrolled a device and is still unused")
	}
	if len(s.notify.got) != 1 || s.notify.got[0].user != e.user {
		t.Fatalf("UNTOLD — a new device was trusted by slip and the member's other devices were not told: %+v", s.notify.got)
	}
}

// A wrong PIN: refused as at sign-in, counted against the lockout, and the
// code untouched — not spent, no try taken.
func TestRedeem05_AWrongPinNeverTouchesTheCode(t *testing.T) {
	s := newSvc(t)
	e := s.holder("Member (wrong PIN)")
	id, code := s.issueTo(e)
	before := s.failuresOf(e.user)
	if _, err := s.enrol(e.number, code, "0000", ""); !errors.Is(err, auth.ErrRefused) {
		t.Fatalf("WRONG ANSWER — a wrong PIN with a right code answered %v; want ErrRefused", err)
	}
	wantLive(t, id, 0, "the PIN was wrong")
	if s.failuresOf(e.user) != before+1 {
		t.Fatalf("UNCOUNTED — a wrong PIN at enrolment was not recorded against the account: a slip would be a free PIN oracle")
	}
}

// D-15: the code never stands in for a PIN reset. Someone who has a PIN and
// asks for a new one is refused, the PIN unchanged, the code live.
func TestRedeem06_ACodeNeverResetsAPin(t *testing.T) {
	s := newSvc(t)
	e := s.holder("Member (asks for a new PIN)")
	id, code := s.issueTo(e)
	before := pinHashOf(t, e.user)
	if _, err := s.enrol(e.number, code, e.pin, enrolNewPIN); !errors.Is(err, auth.ErrPINAlreadySet) {
		t.Fatalf("RESET BY SLIP — a holder asking for a new PIN with a code answered %v; want ErrPINAlreadySet (D-15)", err)
	}
	if pinHashOf(t, e.user) != before {
		t.Fatalf("RESET BY SLIP — the PIN was changed by an enrolment code (D-15)")
	}
	wantLive(t, id, 0, "a holder asked for a new PIN")
}

// =============================================================================
// THE CODE'S OWN TRIES
// =============================================================================

// A wrong code costs one of the code's tries, never a PIN try, and answers
// exactly as an unknown number does.
func TestRedeem07_AWrongCodeCostsACodeTryAndLooksLikeAnUnknownNumber(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (mistyped slip)")
	id, _ := s.issueTo(e)
	_, unknown := s.enrol(unregistered(), "ENROL-NOT-A-CODE", "", enrolNewPIN)
	if !errors.Is(unknown, auth.ErrRefused) {
		t.Fatalf("WRONG ANSWER — an unknown number answered %v; want ErrRefused", unknown)
	}
	_, err := s.enrol(e.number, "ENROL-NOT-A-CODE", "", enrolNewPIN)
	if err == nil || err.Error() != unknown.Error() || !errors.Is(err, auth.ErrRefused) {
		t.Fatalf("TELLS — a wrong code for a real member answered %v; an unknown number answered %v. They must be the same", err, unknown)
	}
	wantLive(t, id, 1, "one wrong code was typed")
	if n := attemptsOf(t, e.user); n != 0 {
		t.Fatalf("PIN TRY TAKEN — a wrong code wrote %d sign-in attempt(s): code tries never count against the PIN lockout", n)
	}
}

// A holder with the right PIN and a wrong code: refused, the code's try
// counted, no PIN failure, nothing trusted.
func TestRedeem08_ARightPinWithAWrongCodeEnrolsNothing(t *testing.T) {
	s := newSvc(t)
	e := s.holder("Member (right PIN, wrong slip)")
	id, _ := s.issueTo(e)
	devs, before := devicesOf(t, e.user), s.failuresOf(e.user)
	if _, err := s.enrol(e.number, "ENROL-NOT-A-CODE", e.pin, ""); !errors.Is(err, auth.ErrRefused) {
		t.Fatalf("WRONG ANSWER — a right PIN with a wrong code answered %v; want ErrRefused", err)
	}
	wantLive(t, id, 1, "a wrong code was typed with the right PIN")
	if devicesOf(t, e.user) != devs {
		t.Fatalf("DEVICE TRUSTED — a wrong code trusted a device")
	}
	if s.failuresOf(e.user) != before {
		t.Fatalf("PIN TRY TAKEN — a wrong code with the right PIN was counted as a PIN failure")
	}
}

// A locked account does not enrol, with the right PIN and the right code,
// and the code is untouched. An enrolment is always from a device that is not
// the member's own, so the lock it meets is that one (0022: the two locks
// never lock each other): five wrong PINs from a device that is not theirs.
func TestRedeem09_ALockedAccountDoesNotEnrol(t *testing.T) {
	s := newSvc(t)
	e := s.holder("Member (locked)")
	id, code := s.issueTo(e)
	for i := 0; i < 5; i++ {
		s.signIn(e.number, "0000", "")
	}
	if _, err := s.enrol(e.number, code, e.pin, ""); !errors.Is(err, auth.ErrTryLater) {
		t.Fatalf("LOCK BYPASSED — after five wrong PINs, an enrolment with the right PIN answered %v; want ErrTryLater", err)
	}
	wantLive(t, id, 0, "the account was locked")
}

// =============================================================================
// THE ACCEPTED RESIDUAL — the used slip says so, and the device signs in
// =============================================================================

// The person's own used slip, typed again: they are told, in the words
// approved, what to do. No try is taken.
func TestRedeem10_TheUsedSlipTellsItsHolderWhatToDo(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (slip taken)")
	id, code := s.issueTo(e)
	if _, err := s.enrol(e.number, code, "", enrolNewPIN); err != nil {
		t.Fatalf("FIXTURE: the first enrolment (the one that took the slip) answered %v", err)
	}
	_, err := s.enrol(e.number, code, enrolNewPIN, "")
	if !errors.Is(err, auth.ErrSlipUsed) || !strings.Contains(err.Error(), "Speak to your secretary") {
		t.Fatalf("SILENT — the person's own used slip answered %v; want ErrSlipUsed: \"This code has already been used. Speak to your secretary.\"", err)
	}
	if c := codeState(t, id); c.tries != 0 {
		t.Fatalf("TRY TAKEN — telling the holder their slip was used cost %d tries", c.tries)
	}
}

// The device token handed back is a device the server trusts: the PIN alone
// signs in on it afterwards, without an SMS code.
func TestRedeem11_TheEnrolledDeviceSignsInWithThePinAlone(t *testing.T) {
	s := newSvc(t)
	e := s.newcomer("Member (signs in again)")
	_, code := s.issueTo(e)
	r, err := s.enrol(e.number, code, "", enrolNewPIN)
	fixture(t, err, "the enrolment")
	again, err := s.signIn(e.number, enrolNewPIN, r.DeviceToken)
	if err != nil || again.SessionToken == "" || again.Challenge != "" {
		t.Fatalf("NOT TRUSTED — sign-in on the enrolled device answered %+v, %v; want a session with no challenge", again, err)
	}
}

// =============================================================================
// THE UNIFORM REFUSAL REACHES THE LOCK (2026-11-09)
// =============================================================================
// Whether a request counts depends on the request, never on the account.
// Carrying a PIN, it meets a lock and is counted, whoever the number names.
// Carrying none, it is never counted. Otherwise five wrong PINs would tell an
// enrolled number from an unknown one.

// Six requests with a wrong PIN, to an enrolled member, to a registered
// member who has no PIN, and to a number nobody holds: the same six answers,
// ending in a lock.
func TestRedeem12_AWrongPinLocksEveryNumberAlike(t *testing.T) {
	s := newSvc(t)
	holder := s.holder("Member (enrolled)")
	newcomer := s.newcomer("Member (no PIN yet)")
	answers := func(cred string) string {
		var got []string
		for i := 0; i < 6; i++ {
			_, err := s.enrol(cred, "ENROL-NOT-A-CODE", "0000", "")
			got = append(got, fmt.Sprint(err))
		}
		return strings.Join(got, " | ")
	}
	h, n, u := answers(holder.number), answers(newcomer.number), answers(unregistered())
	if !strings.HasSuffix(h, auth.ErrTryLater.Error()) {
		t.Fatalf("FIXTURE: an enrolled member was not locked after five wrong PINs at enrolment: %s", h)
	}
	if n != h || u != h {
		t.Fatalf("TELLS — five wrong PINs answer differently by who holds the number:\n  enrolled:      %s\n  no PIN yet:    %s\n  nobody:        %s", h, n, u)
	}
}

// A request with no PIN is never counted, for anyone, and a holder who gives
// none is refused with their code untouched.
func TestRedeem13_ARequestWithNoPinIsNeverCounted(t *testing.T) {
	s := newSvc(t)
	holder := s.holder("Member (forgot to type the PIN)")
	id, code := s.issueTo(holder)
	before := attemptsOf(t, holder.user)
	for i := 0; i < 6; i++ {
		if _, err := s.enrol(holder.number, code, "", ""); !errors.Is(err, auth.ErrRefused) {
			t.Fatalf("WRONG ANSWER — a holder's right code with no PIN answered %v on try %d; want ErrRefused", err, i+1)
		}
	}
	if n := attemptsOf(t, holder.user) - before; n != 0 {
		t.Fatalf("COUNTED — six requests with no PIN wrote %d sign-in attempt(s)", n)
	}
	wantLive(t, id, 0, "a holder gave no PIN")
	if _, err := s.enrol(holder.number, code, holder.pin, ""); err != nil {
		t.Fatalf("LOCKED — after six requests with no PIN, the right PIN and code answered %v", err)
	}
}
