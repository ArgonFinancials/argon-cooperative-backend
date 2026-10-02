//go:build tier1

// Acceptance tests for migration 0003 — authentication state. Written BEFORE
// 0003, for review. Every TestAuth* name is in this suite.
//
// Decisions encoded here (2026-09-30):
//   - PIN on every sign-in; an SMS code only on a new device.
//   - A code proves someone holds the phone, NOT who they are. Every
//     attributable act consumes a fresh, single-use PIN proof of the ACTOR.
//   - Lockout per ACCOUNT: 5 failures → 15 min, doubling within 24 h; the third
//     lockout in 24 h needs an officer. A success between lockouts does not
//     erase the history.
//   - A locked-out signatory is reset by TWO OTHER current signatories (D-15
//     revised), within a window, with approvals that postdate the request.
//   - Session length follows the highest privilege the person holds NOW:
//     8 h for anyone holding an appointment (officers, approvers), else 30 d.
//
// Numbers chosen for this suite and open to review: PIN-proof freshness
// 5 min; two-signatory window 48 h. Code lifetime 5 min / 3 attempts / 60 s
// re-send, and session lengths, were decided.
//
// Time: every window is measured by auth_now(), advanced in these tests
// through argon.test_now — honoured only because TestMain enabled the
// DATABASE-level test clock (TestAuth0 proves a session cannot).
package tier1

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argon/backend/internal/auth"
)

// ---- harness ----------------------------------------------------------------

type authT struct {
	t   *testing.T
	s   *soc
	c   *pgxpool.Conn // one connection per test, carrying its clock
	now time.Time
}

func newAuth(t *testing.T) *authT {
	t.Helper()
	a := &authT{t: t, s: newSociety(t)}
	c, err := db.Acquire(bg())
	fixture(t, err, "acquire connection")
	a.c = c
	t.Cleanup(func() { c.Exec(bg(), `RESET argon.test_now`); c.Release() })
	var now time.Time
	fixture(t, c.QueryRow(bg(), `SELECT date_trunc('second', now())`).Scan(&now), "read clock")
	a.set(now)
	return a
}

func (a *authT) set(ts time.Time) {
	a.now = ts
	_, err := a.c.Exec(bg(), `SELECT set_config('argon.test_now', $1, false)`, ts.UTC().Format(time.RFC3339Nano))
	fixture(a.t, err, "set clock")
}
func (a *authT) advance(d time.Duration) { a.set(a.now.Add(d)) }

func (a *authT) exec(sql string, args ...any) error {
	_, err := a.c.Exec(bg(), sql, args...)
	return err
}

func (a *authT) tx(fn func(pgx.Tx) error) error {
	x, err := a.c.Begin(bg())
	if err != nil {
		return err
	}
	defer x.Rollback(bg())
	if err := fn(x); err != nil {
		return err
	}
	return x.Commit(bg())
}

func (a *authT) scan(what, sql string, dst []any, args ...any) {
	a.t.Helper()
	fixture(a.t, a.c.QueryRow(bg(), sql, args...).Scan(dst...), what)
}

// A syntactically valid argon2id PHC string. The database checks FORMAT; it
// cannot verify a PIN (argon2 is not available in Postgres) — the service does.
const argonHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$Zm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm8"

var dseq atomic.Int64

// digest: a unique 64-hex string, the shape of an HMAC/SHA-256 digest.
func digest() string { return fmt.Sprintf("%064x", time.Now().UnixNano()+dseq.Add(1)) }

type person struct{ user, member, phone string }

// who creates a person: a user, one member record linked to them, a phone.
func (a *authT) who(name string) person {
	a.t.Helper()
	p := person{phone: fmt.Sprintf("+23480%08d", seq.Add(1))}
	fixture(a.t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, name).Scan(&p.user), "user")
	p.member = a.record(p.user, p.phone)
	return p
}

