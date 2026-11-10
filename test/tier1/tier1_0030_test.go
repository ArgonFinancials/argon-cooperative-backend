//go:build tier1

// Suite 0030 — IS THIS THE PERSON'S OWN SLIP, ALREADY USED? (2026-11-10).
// The question the auth service asks for someone who says they have never
// had a PIN, so that the member whose slip was taken is told it was used (§12)
// and not refused like a typo. It answers only for the person's own spent
// slip, and spends, counts and touches nothing.
package tier1

import "testing"

func slipUsed(t *testing.T, user, code string) bool {
	t.Helper()
	var used bool
	fixture(t, db.QueryRow(bg(), `SELECT enrolment_slip_used($1, $2)`, user, digestOf(code)).Scan(&used), "the question")
	return used
}

func TestSpentSlip01_ThePersonsOwnUsedSlipIsSaidUsed(t *testing.T) {
	w := newEnrolWorld(t)
	_, code := w.officerIssues(t, w.person, w.personMem)
	if redeem(t, w.person, code) == "" {
		t.Fatalf("FIXTURE: the slip was not redeemed")
	}
	if !slipUsed(t, w.person, code) {
		t.Fatalf("UNTOLD — the person's own slip, used by someone else, is not said to be used: the robbed member is refused like a typo")
	}
}

// A live slip is never said used, and asking does not spend it: a live code
// is redeemed only with the PIN (D-15), never by this question.
func TestSpentSlip02_ALiveSlipIsNotSaidUsedAndIsUntouched(t *testing.T) {
	w := newEnrolWorld(t)
	id, code := w.officerIssues(t, w.person, w.personMem)
	if slipUsed(t, w.person, code) {
		t.Fatalf("WRONG — a live slip is said to be used")
	}
	if c := codeState(t, id); c.used || c.tries != 0 {
		t.Fatalf("TOUCHED — asking spent or counted the live slip: %+v", c)
	}
}

func TestSpentSlip03_AnotherPersonsUsedSlipIsNotThisPersons(t *testing.T) {
	w := newEnrolWorld(t)
	_, code := w.officerIssues(t, w.other, w.otherMem)
	if redeem(t, w.other, code) == "" {
		t.Fatalf("FIXTURE: the other's slip was not redeemed")
	}
	if slipUsed(t, w.person, code) {
		t.Fatalf("WRONG — another person's used slip is said to be this person's: it tells a stranger whose slip it was")
	}
}

func TestSpentSlip04_AWrongCodeCountsNothing(t *testing.T) {
	w := newEnrolWorld(t)
	id, _ := w.officerIssues(t, w.person, w.personMem)
	wrong, _ := slip()
	if slipUsed(t, w.person, wrong) {
		t.Fatalf("WRONG — a code never issued is said to be used")
	}
	if c := codeState(t, id); c.tries != 0 {
		t.Fatalf("COUNTED — asking with a wrong code took a try from the live slip (%d): only a redemption counts", c.tries)
	}
}
