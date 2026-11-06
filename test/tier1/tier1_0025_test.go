//go:build tier1

// Suite 0025 — A REDEEMED CODE NAMES THE DEVICE IT TRUSTED; A USED SLIP SAYS
// SO TO ITS HOLDER. Written before the migration, for review.
//
// Accepted 2026-11-06 as a residual (LEDGER_CONTRACT.md §12): a slip is a
// bearer credential, and whoever holds another's slip can enrol as them. What
// makes it recoverable rather than silent:
//   - THE REDEMPTION RECORDS THE DEVICE AND THE TIME: redeeming creates the
//     trusted device and binds it to the code in one act, so an officer can
//     see from what device a slip was redeemed. A device bound to a code never
//     changes; a refused redemption trusts no device.
//   - A MEMBER WHOSE SLIP WAS ALREADY USED IS TOLD SO: when the code typed is
//     a code of theirs that was already used. Only then: a wrong code never
//     says "already used", so a stranger without the slip learns nothing, and
//     telling costs no try.
// The redemption before 0025 (two arguments, no device) is gone: a path that
// trusts a device without recording it is the gap this closes.
package tier1

import (
	"testing"
)

type redemption struct {
	code, device string
	alreadyUsed  bool
}

// redeemOn: the person types `code` on a new device whose token digest is `dev`.
func redeemOn(t *testing.T, user, code, dev string) redemption {
	t.Helper()
	var r redemption
	var c, d *string
	fixture(t, db.QueryRow(bg(), `SELECT code_id::text, device_id::text, coalesce(already_used, false)
		FROM enrolment_redeem($1, $2, $3)`, user, digestOf(code), dev).Scan(&c, &d, &r.alreadyUsed), "a redemption")
	if c != nil {
		r.code = *c
	}
	if d != nil {
		r.device = *d
	}
	return r
}

func devicesOf(t *testing.T, user string) int {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM auth_devices WHERE user_id = $1`, user).Scan(&n), "devices")
	return n
}

// The device is created by the redemption, for the code's person, and the
// code names it, with the time it was used.
func TestSlip01_ARedemptionTrustsOneDeviceAndTheCodeNamesIt(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	dev := digest()
	r := redeemOn(t, w.person, code, dev)
	if r.code != id || r.device == "" {
		t.Fatalf("NOT REDEEMED — %+v", r)
	}
	var owner, bound, token string
	var used bool
	fixture(t, db.QueryRow(bg(), `SELECT d.user_id::text, coalesce(c.used_device_id::text, ''), d.token_digest, c.used_at IS NOT NULL
		FROM enrolment_codes c, auth_devices d WHERE c.id = $1 AND d.id = $2`, id, r.device).Scan(&owner, &bound, &token, &used), "the code and its device")
	if owner != w.person || bound != r.device || token != dev || !used {
		t.Fatalf("UNBOUND — the device belongs to %s (want %s), the code names %q (want %s), token %s, used %v", owner, w.person, bound, r.device, token, used)
	}
}

func TestSlip02_ARefusedRedemptionTrustsNoDevice(t *testing.T) {
	w := newEnrolWorld(t)
	w.officerIssues(t, w.person, w.personMem)
	before := devicesOf(t, w.person)
	if r := redeemOn(t, w.person, "not-the-code", digest()); r.code != "" || r.device != "" {
		t.Fatalf("FIXTURE: a wrong code redeemed: %+v", r)
	}
	if after := devicesOf(t, w.person); after != before {
		t.Fatalf("TRUSTED ANYWAY — a wrong code left %d new device(s)", after-before)
	}
}

// Once bound, the device a code trusted is never rewritten.
func TestSlip03_TheBoundDeviceNeverChanges(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	redeemOn(t, w.person, code, digest())
	var other string
	fixture(t, db.QueryRow(bg(), `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id::text`, w.person, digest()).Scan(&other), "another device")
	refused(t, one(`UPDATE enrolment_codes SET used_device_id = $2 WHERE id = $1`, id, other), "a used code's device rewritten", refusedAs("enrolment_code_append_only"))
}

// The slip's holder is told it was used; nothing is counted, nothing trusted.
func TestSlip04_AUsedSlipSaysSoToItsHolder(t *testing.T) {
	w := newEnrolWorld(t)
	_, code := w.officerIssues(t, w.person, w.personMem)
	redeemOn(t, w.person, code, digest())
	before := devicesOf(t, w.person)
	r := redeemOn(t, w.person, code, digest())
	if !r.alreadyUsed || r.code != "" || r.device != "" {
		t.Fatalf("SILENT — the used slip, typed again: %+v; want already_used and nothing trusted", r)
	}
	if devicesOf(t, w.person) != before {
		t.Fatalf("TRUSTED — a used slip trusted another device")
	}
}

// "Already used" only for the slip itself: a wrong code says nothing, even
// when the person has a used code; and it costs the live code no try.
func TestSlip05_AWrongCodeNeverSaysAlreadyUsed(t *testing.T) {
	w := newEnrolWorld(t)
	_, code := w.officerIssues(t, w.person, w.personMem)
	redeemOn(t, w.person, code, digest())
	live, _ := w.officerIssues(t, w.person, w.personMem)
	if r := redeemOn(t, w.person, "not-any-code", digest()); r.alreadyUsed {
		t.Fatalf("LEAKED — a wrong code was told a slip had been used")
	}
	redeemOn(t, w.person, code, digest()) // the used slip again: told, not counted
	if got := codeState(t, live); got.tries != 1 {
		t.Fatalf("COUNTED — the live code has %d tries after one wrong code and one used slip; want 1", got.tries)
	}
}

// The two-argument redemption, which trusted no device, is gone.
func TestSlip06_NoRedemptionWithoutADevice(t *testing.T) {
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM pg_proc WHERE proname = 'enrolment_redeem' AND pronargs = 2`).Scan(&n), "the functions")
	if n != 0 {
		t.Fatalf("STILL THERE — enrolment_redeem(user, digest), which trusts no device, can still be called")
	}
}

func TestSlip_Control_TheRightSlipEnrols(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	if r := redeemOn(t, w.person, code, digest()); r.code != id || r.alreadyUsed {
		t.Fatalf("REFUSED — the person's own live slip: %+v", r)
	}
}