// record adds a member record for an existing person (the import duplicate).
func (a *authT) record(user, phone string) string {
	a.t.Helper()
	var m string
	fixture(a.t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, 'Member', $4, 'cash', '2020-01-01', $5) RETURNING id`,
		a.s.id, a.s.branch, fmt.Sprintf("A-%d", seq.Add(1)), phone, user).Scan(&m), "member record")
	return m
}

// officer: a person holding a current post. signatory = a signing post.
func (a *authT) officer(name, post string, signatory bool) person {
	a.t.Helper()
	p := a.who(name)
	fixture(a.t, a.s.appoint(p.member, post, signatory, "2020-01-01", ""), post+" appointment")
	return p
}

func (a *authT) withPIN(user string) {
	a.t.Helper()
	fixture(a.t, a.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, user, argonHash), "PIN")
}

// proof mints a proof FOR an act kind (0005 scopes every proof at mint). On a
// schema without the column, the kind is not written, as accountInsert does
// for roles.
func (a *authT) proof(user, method, kind string) string {
	a.t.Helper()
	var id string
	if schemaHasProofKinds() {
		a.scan(method+" proof", `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, $2, $3) RETURNING id`, []any{&id}, user, method, kind)
	} else {
		a.scan(method+" proof", `INSERT INTO auth_proofs (user_id, method) VALUES ($1, $2) RETURNING id`, []any{&id}, user, method)
	}
	return id
}

type dev struct{ id, digest string }

func (a *authT) device(user string) dev {
	a.t.Helper()
	d := dev{digest: digest()}
	a.scan("device", `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id`, []any{&d.id}, user, d.digest)
	return d
}

type sess struct{ id, digest string }

func (a *authT) session(user string, d dev) sess {
	a.t.Helper()
	s := sess{digest: digest()}
	a.scan("session", `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3) RETURNING id`, []any{&s.id}, user, d.id, s.digest)
	return s
}

func (a *authT) valid(s sess) bool {
	var ok bool
	a.scan("session_valid", `SELECT session_valid($1)`, []any{&ok}, s.digest)
	return ok
}

type act struct {
	kind, actor, actorSession, proof, targetUser, targetMember, newPhone, newPin, request, reason, at string
}

func (a *authT) act(x act) (string, error) {
	var id string
	err := a.c.QueryRow(bg(), `INSERT INTO credential_acts
		  (kind, actor_user_id, actor_session_id, proof_id, target_user_id, target_member_id, new_phone, new_pin_hash, request_id, reason, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, coalesce($11::timestamptz, auth_now())) RETURNING id`,
		x.kind, x.actor, nz(x.actorSession), nz(x.proof), nz(x.targetUser), nz(x.targetMember),
		nz(x.newPhone), nz(x.newPin), nz(x.request), nz(x.reason), nz(x.at)).Scan(&id)
	return id, err
}

const why = "Member locked out after changing phones; identity confirmed in person at the branch"

func (a *authT) hasPIN(user string) bool {
	var n int
	a.scan("credential", `SELECT count(*) FROM auth_credentials WHERE user_id = $1`, []any{&n}, user)
	return n == 1
}

func (a *authT) liveSessions(user string) int {
	var n int
	a.scan("sessions", `SELECT count(*) FROM auth_sessions WHERE user_id = $1 AND ended_at IS NULL`, []any{&n}, user)
	return n
}

func w(code, constraint string) want { return want{codes: []string{code}, constraint: constraint} }

// =============================================================================
// 0 — THE CLOCK CANNOT BE SET BY A SESSION
// =============================================================================

// If the app could move auth_now(), every window below — code expiry,
// lockout, the two-signatory window, session length — would be the caller's
// to choose. The test clock is honoured only by a DATABASE-level setting.
func TestAuth0_SessionCannotEnableTheTestClock(t *testing.T) {
	ac, err := pgx.Connect(bg(), adminURL)
	fixture(t, err, "admin connect")
	defer ac.Close(bg())
	_, err = ac.Exec(bg(), `ALTER DATABASE `+dbName+` RESET argon.test_clock`)
	fixture(t, err, "disable test clock")
	defer ac.Exec(bg(), `ALTER DATABASE `+dbName+` SET argon.test_clock = 'on'`)

	c, err := pgx.ConnectConfig(bg(), db.Config().ConnConfig.Copy())
	fixture(t, err, "fresh connection")
	defer c.Close(bg())
	_, err = c.Exec(bg(), `SET argon.test_clock = 'on'`)
	fixture(t, err, "session-level SET")
	_, err = c.Exec(bg(), `SET argon.test_now = '2000-01-01 00:00:00+00'`)
	fixture(t, err, "session-level test_now")
	var drift float64
	fixture(t, c.QueryRow(bg(), `SELECT abs(extract(epoch FROM auth_now() - now()))`).Scan(&drift), "auth_now")
	if drift > 60 {
		t.Fatalf("ACCEPTED — a session set auth_now() %.0f s away from the real clock without any database-level setting", drift)
	}
}

// =============================================================================
// A — ONLY HASHES ARE STORED
// =============================================================================

func TestAuthA1_PinNeverStoredInPlaintext(t *testing.T) {
	a := newAuth(t)
	refused(t, a.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, '1234')`, a.who("M").user),
		"a PIN stored as '1234'", w("23514", "pin_is_argon2id"))
}

func TestAuthA1_PinHashMustBeArgon2id(t *testing.T) {
	a := newAuth(t)
	refused(t, a.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, '$2b$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW')`, a.who("M").user),
		"a PIN stored as a bcrypt hash", w("23514", "pin_is_argon2id"))
}

func TestAuthA2_CodeNeverStoredInPlaintext(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', '482913', $2)`, p.user, p.phone),
		"a one-time code stored as '482913'", w("23514", "code_is_digest"))
}

func TestAuthA3_SessionTokenNeverStoredInPlaintext(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	d := a.device(p.user)
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, 'sess_plain_token_value')`, p.user, d.id),
		"a session token stored in the clear", w("23514", "token_is_digest"))
}

func TestAuthA3_DeviceTokenNeverStoredInPlaintext(t *testing.T) {
	a := newAuth(t)
	refused(t, a.exec(`INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, 'device-token-in-clear')`, a.who("M").user),
		"a device token stored in the clear", w("23514", "token_is_digest"))
}

func TestAuthA_Control_HashesAccepted(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	accepted(t, a.exec(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, p.user, argonHash), "an argon2id PIN hash")
	accepted(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3)`, p.user, digest(), p.phone),
		"a code digest sent to the registered phone")
	accepted(t, a.exec(`INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2)`, p.user, digest()), "a device token digest")
}

// =============================================================================
// B — A CODE IS NOT AN ATTRIBUTION
//
// A code proves someone holds the phone, not who they are. Every attributable
// act consumes a fresh, single-use PIN proof OF THE ACTOR. Otherwise anyone
// holding a member's phone becomes that member for every control above.
// =============================================================================

var wPinProof = w("23514", "act_needs_pin_proof")

