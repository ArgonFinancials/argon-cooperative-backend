//go:build tier1

// ACCEPTANCE TESTS — AN OFFICER RELEASES A LOCKED-OUT MEMBER, OVER HTTP
// (0022, decided 2026-11-05). Shipped with the lockout split, never after it:
// fixing the 48-hour leak alone would make a lock nobody could lift, and the
// first member it happened to would be locked out for good.
//
// POST /societies/{societyId}/members/{memberId}/lock-releases. The rules are
// the database's (0003, 0022): an officer of the member's society, never the
// auditor, never their own lock, a reason, the officer's own step-up, and a
// lock to release. The API's own: anyone who is not an officer of the society
// is told nothing is there (404); each refusal is something the officer can act on.
package tier1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/argon/backend/internal/auth"
)

const lockReason = "Locked out after a stranger tried her number; identity confirmed in person with her card"

type lockWorld struct {
	srv                        *httptest.Server
	s                          *soc
	secretary, auditor, member signer
	elsewhere                  signer // the secretary of ANOTHER society
	otherMember                signer // a member of that other society
}

func newLockAPI(t *testing.T) *lockWorld {
	t.Helper()
	s, o := newSociety(t), newSociety(t)
	return &lockWorld{srv: apiServer(t), s: s,
		secretary:   newSigner(t, s, "Secretary", "secretary", false),
		auditor:     newSigner(t, s, "Auditor", "internal_auditor", false),
		member:      newSigner(t, s, "Member", "", false),
		elsewhere:   newSigner(t, o, "Secretary elsewhere", "secretary", false),
		otherMember: newSigner(t, o, "Member elsewhere", "", false),
	}
}

// lockOut: five wrong PINs on the person's own device, a 15-minute lock.
func lockOut(t *testing.T, p signer) {
	t.Helper()
	for i := 0; i < 5; i++ {
		fixture(t, one(`INSERT INTO sign_in_attempts (user_id, credential_digest, device_id, succeeded)
			SELECT $1, $2, id, false FROM auth_devices WHERE user_id = $1 AND token_digest = $3`,
			p.user, digest(), auth.TokenDigest(p.device)), "a wrong PIN on the member's own phone")
	}
	if !lockedOut(t, p) {
		t.Fatalf("FIXTURE: five wrong PINs on the member's own phone locked nothing")
	}
}

func lockedOut(t *testing.T, p signer) bool {
	t.Helper()
	var a, b bool
	fixture(t, db.QueryRow(bg(), `SELECT (o.locked_until IS NOT NULL OR o.needs_officer), (e.locked_until IS NOT NULL OR e.needs_officer)
		FROM lock_state($1) o, lock_state_unknown_device($1) e`, p.user).Scan(&a, &b), "lock state")
	return a || b
}

func (w *lockWorld) release(t *testing.T, as signer, society *soc, member signer, proof, reason string) resp {
	t.Helper()
	body := map[string]string{"reason": reason}
	if proof != "" {
		body["pinProofId"] = proof
	}
	return postJSON(t, w.srv, "/societies/"+society.id+"/members/"+member.member+"/lock-releases", signIn(t, as.user), body)
}

