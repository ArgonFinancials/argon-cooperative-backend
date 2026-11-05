//go:build tier1

// ACCEPTANCE TESTS — 0022: THE LOCKOUT, SPLIT BY DEVICE (decided 2026-11-05).
//
// 0003 counted every wrong PIN against one lock per account, whatever device
// it came from, and its third lockout in 24 h waited for an officer. Anyone
// who knew a member's number could make fifteen calls and lock the member's
// own phone until an officer acted (finding 60). And the officer lock never
// lasted: lock_state read only the last 48 hours of attempts, so the lock
// ended by itself two days later (finding 62).
//
// THE RULE NOW. Two locks per account:
//   - the member's OWN devices (an attempt naming a device this server
//     trusted for them; the stolen-phone case): 15 min, 30 min, then locked
//     until an officer releases it, however long that takes;
//   - ANY OTHER device, and an unregistered number exactly alike: 15 min,
//     30 min, then 48 hours. Never the officer: there, a right PIN only sends
//     a code to the member's phone, and the code already does the officer's job.
// One never resets or locks the other. An officer's release clears both.
package tier1

import (
	"testing"
	"time"
)

func (a *authT) lockElsewhere(user string) (until *time.Time, officer bool) {
	a.scan("lock_state_unknown_device", `SELECT locked_until, needs_officer FROM lock_state_unknown_device($1)`, []any{&until, &officer}, user)
	return
}

func (a *authT) lockCredential(d string) (until *time.Time, officer bool) {
	a.scan("lock_state_for_credential", `SELECT locked_until, needs_officer FROM lock_state_for_credential($1)`, []any{&until, &officer}, d)
	return
}

// strangerFails: wrong PINs from a device that is not the member's own.
func (a *authT) strangerFails(user string, n int) {
	for i := 0; i < n; i++ {
		a.attempt(user, false, nil)
	}
}

// unknownFails: wrong PINs for a number no one is registered with.
func (a *authT) unknownFails(d string, n int) {
	a.t.Helper()
	for i := 0; i < n; i++ {
		fixture(a.t, a.exec(`INSERT INTO sign_in_attempts (user_id, credential_digest, device_id, succeeded) VALUES (NULL, $1, NULL, false)`, d), "unregistered attempt")
	}
}

func (a *authT) wantTimedLock(what string, until *time.Time, officer bool, d time.Duration) {
	a.t.Helper()
	if officer {
		a.t.Fatalf("OFFICER — %s: locked until an officer acts; want a timed lock of %s", what, d)
	}
	if until == nil {
		a.t.Fatalf("ACCEPTED — %s: not locked at all; want %s", what, d)
	}
	if got := until.Sub(a.now); got < d-time.Second || got > d+time.Second {
		a.t.Fatalf("WRONG LOCK — %s: locked for %s, want %s", what, got.Round(time.Second), d)
	}
}

// threeStrangerLockouts: three lockouts within the hour, from devices that
// are not the member's own.
func (a *authT) threeStrangerLockouts(u string) {
	a.strangerFails(u, 5)
	a.advance(16 * time.Minute)
	a.strangerFails(u, 5)
	a.advance(31 * time.Minute)
	a.strangerFails(u, 5)
}

// THE FINDING (62): "waits for an officer" was never true. The lock was
// computed from the last 48 hours of attempts, so two days later the
// failures that caused it were no longer read, and the lock was gone.
func TestDeviceLock01_TheOfficerLockOutlastsTwoDays(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.threeLockouts(u)
	if _, officer := a.lock(u); !officer {
		t.Fatalf("FIXTURE: three lockouts on the member's own phone did not reach the officer")
	}
	for _, after := range []time.Duration{49 * time.Hour, 7 * 24 * time.Hour} {
		a.advance(after)
		if _, officer := a.lock(u); !officer {
			t.Fatalf("RELEASED ITSELF — the officer lock on the member's own phone ended on its own after %s: a thief holding the phone gets fifteen more guesses", after)
		}
	}
}

// The same, on a phone with no success on it to measure from (stolen right
// after it was enrolled): every failure counts, however old.
func TestDeviceLock10_TheOfficerLockLastsWithNoSuccessToMeasureFrom(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 5)
	a.advance(16 * time.Minute)
	a.fails(u, 5)
	a.advance(31 * time.Minute)
	a.fails(u, 5)
	if _, officer := a.lock(u); !officer {
		t.Fatalf("FIXTURE: three lockouts on the member's own phone did not reach the officer")
	}
	a.advance(4 * 24 * time.Hour)
	if _, officer := a.lock(u); !officer {
		t.Fatalf("RELEASED ITSELF — on a phone never signed in on, the officer lock ended on its own after four days")
	}
}

// A stranger who knows the number reaches the member's own phone not at all.
func TestDeviceLock02_AStrangersFailuresNeverLockTheMembersOwnPhone(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.threeStrangerLockouts(u)
	if until, officer := a.lockElsewhere(u); until == nil && !officer {
		t.Fatalf("FIXTURE: fifteen wrong PINs from elsewhere locked nothing at all")
	}
	if until, officer := a.lock(u); until != nil || officer {
		t.Fatalf("LOCKED OUT — fifteen wrong PINs from a stranger's device locked the member's own phone (until %v, officer %v)", until, officer)
	}
}