// resetFixture: an officer and a member (with a PIN and a session) to reset.
func (a *authT) resetFixture() (officer, member person) {
	officer = a.officer("Secretary", "secretary", false)
	member = a.who("Member")
	a.withPIN(member.user)
	a.session(member.user, a.device(member.user))
	return
}

func TestAuthB1_CodeProofCannotAuthoriseAReset(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "code", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "an officer's member reset authorised only by a code to the officer's phone", wPinProof)
}

func TestAuthB1_CodeProofCannotAuthoriseAPhoneChange(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	_, err := a.act(act{kind: "phone_change", actor: o.user, proof: a.proof(o.user, "code", "phone_change"), targetMember: m.member, newPhone: unregistered(), reason: why})
	refused(t, err, "a phone change authorised only by a code", wPinProof)
}

func TestAuthB2_ActorMustUseTheirOwnProof(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	colleague := a.officer("Chairman", "chairman", true)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(colleague.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "an officer acting on a colleague's PIN proof", wPinProof)
}

func TestAuthB3_StaleProofRefused(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	pr := a.proof(o.user, "pin", "member_reset")
	a.advance(6 * time.Minute)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: pr, targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "a reset on a PIN proof six minutes old", wPinProof)
}

func TestAuthB4_ProofIsSingleUse(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	m2 := a.who("Second member")
	pr := a.proof(o.user, "pin", "member_reset")
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: pr, targetUser: m.user, targetMember: m.member, reason: why})
	fixture(t, err, "first reset on the proof")
	_, err = a.act(act{kind: "member_reset", actor: o.user, proof: pr, targetUser: m2.user, targetMember: m2.member, reason: why})
	refused(t, err, "a second reset on the same PIN proof", wPinProof)
}

func TestAuthB5_ProofTimeIsTheServers(t *testing.T) {
	a := newAuth(t)
	o := a.officer("Secretary", "secretary", false)
	q := `INSERT INTO auth_proofs (user_id, method, at) VALUES ($1, 'pin', $2)`
	if schemaHasProofKinds() { // scoped, so the only thing wrong with it is its time
		q = `INSERT INTO auth_proofs (user_id, method, at, act_kind) VALUES ($1, 'pin', $2, 'member_reset')`
	}
	refused(t, a.exec(q, o.user, a.now.Add(time.Hour).Format(time.RFC3339)),
		"a PIN proof whose caller supplied its time", w("23514", "auth_proofs_at_is_now"))
}

// =============================================================================
// C — AN OFFICER RESETS A MEMBER: ATTRIBUTABLE, NEVER THEMSELVES
// =============================================================================

func TestAuthC1_ResetStatesReason(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member})
	refused(t, err, "a member reset with no reason", w("23514", "reset_states_reason"))
}

func TestAuthC2_ResetByNonOfficerRefused(t *testing.T) {
	a := newAuth(t)
	_, m := a.resetFixture()
	x := a.who("Ordinary member")
	_, err := a.act(act{kind: "member_reset", actor: x.user, proof: a.proof(x.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "a member reset by someone holding no post", w("23514", "reset_by_current_officer"))
}

func TestAuthC3_ResetByAuditorRefused(t *testing.T) {
	a := newAuth(t)
	_, m := a.resetFixture()
	aud := a.officer("Auditor", "internal_auditor", false)
	_, err := a.act(act{kind: "member_reset", actor: aud.user, proof: a.proof(aud.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "a member reset by the internal auditor (read-only)", w("23514", "reset_by_current_officer"))
}

func TestAuthC4_OfficerCannotResetThemselves(t *testing.T) {
	a := newAuth(t)
	o, _ := a.resetFixture()
	a.withPIN(o.user)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetUser: o.user, targetMember: o.member, reason: why})
	refused(t, err, "an officer resetting their own credentials", w("23514", "reset_not_self"))
}

// The auditor-relink shape: the rule enforced on the wrong object. The act
// names only a MEMBER RECORD — the officer's own duplicate — not a user.
func TestAuthC4_OfficerCannotResetThemselvesThroughOwnOtherRecord(t *testing.T) {
	a := newAuth(t)
	o, _ := a.resetFixture()
	a.withPIN(o.user)
	dup := a.record(o.user, o.phone)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetMember: dup, reason: why})
	refused(t, err, "an officer resetting themselves by naming their duplicate member record", w("23514", "reset_not_self"))
}

func TestAuthC5_OfficerOfAnotherSocietyRefused(t *testing.T) {
	a := newAuth(t)
	_, m := a.resetFixture()
	other := newAuth(t)
	far := other.officer("Other society's secretary", "secretary", false)
	_, err := a.act(act{kind: "member_reset", actor: far.user, proof: a.proof(far.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "a member reset by an officer of a different society", w("23514", "reset_by_current_officer"))
}

func TestAuthC6_OneOfficerCannotResetASignatory(t *testing.T) {
	a := newAuth(t)
	o, _ := a.resetFixture()
	sig := a.officer("Treasurer", "treasurer", true)
	a.withPIN(sig.user)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetUser: sig.user, targetMember: sig.member, reason: why})
	refused(t, err, "one officer resetting a current signatory (D-15 revised: two other signatories)", w("23514", "signatory_reset_needs_two"))
}

