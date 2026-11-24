//go:build tier1

// ACCEPTANCE TESTS — 0036, A FOUNDING OFFICER IS AN ACTIVE MEMBER (2026-11-22).
// members.status defaults to 'pending', and the founding officer's function
// never said otherwise, so every founding officer sat in a state nobody chose
// (and, pending, could not guarantee a loan: 0008). The function now says
// 'active'; the officers it already made are made active by the backfill,
// only founding officers, and only while still pending.
package tier1

import (
	"fmt"
	"testing"
)

// prospect: a society being founded, by the operator's function.
func prospect(t *testing.T) string {
	t.Helper()
	var id string
	extra := ""
	if foundingTakesWindow() {
		extra = " 7, 10000000,"
	}
	fixture(t, db.QueryRow(bg(), `SELECT operator_create_society($1, 'Ikeja Teachers Cooperative', 'LSCS/2019/0412', 'Lagos', 2, 2, 2,`+extra+`
		'Test Operator', 'test@host', 'Founding resolution', current_date, 'a test')`, fmt.Sprintf("FA%d", seq.Add(1))).Scan(&id), "a society being founded")
	return id
}

func appointFounding(t *testing.T, society string, post ...string) string {
	t.Helper()
	var m string
	p := "chairman"
	if len(post) == 1 {
		p = post[0]
	}
	fixture(t, db.QueryRow(bg(), `SELECT operator_appoint_founding_officer($1, 'Founding Officer', $2, $3, '2024-01-15', $4::officer_post, true,
		'Test Operator', 'test@host', 'Founding resolution', current_date, 'a test')`,
		society, fmt.Sprintf("+23470%08d", seq.Add(1)), fmt.Sprintf("F-%d", seq.Add(1)), p).Scan(&m), "a founding officer")
	return m
}

func statusOf(t *testing.T, member string) string {
	t.Helper()
	var s string
	fixture(t, db.QueryRow(bg(), `SELECT status::text FROM members WHERE id = $1`, member).Scan(&s), "a member's status")
	return s
}

func setStatus(t *testing.T, member, status string) {
	t.Helper()
	fixture(t, one(`UPDATE members SET status = $2::member_status WHERE id = $1`, member, status), "a status as it was")
}

func backfill(t *testing.T) {
	t.Helper()
	fixture(t, one(`SELECT backfill_founding_officers_active()`), "the backfill")
}

func TestOfficerActive01_AFoundingOfficerIsAnActiveMember(t *testing.T) {
	m := appointFounding(t, prospect(t))
	if s := statusOf(t, m); s != "active" {
		t.Fatalf("NOT CHOSEN — a founding officer is %q; want active, said by the function, not left to the column's default", s)
	}
}

// The officers appointed before 0036 were pending; the backfill makes them active.
func TestOfficerActive02_TheFoundingOfficersAlreadyAppointedAreMadeActive(t *testing.T) {
	m := appointFounding(t, prospect(t))
	setStatus(t, m, "pending") // as 0027 left them
	backfill(t)
	if s := statusOf(t, m); s != "active" {
		t.Fatalf("LEFT PENDING — a founding officer appointed before 0036 is %q after the backfill; want active", s)
	}
}

// Only while still pending: a status someone has since chosen stands.
func TestOfficerActive03_AStatusSomeoneChoseIsNotOverwritten(t *testing.T) {
	soc := prospect(t)
	left, chosen := appointFounding(t, soc, "chairman"), appointFounding(t, soc, "treasurer")
	setStatus(t, left, "pending")
	setStatus(t, chosen, "suspended")
	backfill(t)
	if a, b := statusOf(t, left), statusOf(t, chosen); a != "active" || b != "suspended" {
		t.Fatalf("WRONG — after the backfill the pending officer is %q and the suspended one %q; want active and suspended (a choice is never overwritten)", a, b)
	}
}

// The control: nobody but a founding officer is touched.
func TestOfficerActive_Control_OnlyFoundingOfficersAreTouched(t *testing.T) {
	soc := prospect(t)
	var u, other string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Not a founding officer') RETURNING id`).Scan(&u), "a user")
	other = memberOf(t, soc, u, fmt.Sprintf("N-%d", seq.Add(1)), fmt.Sprintf("+23471%08d", seq.Add(1)))
	setStatus(t, other, "pending")
	backfill(t)
	if s := statusOf(t, other); s != "pending" {
		t.Fatalf("TOUCHED — a member who is not a founding officer became %q; the backfill is for founding officers only", s)
	}
}
