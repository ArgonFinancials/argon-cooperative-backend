//go:build tier1

// ACCEPTANCE TESTS — sign-in over HTTP: POST /auth/sign-in, /auth/codes/verify,
// /auth/sign-out, and the refusing SMS adapter the binary runs with until a
// provider is procured. Written before the handlers, for review.
//
// The service already carries these rules and its own tests (TestAuthS*); an
// HTTP layer can still undo each one — answer an unknown number differently,
// add a lookup that leaks timing, sign in a shared credential, or let the
// client's surface set the session's length. These prove them end to end, as
// a client meets them. The user's four (2026-10-02):
//
//  1. a session is the only thing that makes anything reachable: refusals
//     tested as hard as successes (02–05);
//  2. the uniform refusal holds in TIME, not only in content (06, 07);
//  3. a phone shared across societies signs in neither person (08);
//  4. the session follows the highest privilege held, not the surface (09).
//
// And the adapter: it refuses LOUDLY, never succeeds silently, and says it is
// temporary (10, 11).
//
// Which handlers run: default api.NewServer with auth.New; with
// ARGON_TEST_API_FAKE=<break>, the fake in apifake_signin_test.go.
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

// ---- servers ----------------------------------------------------------------

// authServer: the API with an auth service whose SMS is sms, its clock
// `ahead` of now (0: now; later: "the same server, later"). The real server
// runs on the roles production gives it (roles_test.go); a fake on the owner.
func authServer(t *testing.T, sms auth.SMS, ahead time.Duration) *httptest.Server {
	t.Helper()
	deps := auth.Deps{SMS: sms, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon}
	var h http.Handler
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		pool := db
		if ahead != 0 {
			pool = poolAt(t, ahead)
		}
		deps.DB = pool
		h = newFakeAPIWith(&fakeAPI{db: pool, brk: brk, deps: deps})
	} else {
		app, authp := rolePools(t, ahead)
		deps.DB = authp
		svc, err := auth.New(deps)
		fixture(t, err, "the auth service")
		h, err = api.NewServer(api.Deps{DB: app, Auth: svc, CSRFKey: testCSRFKey})
		fixture(t, err, "the API")
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// poolAt: the test database, with auth_now() set `ahead` into the future —
// "the same server, later". Sessions cannot be aged by editing them (0003:
// a session changes only by ending), so time moves instead. Honoured only
// because the test database enables the test clock (TestAuth0).
func poolAt(t *testing.T, ahead time.Duration) *pgxpool.Pool {
	t.Helper()
	cfg := db.Config().Copy()
	cfg.ConnConfig.RuntimeParams["argon.test_now"] = time.Now().Add(ahead).UTC().Format(time.RFC3339Nano)
	p, err := pgxpool.NewWithConfig(bg(), cfg)
	fixture(t, err, "a pool with the clock ahead")
	t.Cleanup(p.Close)
	return p
}

// ---- people -----------------------------------------------------------------

const signPIN = "4827"

type signer struct{ user, member, number, phone, device string }

// newSigner: a person in s with a PIN and a device the server trusts, holding
// post (signatory or not) if one is named.
func newSigner(t *testing.T, s *soc, name, post string, signatory bool) signer {
	t.Helper()
	var p signer
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, name).Scan(&p.user), "user")
	p.member = s.member(p.user)
	fixture(t, db.QueryRow(bg(), `SELECT member_number, phone FROM members WHERE id = $1`, p.member).Scan(&p.number, &p.phone), "member number")
	fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, p.user, hashPIN(signPIN)), "PIN")
	p.device = "device-" + digest()
	fixture(t, one(`INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2)`, p.user, auth.TokenDigest(p.device)), "trusted device")
	if post != "" {
		fixture(t, s.appoint(p.member, post, signatory, "2020-01-01", ""), post)
	}
	return p
}