// "It clears their PIN and every signed-in device" — as one act, so no reset
// can leave a session alive behind it.
func TestAuthC7_ResetClearsPinAndEndsEverySession(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	d2 := a.device(m.user)
	a.session(m.user, d2)
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	accepted(t, err, "officer resets member")
	if a.hasPIN(m.user) {
		t.Fatalf("ACCEPTED — the reset left the member's PIN in place")
	}
	if n := a.liveSessions(m.user); n != 0 {
		t.Fatalf("ACCEPTED — the reset left %d session(s) signed in", n)
	}
	var trusted bool
	a.scan("device", `SELECT device_trusted($1)`, []any{&trusted}, d2.digest)
	if trusted {
		t.Fatalf("ACCEPTED — the reset left a device trusted")
	}
}

func TestAuthC_Control_OfficerResetsMember(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	_, err := a.act(act{kind: "member_reset", actor: o.user, proof: a.proof(o.user, "pin", "member_reset"), targetUser: m.user, targetMember: m.member, reason: why})
	accepted(t, err, "a current officer resets a member, with a reason and their own fresh PIN proof")
}

// =============================================================================
// D — A SIGNATORY IS RESET BY TWO OTHER SIGNATORIES, TOGETHER (D-15 revised)
//
// The reset is itself a 2-of-N act. "Together" means within a window: two
// approvals three weeks apart are one person waiting, not two agreeing. And an
// approval cannot predate the request it approves.
// =============================================================================

type sigFixture struct {
	a                     *authT
	target, first, second person
	request               string
}

// Treasurer (target, locked out), chairman and secretary (the other two).
func (a *authT) sigReset() *sigFixture {
	f := &sigFixture{a: a,
		target: a.officer("Treasurer", "treasurer", true),
		first:  a.officer("Chairman", "chairman", true),
		second: a.officer("Secretary", "secretary", true)}
	a.withPIN(f.target.user)
	a.session(f.target.user, a.device(f.target.user))
	var err error
	f.request, err = a.act(act{kind: "signatory_reset_request", actor: f.first.user, proof: a.proof(f.first.user, "pin", "signatory_reset_request"),
		targetUser: f.target.user, targetMember: f.target.member, reason: why})
	fixture(a.t, err, "signatory reset request")
	return f
}

func (f *sigFixture) approve(by person) error {
	_, err := f.a.act(act{kind: "signatory_approval", actor: by.user, proof: f.a.proof(by.user, "pin", "signatory_approval"), request: f.request, targetUser: f.target.user})
	return err
}

func TestAuthD1_OneApprovalDoesNotReset(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	fixture(t, f.approve(f.first), "first approval")
	if !a.hasPIN(f.target.user) {
		t.Fatalf("ACCEPTED — a signatory was reset on ONE approval")
	}
}

func TestAuthD2_TargetCannotApproveOwnReset(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	refused(t, f.approve(f.target), "the locked-out signatory approving their own reset", w("23514", "reset_approver_not_target"))
}

func TestAuthD3_OnePersonCountsOnce(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	fixture(t, f.approve(f.first), "first approval")
	refused(t, f.approve(f.first), "the same signatory approving twice",
		want{codes: []string{"23505", "23514"}, constraint: "reset_one_approval_per_person"})
}

func TestAuthD4_ApproverMustBeASignatory(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	lo := a.officer("Loan officer", "loan_officer", false)
	refused(t, f.approve(lo), "an approval by an officer holding no signing post (an administrator, D-15)", w("23514", "reset_approver_is_signatory"))
}

func TestAuthD4_FormerSignatoryCannotApprove(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	was := a.who("Former branch officer")
	fixture(t, a.s.appoint(was.member, "branch_officer", true, "2020-01-01", "2020-12-31"), "signing term ended 2020")
	refused(t, f.approve(was), "an approval by someone whose signing term has ended", w("23514", "reset_approver_is_signatory"))
}

func TestAuthD4_SignatoryOfAnotherSocietyCannotApprove(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	other := newAuth(t)
	far := other.officer("Other society's chairman", "chairman", true)
	refused(t, f.approve(far), "an approval by a signatory of a different society", w("23514", "reset_approver_is_signatory"))
}

func TestAuthD5_ApprovalsWeeksApartDoNotCount(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	fixture(t, f.approve(f.first), "first approval")
	a.advance(21 * 24 * time.Hour)
	refused(t, f.approve(f.second), "a second approval three weeks after the first", w("23514", "reset_request_expired"))
	if !a.hasPIN(f.target.user) {
		t.Fatalf("ACCEPTED — approvals three weeks apart reset a signatory")
	}
}

// The back-dating walk-around: a PIN proof taken BEFORE the request was
// raised, still fresh enough to use, counted as agreement to it.
func TestAuthD6_ApprovalCannotPredateTheRequest(t *testing.T) {
	a := newAuth(t)
	target := a.officer("Treasurer", "treasurer", true)
	first := a.officer("Chairman", "chairman", true)
	a.withPIN(target.user)
	early := a.proof(first.user, "pin", "signatory_approval")
	a.advance(time.Minute)
	req, err := a.act(act{kind: "signatory_reset_request", actor: first.user, proof: a.proof(first.user, "pin", "signatory_reset_request"),
		targetUser: target.user, targetMember: target.member, reason: why})
	fixture(t, err, "request")
	_, err = a.act(act{kind: "signatory_approval", actor: first.user, proof: early, request: req, targetUser: target.user})
	refused(t, err, "an approval on a PIN proof taken before the request existed", w("23514", "approval_after_request"))
}

