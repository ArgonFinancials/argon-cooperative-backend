//go:build tier1

// Acceptance tests for the sign-in SERVICE (internal/auth). Written before the
// service, for review; they observe only its interface, the fake SMS and
// notifier, and what it writes to the database.
//
// Which implementation runs:
//   - default: auth.New — the real service (not yet written: every test fails
//     "NOT IMPLEMENTED", which is the honest state).
//   - ARGON_TEST_AUTH_FAKE=<break>: the mutation vehicle in authfake_test.go,
//     deliberately broken one way (or "" for none). See mutate.sh.
package tier1

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/argon/backend/internal/auth"
)

// ---- fakes the service is given ---------------------------------------------

type sentSMS struct{ to, text string }

type fakeSMS struct {
	mu   sync.Mutex
	sent []sentSMS
}

func (f *fakeSMS) Send(_ context.Context, to, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentSMS{to, text})
	return nil
}

var codeRE = regexp.MustCompile(`\b\d{6}\b`)

func (f *fakeSMS) lastCode(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatalf("FIXTURE: no SMS was sent")
	}
	c := codeRE.FindString(f.sent[len(f.sent)-1].text)
	if c == "" {
		t.Fatalf("FIXTURE: the SMS carried no six-digit code: %q", f.sent[len(f.sent)-1].text)
	}
	return c
}

type notice struct {
	user string
	at   time.Time
}

type fakeNotifier struct {
	mu  sync.Mutex
	got []notice
}

func (f *fakeNotifier) NewDeviceSignedIn(_ context.Context, user string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, notice{user, at})
	return nil
}

// Small but real argon2id cost: enough that a hash takes measurable time
// (the timing test depends on it), small enough for a constrained machine.
var testArgon = auth.Argon2{MemoryKiB: 8 * 1024, Time: 2, Threads: 1}