func TestDeviceLock03_AStrangerReachesTimedLocksOnly(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.strangerFails(u, 5)
	until, officer := a.lockElsewhere(u)
	a.wantTimedLock("five wrong PINs from elsewhere", until, officer, 15*time.Minute)
	a.advance(16 * time.Minute)
	a.strangerFails(u, 5)
	until, officer = a.lockElsewhere(u)
	a.wantTimedLock("the second lockout from elsewhere", until, officer, 30*time.Minute)
	a.advance(31 * time.Minute)
	a.strangerFails(u, 5)
	until, officer = a.lockElsewhere(u)
	a.wantTimedLock("the third lockout from elsewhere in 24 h", until, officer, 48*time.Hour)
}

// The lockouts spread across a day, so the first is 23 h older than the
// third: the lock must still read it when the third is 46 hours old.
func TestDeviceLock04_TheStrangerLockLastsItsFullFortyEightHours(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.strangerFails(u, 5)
	a.advance(23 * time.Hour)
	a.strangerFails(u, 5)
	a.advance(31 * time.Minute)
	a.strangerFails(u, 5)
	// Locked at all: how long is TestDeviceLock03's question.
	if until, officer := a.lockElsewhere(u); until == nil && !officer {
		t.Fatalf("FIXTURE: three lockouts from elsewhere in 24 h locked nothing")
	}
	a.advance(46 * time.Hour)
	if until, _ := a.lockElsewhere(u); until == nil {
		t.Fatalf("RELEASED EARLY — the 48-hour lock ended at 46 hours: its first lockout fell out of what the lock reads")
	}
	a.advance(3 * time.Hour)
	if until, officer := a.lockElsewhere(u); until != nil || officer {
		t.Fatalf("NEVER ENDS — the 48-hour lock was still on at 49 hours (until %v, officer %v)", until, officer)
	}
}

// Sign-in must never say which numbers are registered: an unregistered
// number locks exactly as a registered one does from elsewhere.
func TestDeviceLock05_AnUnknownNumberLocksExactlyLikeAStranger(t *testing.T) {
	a := newAuth(t)
	d := digest()
	a.unknownFails(d, 5)
	a.advance(16 * time.Minute)
	a.unknownFails(d, 5)
	a.advance(31 * time.Minute)
	a.unknownFails(d, 5)
	until, officer := a.lockCredential(d)
	a.wantTimedLock("the third lockout in 24 h on an unregistered number", until, officer, 48*time.Hour)
}

func TestDeviceLock06_Control_TheMembersOwnPhoneStillReachesTheOfficer(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.threeLockouts(u)
	a.advance(3 * time.Hour)
	if _, officer := a.lock(u); !officer {
		t.Fatalf("NO OFFICER — three lockouts in 24 h on the member's own phone released themselves")
	}
}

// The member is in front of the officer: whatever locked them, they leave
// able to sign in.
func TestDeviceLock07_AReleaseClearsBothLocks(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	a.threeLockouts(m.user)
	a.threeStrangerLockouts(m.user)
	o := a.officer("Secretary", "secretary", false)
	_, err := a.act(act{kind: "lock_release", actor: o.user, proof: a.proof(o.user, "pin", "lock_release"), targetUser: m.user, targetMember: m.member, reason: why})
	accepted(t, err, "an officer releases the lock")
	if until, officer := a.lock(m.user); until != nil || officer {
		t.Fatalf("STILL LOCKED — after the release, the member's own phone: until %v, officer %v", until, officer)
	}
	if until, officer := a.lockElsewhere(m.user); until != nil || officer {
		t.Fatalf("STILL LOCKED — after the release, a new phone: until %v, officer %v", until, officer)
	}
}

// A release starts a clean count: what came before it is forgiven, what
// comes after it counts from one.
func TestDeviceLock08_AFailureAfterAReleaseStartsANewCount(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	a.threeLockouts(m.user)
	o := a.officer("Secretary", "secretary", false)
	_, err := a.act(act{kind: "lock_release", actor: o.user, proof: a.proof(o.user, "pin", "lock_release"), targetUser: m.user, targetMember: m.member, reason: why})
	fixture(t, err, "the release")
	a.advance(time.Minute)
	a.fails(m.user, 5)
	until, officer := a.lock(m.user)
	a.wantTimedLock("five wrong PINs after a release", until, officer, 15*time.Minute)
}

// A release forgives every failure before it: one made while nothing is
// locked would hand whoever holds the phone a fresh count, for nothing.
func TestDeviceLock11_AReleaseNeedsALock(t *testing.T) {
	a := newAuth(t)
	m := a.who("Member")
	a.fails(m.user, 4)
	o := a.officer("Secretary", "secretary", false)
	_, err := a.act(act{kind: "lock_release", actor: o.user, proof: a.proof(o.user, "pin", "lock_release"), targetUser: m.user, targetMember: m.member, reason: why})
	refused(t, err, "a release for a member who is not locked out", w("23514", "release_needs_a_lock"))
}

// A right PIN from a stranger's device (it then needs the code) does not wipe
// the run of failures on the member's own phone, and the reverse.
func TestDeviceLock09_OneLockNeverResetsTheOther(t *testing.T) {
	a := newAuth(t)
	u := a.who("Member").user
	a.fails(u, 4)
	a.advance(time.Second)
	a.attempt(u, true, nil) // the right PIN, from elsewhere
	a.advance(time.Second)
	a.fails(u, 1)
	until, officer := a.lock(u)
	a.wantTimedLock("five wrong PINs on the member's own phone around a success elsewhere", until, officer, 15*time.Minute)
}