func TestAuthD7_ActTimeIsTheServers(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	_, err := a.act(act{kind: "signatory_approval", actor: f.second.user, proof: a.proof(f.second.user, "pin", "signatory_approval"), request: f.request,
		targetUser: f.target.user, at: a.now.Add(-time.Hour).Format(time.RFC3339)})
	refused(t, err, "an approval whose caller supplied its time", w("23514", "credential_acts_at_is_now"))
}

func TestAuthD_TwoOtherSignatoriesReset(t *testing.T) {
	a := newAuth(t)
	f := a.sigReset()
	fixture(t, f.approve(f.first), "first approval")
	accepted(t, f.approve(f.second), "second approval, within the window")
	if a.hasPIN(f.target.user) || a.liveSessions(f.target.user) != 0 {
		t.Fatalf("REFUSED — two other current signatories approved in time, but the signatory's PIN or sessions survived")
	}
}

// =============================================================================
// E — A PHONE NUMBER CHANGES ONLY BY AN ATTRIBUTABLE ACT
//
// The registered phone is where codes go. Changing it is a route into the
// account, so it is guarded like a reset: never by the member themselves (a
// code to the old phone is not identity), by an officer attributably, and for
// a signatory by two other signatories.
// =============================================================================

func TestAuthE1_DirectPhoneUpdateRefused(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	refused(t, a.exec(`UPDATE members SET phone = $2 WHERE id = $1`, m.member, unregistered()),
		"a member's phone number overwritten without an attributable act", w("23514", "phone_change_attributable"))
}

func TestAuthE2_MemberCannotChangeOwnPhone(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	_, err := a.act(act{kind: "phone_change", actor: m.user, proof: a.proof(m.user, "pin", "phone_change"), targetMember: m.member, newPhone: unregistered(), reason: why})
	refused(t, err, "a member changing their own registered phone", w("23514", "phone_change_not_self"))
}

func TestAuthE3_OfficerPhoneChangeAppliesTheRecordedNumber(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	fresh := unregistered()
	_, err := a.act(act{kind: "phone_change", actor: o.user, proof: a.proof(o.user, "pin", "phone_change"), targetMember: m.member, newPhone: fresh, reason: why})
	accepted(t, err, "an officer records a phone change")
	var got string
	a.scan("phone", `SELECT phone FROM members WHERE id = $1`, []any{&got}, m.member)
	if got != fresh {
		t.Fatalf("REFUSED — the recorded change did not take effect: phone is %s", got)
	}
}

func TestAuthE4_OneOfficerCannotChangeASignatorysPhone(t *testing.T) {
	a := newAuth(t)
	o, _ := a.resetFixture()
	sig := a.officer("Treasurer", "treasurer", true)
	_, err := a.act(act{kind: "phone_change", actor: o.user, proof: a.proof(o.user, "pin", "phone_change"), targetMember: sig.member, newPhone: unregistered(), reason: why})
	refused(t, err, "one officer redirecting a signatory's codes to a new number", w("23514", "signatory_phone_needs_two"))
}

// The record says one number; the row gets another.
func TestAuthE5_AppliedNumberMustBeTheRecordedNumber(t *testing.T) {
	a := newAuth(t)
	o, m := a.resetFixture()
	recorded, applied := unregistered(), unregistered()
	err := a.tx(func(x pgx.Tx) error {
		if _, err := x.Exec(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id, new_phone, reason)
			VALUES ('phone_change', $1, $2, $3, $4, $5)`, o.user, a.proof(o.user, "pin", "phone_change"), m.member, recorded, why); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `UPDATE members SET phone = $2 WHERE id = $1`, m.member, applied)
		return err
	})
	refused(t, err, "an attributable change to one number followed by an update to a different one", w("23514", "phone_change_attributable"))
}

func TestAuthE_TwoSignatoriesChangeASignatorysPhone(t *testing.T) {
	a := newAuth(t)
	target := a.officer("Treasurer", "treasurer", true)
	first := a.officer("Chairman", "chairman", true)
	second := a.officer("Secretary", "secretary", true)
	fresh := unregistered()
	req, err := a.act(act{kind: "signatory_phone_request", actor: first.user, proof: a.proof(first.user, "pin", "signatory_phone_request"),
		targetUser: target.user, targetMember: target.member, newPhone: fresh, reason: why})
	fixture(t, err, "request")
	for _, by := range []person{first, second} {
		_, err = a.act(act{kind: "signatory_approval", actor: by.user, proof: a.proof(by.user, "pin", "signatory_approval"), request: req, targetUser: target.user})
		fixture(t, err, "approval")
	}
	var got string
	a.scan("phone", `SELECT phone FROM members WHERE id = $1`, []any{&got}, target.member)
	if got != fresh {
		t.Fatalf("REFUSED — two other signatories approved, but the phone is still %s", got)
	}
}

// =============================================================================
// F — CODES: TO THE REGISTERED PHONE ONLY, SHORT-LIVED, SINGLE-USE
// =============================================================================

var wCodeDest = w("23514", "code_goes_to_registered_phone")

func (a *authT) code(p person) (string, string) {
	a.t.Helper()
	var id string
	dg := digest()
	a.scan("code", `INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3) RETURNING id`, []any{&id}, p.user, dg, p.phone)
	return id, dg
}

func (a *authT) verify(id, dg string) bool {
	var ok bool
	a.scan("code_verify", `SELECT code_verify($1, $2)`, []any{&ok}, id, dg)
	return ok
}

// The import duplicate again: a second member record for the same person
// carrying a different number. Sending to "the" phone would send to either.
func TestAuthF1_NoCodeWhenRecordsDisagreeOnPhone(t *testing.T) {
	a := newAuth(t)
	p := a.who("Member")
	other := unregistered()
	a.record(p.user, other)
	var dest *string
	a.scan("destination", `SELECT otp_destination($1)`, []any{&dest}, p.user)
	if dest != nil {
		t.Fatalf("ACCEPTED — codes would go to %s although the person's records disagree on their number", *dest)
	}
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3)`, p.user, digest(), other),
		"a code sent to one of two disagreeing numbers", wCodeDest)
}