// hashPIN writes an argon2id PHC string — the format pin_is_argon2id checks.
func hashPIN(pin string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	h := argon2.IDKey([]byte(pin), salt, testArgon.Time, testArgon.MemoryKiB, testArgon.Threads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", testArgon.MemoryKiB, testArgon.Time, testArgon.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h))
}

// verifyPHC is shared by the fake. Constant-time compare.
func verifyPHC(phc, pin string) bool {
	var m, t uint32
	var p uint8
	parts := strings.Split(phc, "$")
	if len(parts) != 6 {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pin), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- harness ----------------------------------------------------------------

type svcT struct {
	*authT
	svc    auth.Service
	sms    *fakeSMS
	notify *fakeNotifier
}

func newSvc(t *testing.T) *svcT {
	t.Helper()
	a := newAuth(t)
	st := &svcT{authT: a, sms: &fakeSMS{}, notify: &fakeNotifier{}}
	deps := auth.Deps{DB: db, SMS: st.sms, Notify: st.notify, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon}
	if brk, ok := os.LookupEnv("ARGON_TEST_AUTH_FAKE"); ok {
		st.svc = newFakeService(deps, brk)
	} else {
		s, err := auth.New(deps)
		if err != nil {
			t.Fatalf("NOT IMPLEMENTED: %v", err)
		}
		st.svc = s
	}
	return st
}

// member: a registered person with a PIN and a device the server trusts.
type member struct {
	person
	number, pin, deviceToken string
}

func (s *svcT) member(name string) member {
	s.t.Helper()
	m := member{person: s.who(name), pin: "4827"}
	fixture(s.t, db.QueryRow(bg(), `SELECT member_number FROM members WHERE id = $1`, m.member).Scan(&m.number), "member number")
	fixture(s.t, s.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, m.user, hashPIN(m.pin)), "PIN")
	m.deviceToken = fmt.Sprintf("device-%d", seq.Add(1))
	fixture(s.t, s.exec(`INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2)`, m.user, auth.TokenDigest(m.deviceToken)), "trusted device")
	return m
}

func (s *svcT) signIn(cred, pin, device string) (auth.Result, error) {
	return s.svc.SignIn(bg(), auth.SignInRequest{Credential: cred, PIN: pin, DeviceToken: device})
}

// unregistered: a fresh number that matches no account. Fresh per call —
// tests share a database, and a shared number would carry one test's failed
// attempts into another's lockout count.
func unregistered() string { return fmt.Sprintf("+23489%08d", seq.Add(1)) }

// =============================================================================
// UNIFORM REFUSAL — content, lockout and timing
// =============================================================================

func TestAuthS1_UnknownNumberAndWrongPinRefuseIdentically(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r1, e1 := s.signIn(unregistered(), "4827", "")
	r2, e2 := s.signIn(m.phone, "0000", m.deviceToken)
	if e1 == nil || e2 == nil {
		t.Fatalf("ACCEPTED — a sign-in with an unregistered number or a wrong PIN succeeded (%v, %v)", e1, e2)
	}
	if e1.Error() != e2.Error() || r1 != r2 {
		t.Fatalf("ACCEPTED — the refusal says which half was wrong:\n  unregistered number: %q %+v\n  wrong PIN:           %q %+v", e1, r1, e2, r2)
	}
}

// If only real accounts lock, the sixth attempt answers "is this number
// registered?" — the uniform refusal would prove nothing.
func TestAuthS2_LockoutLooksTheSameForUnknownNumbers(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	var real, fake []string
	nobody := unregistered()
	for i := 0; i < 6; i++ {
		_, e := s.signIn(m.phone, "0000", m.deviceToken)
		real = append(real, fmt.Sprint(e))
		_, e = s.signIn(nobody, "0000", "")
		fake = append(fake, fmt.Sprint(e))
	}
	if real[5] != auth.ErrTryLater.Error() {
		t.Fatalf("FIXTURE: the registered account was not locked after five wrong PINs (%q) — the comparison would prove nothing", real[5])
	}
	if strings.Join(real, "|") != strings.Join(fake, "|") {
		t.Fatalf("ACCEPTED — lockout reveals registration:\n  registered:   %v\n  unregistered: %v", real, fake)
	}
}

// Same answer, different time, still tells an attacker which numbers are
// registered: a real account hashes a PIN; an unknown one must cost the same.
func TestAuthS3_RefusalTimingDoesNotRevealRegistration(t *testing.T) {
	s := newSvc(t)
	var known, unknown []time.Duration
	for i := 0; i < 11; i++ {
		m := s.member(fmt.Sprintf("Member %d", i)) // fresh account each time: no lockout interferes
		t0 := time.Now()
		s.signIn(m.phone, "0000", m.deviceToken)
		known = append(known, time.Since(t0))
		t0 = time.Now()
		s.signIn(unregistered(), "0000", "")
		unknown = append(unknown, time.Since(t0))
	}
	med := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	k, u := med(known), med(unknown)
	if ratio := float64(u) / float64(k); ratio < 0.5 || ratio > 2 {
		t.Fatalf("ACCEPTED — refusal time reveals registration: wrong PIN median %s, unregistered number median %s (ratio %.2f, want 0.5–2)",
			k.Round(time.Microsecond), u.Round(time.Microsecond), ratio)
	}
}

// =============================================================================
// DEVICES AND CODES
// =============================================================================

func TestAuthS4_KnownDeviceSignsInWithPinAlone(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r, err := s.signIn(m.number, m.pin, m.deviceToken)
	accepted(t, err, "right PIN on a device the server trusts")
	if r.SessionToken == "" || r.Challenge != "" || len(s.sms.sent) != 0 {
		t.Fatalf("REFUSED — a known device did not sign in on the PIN alone: %+v, %d SMS sent", r, len(s.sms.sent))
	}
}

func TestAuthS5_NewDeviceNeedsACodeToTheRegisteredPhone(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r, err := s.signIn(m.number, m.pin, "")
	accepted(t, err, "right PIN on a new device")
	if r.SessionToken != "" {
		t.Fatalf("ACCEPTED — a new device was signed in on the PIN alone")
	}
	if r.Challenge == "" || len(s.sms.sent) != 1 || s.sms.sent[0].to != m.phone {
		t.Fatalf("REFUSED — no code was sent to the registered phone (%s): %+v, sent %+v", m.phone, r, s.sms.sent)
	}
}

// "The code goes to the phone, not to the screen": a factor delivered to the
// device already in hand is not a second factor.
func TestAuthS6_CodeNeverAppearsInAResponse(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r, err := s.signIn(m.number, m.pin, "")
	fixture(t, err, "new-device sign-in")
	code := s.sms.lastCode(t)
	j, _ := json.Marshal(r)
	if strings.Contains(string(j), code) {
		t.Fatalf("ACCEPTED — the code %s is in the sign-in response: %s", code, j)
	}
	r2, err := s.svc.VerifyCode(bg(), r.Challenge, code)
	accepted(t, err, "the right code")
	j2, _ := json.Marshal(r2)
	if r2.SessionToken == "" || strings.Contains(string(j2), code) {
		t.Fatalf("code verification response wrong (signed in: %v, carries code: %v)", r2.SessionToken != "", strings.Contains(string(j2), code))
	}
}

func TestAuthS7_OtherDevicesAreToldOfANewOne(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r, err := s.signIn(m.number, m.pin, "")
	fixture(t, err, "new-device sign-in")
	_, err = s.svc.VerifyCode(bg(), r.Challenge, s.sms.lastCode(t))
	fixture(t, err, "code")
	if len(s.notify.got) != 1 || s.notify.got[0].user != m.user {
		t.Fatalf("ACCEPTED — a new device signed in and the member's other devices were not told: %+v", s.notify.got)
	}
}

// A device is known only by a token the SERVER issued. A client claiming to
// be a known device is a new device.
func TestAuthS8_ClientAssertedDeviceTokenIsANewDevice(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	r, err := s.signIn(m.number, m.pin, "i-am-a-known-device")
	accepted(t, err, "right PIN, invented device token")
	if r.SessionToken != "" {
		t.Fatalf("ACCEPTED — an invented device token skipped the new-device code")
	}
}

// Lockout counts per ACCOUNT: alternating the phone number and the member
// number must not give an attacker two counters.
func TestAuthS9_CredentialFormsShareOneCount(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	for i := 0; i < 5; i++ {
		cred := m.phone
		if i%2 == 1 {
			cred = m.number
		}
		s.signIn(cred, "0000", m.deviceToken)
	}
	_, err := s.signIn(m.number, m.pin, m.deviceToken)
	if err == nil || err.Error() != auth.ErrTryLater.Error() {
		t.Fatalf("ACCEPTED — five wrong PINs split across phone and member number did not lock the account (%v)", err)
	}
}

// The duplicate record again, now at the service: no single registered
// number, so no code — the path is an officer, in person.
func TestAuthS10_NoAgreedNumberRoutesToAnOfficer(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	s.record(m.user, unregistered())
	r, err := s.signIn(m.number, m.pin, "")
	accepted(t, err, "right PIN on a new device")
	if len(s.sms.sent) != 0 || !r.SeeOfficer || r.SessionToken != "" {
		t.Fatalf("ACCEPTED — with two numbers on record the service did not route to an officer: %+v, sent %+v", r, s.sms.sent)
	}
}

// =============================================================================
// D-21 — A CREDENTIAL RESOLVES TO EXACTLY ONE PERSON, OR NOBODY IS SIGNED IN
//
// A shared family number (common in this market) or a member number that
// exists in two societies must not sign in whichever record is found first.
// "We cannot tell who you are" takes the same path as "we have no way to reach
// you": no code, no session, an officer.
// =============================================================================

// elsewhere: another person, in ANOTHER society, with the given phone and
// member number, and the same PIN — so the PIN cannot tell them apart either.
func (s *svcT) elsewhere(phone, number string) string {
	s.t.Helper()
	other := newSociety(s.t)
	var u, m string
	fixture(s.t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Other society member') RETURNING id`).Scan(&u), "user")
	fixture(s.t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, 'Member', $4, 'cash', '2020-01-01', $5) RETURNING id`, other.id, other.branch, number, phone, u).Scan(&m), "member elsewhere")
	fixture(s.t, s.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, u, hashPIN("4827")), "PIN")
	return u
}

func (s *svcT) wantNobodySignedIn(r auth.Result, what string) {
	s.t.Helper()
	if r.SessionToken != "" || len(s.sms.sent) != 0 || !r.SeeOfficer {
		s.t.Fatalf("ACCEPTED — %s: signed in %v, %d SMS sent, routed to an officer %v (want: nobody signed in, no SMS, an officer)",
			what, r.SessionToken != "", len(s.sms.sent), r.SeeOfficer)
	}
}

func TestAuthS11_SharedPhoneSignsNobodyIn(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	s.elsewhere(m.phone, fmt.Sprintf("X-%d", seq.Add(1))) // a family member in another society, same phone
	r, err := s.signIn(m.phone, m.pin, m.deviceToken)
	accepted(t, err, "a right PIN on a shared phone number")
	s.wantNobodySignedIn(r, "a phone number shared by two people in two societies")
}

func TestAuthS11_SharedMemberNumberSignsNobodyIn(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	s.elsewhere(unregistered(), m.number) // the same member number, in another society
	r, err := s.signIn(m.number, m.pin, m.deviceToken)
	accepted(t, err, "a right PIN on a member number that exists in two societies")
	s.wantNobodySignedIn(r, "a member number held by two people in two societies")
}

// The ambiguous case must not become its own tell: a wrong PIN on a shared
// number answers exactly as a wrong PIN anywhere.
func TestAuthS11_SharedPhoneWrongPinRefusesLikeAnyOther(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	s.elsewhere(m.phone, fmt.Sprintf("X-%d", seq.Add(1)))
	r1, e1 := s.signIn(m.phone, "0000", m.deviceToken)
	r2, e2 := s.signIn(unregistered(), "0000", "")
	if e1 == nil || e2 == nil || e1.Error() != e2.Error() || r1 != r2 {
		t.Fatalf("ACCEPTED — a wrong PIN on a shared number answers differently from an unregistered number: %q %+v vs %q %+v", e1, r1, e2, r2)
	}
}

// One PERSON with records in two societies is one account, not an ambiguity.
func TestAuthS11_Control_OnePersonTwoSocietiesIsOneAccount(t *testing.T) {
	s := newSvc(t)
	m := s.member("Member")
	other := newSociety(t)
	fixture(t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, 'Member', $4, 'cash', '2020-01-01', $5) RETURNING id`, other.id, other.branch, fmt.Sprintf("Y-%d", seq.Add(1)), m.phone, m.user).Scan(new(string)),
		"the same person's record in a second society")
	r, err := s.signIn(m.phone, m.pin, m.deviceToken)
	accepted(t, err, "one person, records in two societies, same phone, known device")
	if r.SessionToken == "" {
		t.Fatalf("REFUSED — one person with records in two societies was treated as two people: %+v", r)
	}
}