func releases(t *testing.T, p signer) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM credential_acts WHERE kind = 'lock_release' AND target_user_id = $1`, p.user).Scan(&n), "releases")
	return n
}

func TestApiLocks01_AnOfficerReleasesALockedOutMember(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.member)
	r := w.release(t, w.secretary, w.s, w.member, stepUp(t, w.secretary.user, "lock_release"), lockReason)
	if r.status != http.StatusCreated {
		t.Fatalf("REFUSED — the secretary releasing a locked-out member answered %d %s; want 201", r.status, r.body)
	}
	var out struct {
		MemberId, ReleasedBy string
		ReleasedAt           time.Time
	}
	if json.Unmarshal(r.body, &out) != nil || out.MemberId != w.member.member || out.ReleasedBy != w.secretary.user || out.ReleasedAt.IsZero() {
		t.Fatalf("WRONG ANSWER — want the member, the releasing officer and the time: %s", r.body)
	}
	if lockedOut(t, w.member) {
		t.Fatalf("STILL LOCKED — the release answered 201 and the member is still locked out")
	}
	var actor, reason string
	fixture(t, db.QueryRow(bg(), `SELECT actor_user_id::text, reason FROM credential_acts WHERE kind = 'lock_release' AND target_user_id = $1`,
		w.member.user).Scan(&actor, &reason), "the release act")
	if actor != w.secretary.user || reason != lockReason {
		t.Fatalf("UNATTRIBUTED — the release act names %s, %q; want the secretary and the reason given", actor, reason)
	}
}

func TestApiLocks02_ReleasingNeedsTheOfficersOwnStepUp(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.member)
	for what, proof := range map[string]string{
		"no proof":                       "",
		"a proof minted for another act": stepUp(t, w.secretary.user, "approval_signature"),
		"another officer's proof":        stepUp(t, w.auditor.user, "lock_release"),
	} {
		wantProblem(t, w.release(t, w.secretary, w.s, w.member, proof, lockReason), http.StatusForbidden, "step_up_required", "a release with "+what)
	}
	if !lockedOut(t, w.member) || releases(t, w.member) != 0 {
		t.Fatalf("RELEASED — a refused release lifted the lock or wrote an act")
	}
}

func TestApiLocks03_TheAuditorCannotRelease(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.member)
	wantProblem(t, w.release(t, w.auditor, w.s, w.member, stepUp(t, w.auditor.user, "lock_release"), lockReason),
		http.StatusForbidden, "not_permitted", "the internal auditor releasing a lock")
	if !lockedOut(t, w.member) {
		t.Fatalf("RELEASED — by the read-only auditor")
	}
}

func TestApiLocks04_NobodyReleasesTheirOwnLock(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.secretary)
	wantProblem(t, w.release(t, w.secretary, w.s, w.secretary, stepUp(t, w.secretary.user, "lock_release"), lockReason),
		http.StatusForbidden, "conflict_of_interest", "the secretary releasing their own lock")
	if !lockedOut(t, w.secretary) {
		t.Fatalf("RELEASED — an officer lifted their own lock")
	}
}

// Anyone who is not an officer of the society, its members included, is told
// nothing is there: not that a member exists, not that one is locked.
func TestApiLocks05_SomeoneWhoIsNotAnOfficerHereIsToldNothing(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.member)
	for what, as := range map[string]signer{"another society's secretary": w.elsewhere, "a member": w.otherMember} {
		wantProblem(t, w.release(t, as, w.s, w.member, stepUp(t, as.user, "lock_release"), lockReason), http.StatusNotFound, "not_found", what+" releasing a lock")
	}
	if !lockedOut(t, w.member) {
		t.Fatalf("RELEASED — by someone who is not an officer of the society")
	}
}

// The member must be one of the society's: an officer of this society asking
// about a member of another is told nothing is there.
func TestApiLocks06_TheMemberIsTheSocietys(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.otherMember)
	wantProblem(t, w.release(t, w.secretary, w.s, w.otherMember, stepUp(t, w.secretary.user, "lock_release"), lockReason),
		http.StatusNotFound, "not_found", "a release through this society for another society's member")
	if !lockedOut(t, w.otherMember) {
		t.Fatalf("RELEASED — another society's member, by this society's secretary")
	}
}

func TestApiLocks07_AReleaseNeedsALock(t *testing.T) {
	w := newLockAPI(t)
	wantProblem(t, w.release(t, w.secretary, w.s, w.member, stepUp(t, w.secretary.user, "lock_release"), lockReason),
		http.StatusConflict, "not_locked", "a release for a member who is not locked out")
	if releases(t, w.member) != 0 {
		t.Fatalf("WRITTEN — a release with nothing locked was recorded")
	}
}

func TestApiLocks08_AReleaseStatesItsReason(t *testing.T) {
	w := newLockAPI(t)
	lockOut(t, w.member)
	for _, reason := range []string{"", "   ", "confirmed"} {
		wantProblem(t, w.release(t, w.secretary, w.s, w.member, stepUp(t, w.secretary.user, "lock_release"), reason),
			http.StatusUnprocessableEntity, "reason_required", "a release with the reason "+`"`+reason+`"`)
	}
	if !lockedOut(t, w.member) {
		t.Fatalf("RELEASED — with no reason given")
	}
}