func TestAuthF2_CodeCannotGoToAnotherNumber(t *testing.T) {
	a := newAuth(t)
	p := a.who("Member")
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3)`, p.user, digest(), unregistered()),
		"a code sent to a number that is not the registered one", wCodeDest)
}

func TestAuthF3_NoRegisteredNumberNoCode(t *testing.T) {
	a := newAuth(t)
	var u string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('No record') RETURNING id`).Scan(&u), "user with no member record")
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, NULL)`, u, digest()),
		"a code for a person with no registered number", wCodeDest)
}

func TestAuthF4_CodeExpiresAfterFiveMinutes(t *testing.T) {
	a := newAuth(t)
	id, dg := a.code(a.who("Member"))
	a.advance(5*time.Minute + time.Second)
	if a.verify(id, dg) {
		t.Fatalf("ACCEPTED — a code was accepted five minutes and one second after it was sent")
	}
}

func TestAuthF5_FourthAttemptRefusedEvenIfRight(t *testing.T) {
	a := newAuth(t)
	id, dg := a.code(a.who("Member"))
	for i := 0; i < 3; i++ {
		if a.verify(id, digest()) {
			t.Fatalf("FIXTURE: a wrong code was accepted")
		}
	}
	if a.verify(id, dg) {
		t.Fatalf("ACCEPTED — the right code was accepted on the fourth attempt")
	}
}

func TestAuthF6_CodeIsSingleUse(t *testing.T) {
	a := newAuth(t)
	id, dg := a.code(a.who("Member"))
	if !a.verify(id, dg) {
		t.Fatalf("FIXTURE: the right code was refused on first use")
	}
	if a.verify(id, dg) {
		t.Fatalf("ACCEPTED — a code was accepted a second time")
	}
}

func TestAuthF7_ResendNotWithinSixtySeconds(t *testing.T) {
	a := newAuth(t)
	p := a.who("Member")
	a.code(p)
	a.advance(30 * time.Second)
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3)`, p.user, digest(), p.phone),
		"a second code 30 seconds after the first", w("23514", "code_resend_interval"))
}