func sessionsOf(t *testing.T, users ...string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM auth_sessions WHERE user_id = ANY($1::uuid[])`, users).Scan(&n), "sessions")
	return n
}

// ---- HTTP -------------------------------------------------------------------

func postJSON(t *testing.T, srv *httptest.Server, path, token string, body any) resp {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, "POST "+path)
	defer r.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	return resp{r.StatusCode, buf.Bytes()}
}

type signInOut struct {
	Outcome      string  `json:"outcome"`
	SessionToken *string `json:"sessionToken"`
	DeviceToken  *string `json:"deviceToken"`
	ExpiresAt    *string `json:"expiresAt"`
	Challenge    *string `json:"challenge"`
	Reason       *string `json:"reason"`
	Message      *string `json:"message"`
}

func signInAs(t *testing.T, srv *httptest.Server, credential, pin, device, surface string) resp {
	t.Helper()
	body := map[string]any{"credential": credential, "pin": pin}
	if device != "" {
		body["deviceToken"] = device
	}
	if surface != "" {
		body["surface"] = surface
	}
	return postJSON(t, srv, "/auth/sign-in", "", body)
}

func outcome(t *testing.T, r resp, want string) signInOut {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("WRONG STATUS — want 200 %s, got %d: %s", want, r.status, r.body)
	}
	var o signInOut
	if err := json.Unmarshal(r.body, &o); err != nil {
		t.Fatalf("WRONG SHAPE — not a sign-in result: %v: %s", err, r.body)
	}
	if o.Outcome != want {
		t.Fatalf("WRONG OUTCOME — want %s, got %s: %s", want, o.Outcome, r.body)
	}
	return o
}

func signedIn(t *testing.T, r resp) (string, time.Time) {
	t.Helper()
	o := outcome(t, r, "signed_in")
	if o.SessionToken == nil || *o.SessionToken == "" || o.ExpiresAt == nil {
		t.Fatalf("WRONG SHAPE — signed in without a token or an expiry: %s", r.body)
	}
	at, err := time.Parse(time.RFC3339Nano, *o.ExpiresAt)
	if err != nil {
		t.Fatalf("WRONG SHAPE — expiresAt %q: %v", *o.ExpiresAt, err)
	}
	return *o.SessionToken, at
}

func statementOf(t *testing.T, srv *httptest.Server, member, token string) resp {
	return get(t, srv, "/members/"+member+"/statement", token)
}

// =============================================================================
// 1 — A SESSION IS THE ONLY THING THAT MAKES ANYTHING REACHABLE
// =============================================================================

func TestApiSignIn01_KnownDeviceSignsInAndReachesTheMembersOwnStatement(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	tok, _ := signedIn(t, signInAs(t, srv, p.number, signPIN, p.device, "member"))
	if r := statementOf(t, srv, p.member, tok); r.status != http.StatusOK {
		t.Fatalf("UNREACHABLE — the new session could not read the member's own statement: %d %s", r.status, r.body)
	}
}

func TestApiSignIn02_WithoutASessionNothingIsReachable(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	s := newSociety(t)
	p := newSigner(t, s, "Member", "", false)
	for _, token := range []string{"", "not-a-session-" + digest()} {
		for what, r := range map[string]resp{
			"a statement":     statementOf(t, srv, p.member, token),
			"an import":       postJSON(t, srv, "/societies/"+s.id+"/ledger/batches", token, map[string]any{"kind": "opening_balance"}),
			"the PIN step-up": postJSON(t, srv, "/auth/pin-proofs", token, map[string]any{"pin": signPIN, "act": "ledger_batch"}),
			"sign-out":        postJSON(t, srv, "/auth/sign-out", token, nil),
		} {
			if r.status != http.StatusUnauthorized {
				t.Fatalf("REACHED — %s with token %q answered %d, want 401: %s", what, token, r.status, r.body)
			}
		}
	}
}

func TestApiSignIn03_SignedOutSessionReachesNothing(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	tok, _ := signedIn(t, signInAs(t, srv, p.number, signPIN, p.device, ""))
	if r := postJSON(t, srv, "/auth/sign-out", tok, nil); r.status != http.StatusNoContent {
		t.Fatalf("WRONG STATUS — sign-out answered %d, want 204: %s", r.status, r.body)
	}
	if r := statementOf(t, srv, p.member, tok); r.status != http.StatusUnauthorized {
		t.Fatalf("REACHED — a signed-out session still read the statement: %d", r.status)
	}
	if r := postJSON(t, srv, "/auth/sign-out", tok, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("REACHED — a signed-out session signed out again: %d", r.status)
	}
}

// A member's session lasts 30 days: reachable at day 29, not at day 31.
func TestApiSignIn04_ExpiredSessionReachesNothing(t *testing.T) {
	p := newSigner(t, newSociety(t), "Member", "", false)
	tok, _ := signedIn(t, signInAs(t, authServer(t, &fakeSMS{}, 0), p.number, signPIN, p.device, ""))
	if r := statementOf(t, authServer(t, &fakeSMS{}, 29*24*time.Hour), p.member, tok); r.status != http.StatusOK {
		t.Fatalf("FIXTURE: a member's session did not reach day 29: %d %s", r.status, r.body)
	}
	if r := statementOf(t, authServer(t, &fakeSMS{}, 31*24*time.Hour), p.member, tok); r.status != http.StatusUnauthorized {
		t.Fatalf("REACHED — a member's session still worked at day 31: %d", r.status)
	}
}

// A valid session, for the wrong society: as if the society did not exist.
func TestApiSignIn05_SessionDoesNotReachAnotherSociety(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	here, there := newSociety(t), newSociety(t)
	p := newSigner(t, here, "Secretary", "secretary", true)
	theirs := there.member("")
	tok, _ := signedIn(t, signInAs(t, srv, p.number, signPIN, p.device, "officer"))
	a, none := statementOf(t, srv, theirs, tok), statementOf(t, srv, noSuchRow, tok)
	if a.status != http.StatusNotFound || !bytes.Equal(a.body, none.body) {
		t.Fatalf("REACHED — another society's member answered %d %s; a nonexistent one %s", a.status, a.body, none.body)
	}
	// A complete, well-formed import (key and all), so only the society can refuse it.
	b, err := postBatch(t, http.DefaultClient, srv, there.id, newKey(), tok, file("2025-12-31", []string{theirs}, 10))
	fixture(t, err, "import into another society")
	nb, err := postBatch(t, http.DefaultClient, srv, noSuchRow, newKey(), tok, file("2025-12-31", []string{theirs}, 10))
	fixture(t, err, "import into no society")
	if b.status != http.StatusNotFound || !bytes.Equal(b.body, nb.body) {
		t.Fatalf("REACHED — an import into another society answered %d %s; into none %s", b.status, b.body, nb.body)
	}
}

// =============================================================================
// 2 — THE UNIFORM REFUSAL, IN CONTENT AND IN TIME
// =============================================================================

func TestApiSignIn06_UnknownCredentialAndWrongPinAnswerIdentically(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	a := signInAs(t, srv, unregistered(), "0000", "", "")
	b := signInAs(t, srv, p.number, "0000", p.device, "")
	if a.status != http.StatusUnauthorized || a.status != b.status || !bytes.Equal(a.body, b.body) {
		t.Fatalf("DISCLOSED — an unregistered number answered %d %s; a wrong PIN %d %s", a.status, a.body, b.status, b.body)
	}
}

// The identical answer proves nothing if the real account takes measurably
// longer because it hashed a PIN: the timing says which numbers exist.
func TestApiSignIn07_RefusalTimingDoesNotRevealRegistration(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	s := newSociety(t)
	var known, unknown []time.Duration
	for i := 0; i < 11; i++ {
		p := newSigner(t, s, "Member", "", false) // fresh each time: no lockout interferes
		t0 := time.Now()
		signInAs(t, srv, p.number, "0000", p.device, "")
		known = append(known, time.Since(t0))
		t0 = time.Now()
		signInAs(t, srv, unregistered(), "0000", "", "")
		unknown = append(unknown, time.Since(t0))
	}
	med := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	k, u := med(known), med(unknown)
	if ratio := float64(u) / float64(k); ratio < 0.5 || ratio > 2 {
		t.Fatalf("DISCLOSED — over HTTP, a wrong PIN takes %s (median) and an unregistered number %s: ratio %.2f, want 0.5–2",
			k.Round(time.Microsecond), u.Round(time.Microsecond), ratio)
	}
}

// =============================================================================
// 3 — A CREDENTIAL SHARED BY TWO PEOPLE SIGNS IN NEITHER
// =============================================================================

// Shared family phones are common: two members, two societies, one number.
// The right PIN for either routes to an officer; nobody is signed in.
func TestApiSignIn08_SharedPhoneAcrossSocietiesSignsNobodyIn(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	a := newSigner(t, newSociety(t), "Parent", "", false)
	there := newSociety(t)
	var bUser, bMember string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Child') RETURNING id`).Scan(&bUser), "second person")
	fixture(t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, 'Child', $4, 'cash', '2020-01-01', $5) RETURNING id`, there.id, there.branch, "SH-"+digest()[:8], a.phone, bUser).Scan(&bMember), "the same phone, another society")
	fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, bUser, hashPIN(signPIN)), "the second person's PIN")
	outcome(t, signInAs(t, srv, a.phone, signPIN, a.device, ""), "see_officer")
	if n := sessionsOf(t, a.user, bUser); n != 0 {
		t.Fatalf("SIGNED IN — a phone shared by two people signed in %d session(s)", n)
	}
	wrong, unknown := signInAs(t, srv, a.phone, "0000", a.device, ""), signInAs(t, srv, unregistered(), "0000", "", "")
	if wrong.status != unknown.status || !bytes.Equal(wrong.body, unknown.body) {
		t.Fatalf("DISCLOSED — a wrong PIN on a shared phone answered %d %s; an unknown number %d %s", wrong.status, wrong.body, unknown.status, unknown.body)
	}
}

// =============================================================================
// 4 — THE SESSION FOLLOWS THE PERSON, NOT THE SURFACE
// =============================================================================

// An approver is also a member. Signing in on the member surface must not
// grant a member's 30 days: 8 hours, as for any post held.
func TestApiSignIn09_ApproversSessionIsEightHoursOnAnySurface(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	s := newSociety(t)
	approver := newSigner(t, s, "Treasurer", "treasurer", true)
	member := newSigner(t, s, "Member", "", false)
	at, aExp := signedIn(t, signInAs(t, srv, approver.number, signPIN, approver.device, "member"))
	mt, mExp := signedIn(t, signInAs(t, srv, member.number, signPIN, member.device, "member"))
	if d := time.Until(aExp); d > 8*time.Hour+2*time.Minute || d < 7*time.Hour {
		t.Fatalf("OVERLONG — the approver, signing in on the member surface, was told the session lasts %s; want 8 hours", d.Round(time.Minute))
	}
	if d := time.Until(mExp); d < 29*24*time.Hour {
		t.Fatalf("FIXTURE: a plain member's session was reported to last %s, not 30 days", d.Round(time.Hour))
	}
	later := authServer(t, &fakeSMS{}, 9*time.Hour)
	if r := statementOf(t, later, approver.member, at); r.status != http.StatusUnauthorized {
		t.Fatalf("OVERLONG — the approver's member-surface session still worked after 9 hours: %d", r.status)
	}
	if r := statementOf(t, later, member.member, mt); r.status != http.StatusOK {
		t.Fatalf("FIXTURE: a plain member's session did not survive 9 hours: %d %s", r.status, r.body)
	}
}

// =============================================================================
// NO SMS PROVIDER YET: THE REFUSING ADAPTER
// =============================================================================

// testRefusingSMS stands in for auth.UnavailableSMS under the fake, breakable:
//
//	sms_adapter_silent       the send reports success and delivers nothing
//	sms_adapter_quiet        it refuses, but logs nothing
//	sms_adapter_sounds_final its refusal does not say it is temporary
type testRefusingSMS struct {
	brk string
	log *slog.Logger
}

func (s testRefusingSMS) Send(context.Context, string, string) (auth.Sent, error) {
	if s.brk == "sms_adapter_silent" {
		return auth.Sent{}, nil
	}
	var err error = auth.ErrSMSUnavailable
	if s.brk == "sms_adapter_sounds_final" {
		err = finalSoundingRefusal{}
	}
	if s.brk != "sms_adapter_quiet" {
		s.log.Error("refused to send an SMS", "err", err)
	}
	return auth.Sent{}, err
}

// finalSoundingRefusal IS ErrSMSUnavailable (callers still route it to an
// officer) but reads as the design, not as a temporary state.
type finalSoundingRefusal struct{}

func (finalSoundingRefusal) Error() string        { return "auth: this deployment does not send SMS codes" }
func (finalSoundingRefusal) Is(target error) bool { return target == auth.ErrSMSUnavailable }

// refusingSMS: the adapter under test — the real one, or under the fake its
// stand-in — logging into buf.
func refusingSMS(buf *bytes.Buffer) auth.SMS {
	log := slog.New(slog.NewTextHandler(buf, nil))
	if brk, ok := os.LookupEnv("ARGON_TEST_API_FAKE"); ok {
		return testRefusingSMS{brk: brk, log: log}
	}
	return auth.UnavailableSMS{Log: log}
}

func TestApiSignIn10_NewDeviceWithoutSMSGoesToAnOfficerAndSaysWhy(t *testing.T) {
	var logs bytes.Buffer
	srv := authServer(t, refusingSMS(&logs), 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	o := outcome(t, signInAs(t, srv, p.number, signPIN, "", ""), "see_officer") // no device token: a new device
	// The reason is for the client (machine-readable); the MESSAGE IS FOR THE
	// MEMBER (decided 2026-10-22: any field a client can show, some client
	// will). It says what happened and what to do: a new device, confirmed by
	// an officer of their society. Nothing about SMS, a provider, or what is
	// temporary: that is our procurement, not their next step. (Until
	// 2026-10-22 this test required the opposite, the word "temporary".)
	if o.Reason == nil || *o.Reason != "sms_unavailable" || o.Message == nil {
		t.Fatalf("UNEXPLAINED — a new device without SMS was routed to an officer without a reason and a message: %+v", o)
	}
	m := strings.ToLower(*o.Message)
	if !strings.Contains(m, "new device") || !strings.Contains(m, "officer") {
		t.Fatalf("NO NEXT STEP — the member is not told it is a new device and that an officer confirms it: %q", *o.Message)
	}
	for _, dev := range []string{"sms", "provider", "temporar"} {
		if strings.Contains(m, dev) {
			t.Fatalf("WRITTEN FOR DEVELOPERS — the member's message mentions %q: %q", dev, *o.Message)
		}
	}
	if n := sessionsOf(t, p.user); n != 0 {
		t.Fatalf("SIGNED IN — %d session(s) on a new device no code could confirm", n)
	}
}

// The adapter itself: a refusal, never a success that delivers nothing; an
// Error-level line every time; and it says it is temporary.
func TestApiSignIn11_RefusingSMSRefusesLoudlyAndSaysItIsTemporary(t *testing.T) {
	var logs bytes.Buffer
	_, err := refusingSMS(&logs).Send(bg(), "+2348000000000", "Your Argon code is 123456.")
	switch {
	case err == nil:
		t.Fatalf("SILENT — the send reported success and delivered nothing: the worst outcome available")
	case !errors.Is(err, auth.ErrSMSUnavailable):
		t.Fatalf("WRONG REFUSAL — %v is not ErrSMSUnavailable, so callers cannot route it to an officer", err)
	case !strings.Contains(strings.ToLower(err.Error()), "temporary"):
		t.Fatalf("PERMANENT-SOUNDING — the refusal %q does not say it is temporary; someone will read it as the design", err)
	case !strings.Contains(logs.String(), "level=ERROR"):
		t.Fatalf("QUIET — the refusal logged nothing at Error level: %q", logs.String())
	}
}

// =============================================================================
// THE NEW-DEVICE CODE, AND LOCKOUT, OVER HTTP
// =============================================================================

func TestApiSignIn12_NewDeviceCodeCompletesSignIn(t *testing.T) {
	sms := &fakeSMS{}
	srv := authServer(t, sms, 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	o := outcome(t, signInAs(t, srv, p.number, signPIN, "", ""), "code_sent")
	if o.Challenge == nil || strings.Contains(*o.Challenge, sms.lastCode(t)) {
		t.Fatalf("WRONG SHAPE — the challenge is missing or carries the code: %+v", o)
	}
	if r := postJSON(t, srv, "/auth/codes/verify", "", map[string]string{"challenge": *o.Challenge, "code": "000000"}); r.status != http.StatusUnauthorized {
		t.Fatalf("ACCEPTED — a wrong code answered %d: %s", r.status, r.body)
	}
	r := postJSON(t, srv, "/auth/codes/verify", "", map[string]string{"challenge": *o.Challenge, "code": sms.lastCode(t)})
	tok, _ := signedIn(t, r)
	var v signInOut
	json.Unmarshal(r.body, &v)
	if v.DeviceToken == nil || *v.DeviceToken == "" {
		t.Fatalf("UNTRUSTED — the confirmed device was not given a token for next time: %s", r.body)
	}
	if r := statementOf(t, srv, p.member, tok); r.status != http.StatusOK {
		t.Fatalf("UNREACHABLE — the confirmed session could not read the statement: %d", r.status)
	}
}

func TestApiSignIn13_LockedAccountIsTryLaterEvenWithTheRightPin(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	p := newSigner(t, newSociety(t), "Member", "", false)
	for i := 0; i < 5; i++ {
		signInAs(t, srv, p.number, "0000", p.device, "")
	}
	if r := signInAs(t, srv, p.number, signPIN, p.device, ""); r.status != http.StatusTooManyRequests {
		t.Fatalf("ORACLE — after five wrong PINs the right one answered %d %s; want 429", r.status, r.body)
	}
	if n := sessionsOf(t, p.user); n != 0 {
		t.Fatalf("SIGNED IN — a locked account got %d session(s)", n)
	}
}

// =============================================================================
// ONE ERROR FORMAT
// =============================================================================

// Requests the generated router refuses before any handler runs (a missing
// required header, a body that is not JSON, a path parameter that is not a
// uuid) answer in the same JSON Problem as every other refusal. Two error
// formats is two contracts: the frontend parses one and breaks on the other.
func TestApiSignIn14_MalformedRequestsAnswerTheOneErrorFormat(t *testing.T) {
	srv := authServer(t, &fakeSMS{}, 0)
	s := newSociety(t)
	p := newSigner(t, s, "Secretary", "secretary", true)
	tok, _ := signedIn(t, signInAs(t, srv, p.number, signPIN, p.device, ""))
	send := func(method, path, token, contentType, body string) (*http.Response, []byte) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		r, err := http.DefaultClient.Do(req)
		fixture(t, err, method+" "+path)
		defer r.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		return r, buf.Bytes()
	}
	for what, call := range map[string]func() (*http.Response, []byte){
		"an import without its Idempotency-Key": func() (*http.Response, []byte) {
			return send(http.MethodPost, "/societies/"+s.id+"/ledger/batches", tok, "application/json", `{"kind":"opening_balance","asOf":null,"confirmedTotalKobo":"1","rows":[]}`)
		},
		"a sign-in whose body is not JSON": func() (*http.Response, []byte) {
			return send(http.MethodPost, "/auth/sign-in", "", "application/json", `{not json`)
		},
		"a statement for a member id that is not a uuid": func() (*http.Response, []byte) {
			return send(http.MethodGet, "/members/not-a-uuid/statement", tok, "", "")
		},
	} {
		r, body := call()
		var pb struct{ Code, Message string }
		if r.StatusCode != http.StatusBadRequest || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") ||
			json.Unmarshal(body, &pb) != nil || pb.Code == "" || pb.Message == "" {
			t.Fatalf("TWO FORMATS — %s answered %d %q %q; want 400 application/json {code, message}", what, r.StatusCode, r.Header.Get("Content-Type"), body)
		}
	}
}