func TestAuthF7_Control_ResendAfterSixtySeconds(t *testing.T) {
	a := newAuth(t)
	p := a.who("Member")
	a.code(p)
	a.advance(61 * time.Second)
	accepted(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3)`, p.user, digest(), p.phone),
		"a second code 61 seconds after the first")
}

func TestAuthF8_CodeTimeIsTheServers(t *testing.T) {
	a := newAuth(t)
	p := a.who("Member")
	refused(t, a.exec(`INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to, created_at) VALUES ($1, 'new_device', $2, $3, $4)`,
		p.user, digest(), p.phone, a.now.Add(time.Hour).Format(time.RFC3339)),
		"a code whose caller supplied its creation time (extending its life)", w("23514", "auth_codes_at_is_now"))
}

func TestAuthF_Control_RightCodeInTime(t *testing.T) {
	a := newAuth(t)
	id, dg := a.code(a.who("Member"))
	a.advance(4*time.Minute + 59*time.Second)
	if !a.verify(id, dg) {
		t.Fatalf("REFUSED — the right code, first attempt, at 4 min 59 s")
	}
}

// =============================================================================
// G — LOCKOUT, PER ACCOUNT
// =============================================================================

func (a *authT) attempt(user string, ok bool, d *dev) {
	a.t.Helper()
	var dv any
	if d != nil {
		dv = d.id
	}
	fixture(a.t, a.exec(`INSERT INTO sign_in_attempts (user_id, credential_digest, device_id, succeeded) VALUES ($1, $2, $3, $4)`,
		user, auth.TokenDigest("user:"+user), dv, ok), "sign-in attempt")
}

func (a *authT) fails(user string, n int) {
	for i := 0; i < n; i++ {
		a.attempt(user, false, nil)
	}
}

func (a *authT) lock(user string) (until *time.Time, officer bool) {
	a.scan("lock_state", `SELECT locked_until, needs_officer FROM lock_state($1)`, []any{&until, &officer}, user)
	return
}

func (a *authT) wantLockedFor(what string, user string, d time.Duration) {
	a.t.Helper()
	until, _ := a.lock(user)
	if until == nil {
		a.t.Fatalf("ACCEPTED — %s: not locked at all", what)
	}
	if got := until.Sub(a.now); got < d-time.Second || got > d+time.Second {
		a.t.Fatalf("WRONG LOCK — %s: locked for %s, want %s", what, got.Round(time.Second), d)
	}
}

func TestAuthG1_FiveFailuresLockFifteenMinutes(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 5)
	a.wantLockedFor("five wrong PINs", u, 15*time.Minute)
}

func TestAuthG2_FailuresAcrossDevicesAccumulate(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	for i := 0; i < 5; i++ {
		d := a.device(u)
		a.attempt(u, false, &d) // a different phone every time
	}
	a.wantLockedFor("five wrong PINs from five different devices", u, 15*time.Minute)
}

func TestAuthG3_SecondLockoutInADayIsThirtyMinutes(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 5)
	a.advance(16 * time.Minute)
	a.fails(u, 5)
	a.wantLockedFor("the second lockout within 24 h", u, 30*time.Minute)
}

// A success between lockouts must not wipe the slate: otherwise one correct
// guess every few attempts dodges the officer tier forever.
func TestAuthG4_SuccessDoesNotEraseLockoutHistory(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 5)
	a.advance(16 * time.Minute)
	a.attempt(u, true, nil)
	a.advance(time.Second) // after the success, not tied with it
	a.fails(u, 5)
	a.wantLockedFor("a second lockout after a successful sign-in", u, 30*time.Minute)
}

// Each success is a second BEFORE the failures that follow it. Rows written
// at the same instant have no defined order, and whether a success resets a
// streak before or after it would then be a coin toss.
func (a *authT) threeLockouts(u string) {
	a.fails(u, 5)
	a.advance(16 * time.Minute)
	a.attempt(u, true, nil)
	a.advance(time.Second)
	a.fails(u, 5)
	a.advance(31 * time.Minute)
	a.attempt(u, true, nil)
	a.advance(time.Second)
	a.fails(u, 5)
}

func TestAuthG5_ThirdLockoutInADayNeedsAnOfficer(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.threeLockouts(u)
	a.advance(3 * time.Hour)
	if _, officer := a.lock(u); !officer {
		t.Fatalf("ACCEPTED — the third lockout in 24 h released itself; it must wait for an officer")
	}
}

func TestAuthG6_OfficerReleaseEndsTheLock(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	a.threeLockouts(m.user)
	if _, officer := a.lock(m.user); !officer {
		// Without the lock there is nothing to release, and a pass would be vacuous.
		t.Fatalf("FIXTURE: the account is not in the officer-release state (G5's rule is missing)")
	}
	o := a.officer("Secretary", "secretary", false)
	_, err := a.act(act{kind: "lock_release", actor: o.user, proof: a.proof(o.user, "pin", "lock_release"), targetUser: m.user, targetMember: m.member, reason: why})
	accepted(t, err, "an officer releases the lock")
	if until, officer := a.lock(m.user); until != nil || officer {
		t.Fatalf("REFUSED — the officer's release did not end the lock")
	}
}

func TestAuthG7_OfficerCannotReleaseOwnLock(t *testing.T) {
	a := newAuth(t)
	o := a.officer("Secretary", "secretary", false)
	a.threeLockouts(o.user)
	_, err := a.act(act{kind: "lock_release", actor: o.user, proof: a.proof(o.user, "pin", "lock_release"), targetUser: o.user, targetMember: o.member, reason: why})
	refused(t, err, "an officer releasing their own lock", w("23514", "release_not_self"))
}

func TestAuthG8_LockoutsOlderThanADayDoNotEscalate(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 5)
	a.advance(25 * time.Hour)
	a.fails(u, 5)
	a.wantLockedFor("a lockout 25 h after the previous one", u, 15*time.Minute)
}

func TestAuthG9_AttemptTimeIsTheServers(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	refused(t, a.exec(`INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded, at) VALUES ($1, $2, false, $3)`,
		u, digest(), a.now.Add(-48*time.Hour).Format(time.RFC3339)),
		"a failed attempt back-dated out of the lockout window", w("23514", "sign_in_attempts_at_is_now"))
}

func TestAuthG_Control_FourFailuresThenSuccess(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 4)
	a.advance(time.Second)
	a.attempt(u, true, nil)
	if until, _ := a.lock(u); until != nil {
		t.Fatalf("REFUSED — four wrong PINs then a right one locked the account")
	}
}

// =============================================================================
// H — SESSIONS AND DEVICES
// =============================================================================

func TestAuthH1_PinChangeEndsOtherSessionsKeepsThisOne(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	a.withPIN(m.user)
	here := a.session(m.user, a.device(m.user))
	there := a.session(m.user, a.device(m.user))
	_, err := a.act(act{kind: "pin_change", actor: m.user, actorSession: here.id, proof: a.proof(m.user, "pin", "pin_change"), targetUser: m.user, newPin: argonHash})
	accepted(t, err, "a member changes their PIN")
	if a.valid(there) {
		t.Fatalf("ACCEPTED — the PIN changed but another device stayed signed in")
	}
	if !a.valid(here) {
		t.Fatalf("REFUSED — changing the PIN signed out the device it was changed from")
	}
}

func TestAuthH2_EndedSessionCannotBeRevived(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	s := a.session(m.user, a.device(m.user))
	fixture(t, a.exec(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE id = $1`, s.id), "end session")
	refused(t, a.exec(`UPDATE auth_sessions SET ended_at = NULL WHERE id = $1`, s.id),
		"an ended session revived", w("23514", "session_end_is_final"))
}

// The stolen phone: the member, signed in elsewhere, ends the session on the
// device they no longer hold. That device must lose its trust with it.
func TestAuthH3_MemberEndsStolenDeviceFromElsewhere(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	here := a.session(m.user, a.device(m.user))
	stolen := a.device(m.user)
	thief := a.session(m.user, stolen)
	accepted(t, a.exec(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = $1 WHERE id = $2`, here.id, thief.id),
		"the member ends the stolen phone's session from another device")
	if a.valid(thief) {
		t.Fatalf("ACCEPTED — the stolen phone's session is still valid")
	}
	var trusted bool
	a.scan("device", `SELECT device_trusted($1)`, []any{&trusted}, stolen.digest)
	if trusted {
		t.Fatalf("ACCEPTED — the stolen phone is still a trusted device: a thief with the PIN signs straight back in")
	}
}

func TestAuthH4_RevokedDeviceCannotRevive(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	here := a.session(m.user, a.device(m.user))
	stolen := a.device(m.user)
	thief := a.session(m.user, stolen)
	fixture(t, a.exec(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = $1 WHERE id = $2`, here.id, thief.id), "end stolen session")
	fixture(t, a.exec(`UPDATE auth_devices SET revoked_at = auth_now() WHERE id = $1 AND revoked_at IS NULL`, stolen.id), "revoke (if not already)")
	refused(t, a.exec(`UPDATE auth_devices SET revoked_at = NULL WHERE id = $1`, stolen.id),
		"the revoked stolen device un-revoked", w("23514", "device_revocation_is_final"))
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, m.user, stolen.id, digest()),
		"a new session on the revoked device", w("23514", "session_on_revoked_device"))
}

func TestAuthH5_AnotherPersonCannotEndMySession(t *testing.T) {
	a := newAuth(t)
	m, x := a.who("Member"), a.who("Someone else")
	mine := a.session(m.user, a.device(m.user))
	theirs := a.session(x.user, a.device(x.user))
	refused(t, a.exec(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = $1 WHERE id = $2`, theirs.id, mine.id),
		"another person's session ending mine", w("23514", "session_ended_by_owner"))
}

func TestAuthH6_MemberSessionLastsThirtyDays(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	s := a.session(m.user, a.device(m.user))
	a.advance(29 * 24 * time.Hour)
	if !a.valid(s) {
		t.Fatalf("REFUSED — a member's session expired before 30 days")
	}
	a.advance(2 * 24 * time.Hour)
	if a.valid(s) {
		t.Fatalf("ACCEPTED — a member's session outlived 30 days")
	}
}

func TestAuthH7_OfficerSessionLastsEightHours(t *testing.T) {
	a := newAuth(t)
	o := a.officer("Secretary", "secretary", false)
	s := a.session(o.user, a.device(o.user))
	a.advance(7 * time.Hour)
	if !a.valid(s) {
		t.Fatalf("REFUSED — an officer's session expired before 8 h")
	}
	a.advance(2 * time.Hour)
	if a.valid(s) {
		t.Fatalf("ACCEPTED — an officer's session outlived 8 h")
	}
}

// An approver is also a member. Signing in on the member surface must not buy
// a 30-day session for someone who can sign releases.
func TestAuthH8_ApproverSignedInAsMemberGetsEightHours(t *testing.T) {
	a := newAuth(t)
	sig := a.officer("Treasurer", "treasurer", true)
	s := a.session(sig.user, a.device(sig.user))
	a.advance(9 * time.Hour)
	if a.valid(s) {
		t.Fatalf("ACCEPTED — an approver's session outlived 8 h")
	}
}

// "The highest privilege the person holds" — NOW, not at sign-in. Otherwise
// a 30-day member session survives being appointed signatory.
func TestAuthH9_AppointedMidSessionShrinksToEightHours(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	s := a.session(m.user, a.device(m.user))
	a.advance(time.Hour)
	fixture(t, a.s.appoint(m.member, "treasurer", true, "2020-01-01", ""), "appointed treasurer mid-session")
	a.advance(8 * time.Hour)
	if a.valid(s) {
		t.Fatalf("ACCEPTED — a session begun as a member kept its 30 days after they became a signatory")
	}
}

// The reverse of H9 — DECISION (2026-10-01, pending confirmation): a session
// never outlives the privilege it was granted under. When the person's last
// appointment ends mid-session, the session ENDS: it neither keeps its 8 h as
// an officer session nor quietly becomes a 30-day member one. They sign in
// again, as a member.
//
// The trap: terms are DATES. Ending one by setting held_until to yesterday
// also says the term ended before this session began, so a rule asking "did
// they hold a post when the session started?" of the appointments table
// answers no, and grants 30 days. The session must remember its own
// privilege.
func TestAuthH9_AppointmentEndingMidSessionEndsTheSession(t *testing.T) {
	a := newAuth(t)
	sig := a.officer("Treasurer", "treasurer", true)
	s := a.session(sig.user, a.device(sig.user))
	a.advance(time.Hour)
	fixture(t, a.exec(`UPDATE appointments SET held_until = (auth_now()::date - 1) WHERE member_id = $1`, sig.member), "term ended mid-session")
	if a.valid(s) {
		a.advance(8 * time.Hour)
		if a.valid(s) {
			t.Fatalf("ACCEPTED — the treasurer's term ended mid-session and the session became a 30-day member session")
		}
		t.Fatalf("ACCEPTED — the treasurer's term ended mid-session and the session kept running as an officer's")
	}
}

func TestAuthH10_SessionExpiryIsTheServers(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	d := a.device(m.user)
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest, expires_at) VALUES ($1, $2, $3, $4)`,
		m.user, d.id, digest(), a.now.Add(365*24*time.Hour).Format(time.RFC3339)),
		"a session whose caller chose its expiry", w("23514", "session_expiry_is_server"))
}

func TestAuthH_Control_FreshSessionValid(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	if !a.valid(a.session(m.user, a.device(m.user))) {
		t.Fatalf("REFUSED — a new session is not valid")
	}
}
