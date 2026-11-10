//go:build tier1

// Suite 0027 — THE FOUNDING OFFICERS, APPOINTED BY THE OPERATOR ON THE
// FOUNDING RESOLUTION. Written before the migration (stage 1, 2026-11-10).
//
// A society is founded by the operator (0023), and its first devices are
// enrolment codes the operator issues to its founding officers (0024).
// Between the two, nothing could create those officers: no API creates a
// member or an appointment, and there is no Argon staff identity to give one
// (§12). So the operator carries out the resolution here too, as every
// operator act: named, the login, why, and the resolution it rests on.
//
//   ATTRIBUTED: the operator, the server login, a reason, the resolution's
//   reference and date (not in the future), as 0014 and 0023.
//   IDENTIFIED: a name, a phone and the society's own member number.
//   ONLY WHILE FOUNDING: while the society is a prospect. After that, its
//   officers are the society's to appoint, never the operator's.
//   ONE ACT: the person, their member record at the head office, the
//   appointment (held from the resolution's date) and the log row, together.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

type founder struct {
	society, name, phone, number, joined, post string
	signatory                                  bool
	operator, login, ref, date, reason         string
}

func aFounder(t *testing.T) founder {
	t.Helper()
	s := newSociety(t)
	n := seq.Add(1)
	return founder{society: s.id, name: "Ngozi Eze", phone: fmt.Sprintf("+23481%08d", n), number: fmt.Sprintf("F-%d", n),
		joined: "2019-03-01", post: "chairman", signatory: true,
		operator: opName, login: opHost, ref: "Minutes of the founding meeting, item 4", date: "2026-11-01",
		reason: "Appointing the founding chairman, on the founding resolution"}
}

func appointFounder(f founder) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `SELECT operator_appoint_founding_officer($1, $2, $3, $4, $5::date, $6::officer_post, $7, $8, $9, $10, $11::date, $12)::text`,
			f.society, f.name, f.phone, f.number, f.joined, f.post, f.signatory, nz(f.operator), nz(f.login), nz(f.ref), nz(f.date), nz(f.reason)).Scan(&id)
	})
	return id, err
}

func nobodyAppointed(t *testing.T, f founder) {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT (SELECT count(*) FROM members WHERE society_id = $1) + (SELECT count(*) FROM appointments WHERE society_id = $1)`, f.society).Scan(&n), "members and appointments")
	if n != 0 {
		t.Fatalf("WRITTEN — a refused appointment left %d member or appointment row(s)", n)
	}
}

// =============================================================================
// ATTRIBUTED
// =============================================================================

func TestFirstOfficer01_TheOperatorNamesThemselvesTheLoginAndWhy(t *testing.T) {
	for _, c := range []struct {
		set  func(*founder)
		code string
	}{
		{func(f *founder) { f.operator = " " }, "operator_names_themselves"},
		{func(f *founder) { f.login = "" }, "operator_login_recorded"},
		{func(f *founder) { f.reason = "" }, "operator_gives_reason"},
	} {
		f := aFounder(t)
		c.set(&f)
		_, err := appointFounder(f)
		refused(t, err, "a founding officer appointed without "+c.code, refusedAs(c.code))
		nobodyAppointed(t, f)
	}
}

func TestFirstOfficer02_ItRestsOnTheResolution(t *testing.T) {
	f := aFounder(t)
	f.ref = ""
	_, err := appointFounder(f)
	refused(t, err, "a founding officer appointed on no resolution", refusedAs("society_resolution_required"))
	f = aFounder(t)
	f.date = "2099-01-01"
	_, err = appointFounder(f)
	refused(t, err, "a founding officer appointed on a resolution dated in the future", refusedAs("society_resolution_not_future"))
	nobodyAppointed(t, f)
}

func TestFirstOfficer03_TheOfficerIsIdentified(t *testing.T) {
	for _, set := range []func(*founder){
		func(f *founder) { f.name = " " }, func(f *founder) { f.phone = "" }, func(f *founder) { f.number = "" },
	} {
		f := aFounder(t)
		set(&f)
		_, err := appointFounder(f)
		refused(t, err, "a founding officer without a name, phone or member number", refusedAs("founding_officer_identified"))
		nobodyAppointed(t, f)
	}
}

// After founding, the society appoints its officers; the operator never does.
func TestFirstOfficer04_OnlyWhileTheSocietyIsBeingFounded(t *testing.T) {
	f := aFounder(t)
	fixture(t, one(`UPDATE societies SET lifecycle = 'active' WHERE id = $1`, f.society), "an active society")
	_, err := appointFounder(f)
	refused(t, err, "the operator appointing an officer of an active society", refusedAs("founding_officer_while_founding"))
	nobodyAppointed(t, f)
}

// One act: the person, the member at the head office, the appointment from
// the resolution's date, and the log row that says who and why.
func TestFirstOfficer05_OneActWritesThePersonTheMemberThePostAndTheLog(t *testing.T) {
	f := aFounder(t)
	member, err := appointFounder(f)
	accepted(t, err, "the operator appointing the founding chairman")
	var name, phone, number, branch, post, from, logged string
	var signs bool
	fixture(t, db.QueryRow(bg(), `SELECT u.full_name, m.phone, m.member_number, b.code, a.post::text, a.held_from::text, a.is_signatory,
		coalesce((SELECT new_value || '|' || operator_name || '|' || resolution_ref FROM society_settings_log WHERE society_id = m.society_id AND setting = 'founding_officer'), '')
		FROM members m JOIN users u ON u.id = m.user_id JOIN branches b ON b.id = m.branch_id
		JOIN appointments a ON a.member_id = m.id WHERE m.id = $1`, member).
		Scan(&name, &phone, &number, &branch, &post, &from, &signs, &logged), "the founding officer")
	switch {
	case name != f.name || phone != f.phone || number != f.number:
		t.Fatalf("WRONG PERSON — %s %s %s", name, phone, number)
	case branch != "HQ":
		t.Fatalf("WRONG BRANCH — the founding officer's record is at %s; want the head office", branch)
	case post != f.post || !signs || from != f.date:
		t.Fatalf("WRONG POST — %s, signs %v, from %s; want chairman, signing, from the resolution's date %s", post, signs, from, f.date)
	case logged != "chairman:"+f.number+"|"+f.operator+"|"+f.ref:
		t.Fatalf("UNLOGGED — the log reads %q", logged)
	}
	if !personHoldsPost(t, member) {
		t.Fatalf("NO POST — the founding chairman holds no current post, so the operator could not issue their code")
	}
}

func personHoldsPost(t *testing.T, member string) bool {
	t.Helper()
	var ok bool
	fixture(t, db.QueryRow(bg(), `SELECT person_holds_post(user_id, society_id) FROM members WHERE id = $1`, member).Scan(&ok), "the post")
	return ok
}

// The control: the operator then issues the founding chairman's code.
func TestFirstOfficer_Control_TheOperatorThenIssuesTheirCode(t *testing.T) {
	f := aFounder(t)
	member, err := appointFounder(f)
	accepted(t, err, "the operator appointing the founding chairman")
	_, d := slip()
	_, err = operatorIssue(member, d, opName, opHost, enrolWhy)
	accepted(t, err, "the operator issuing the founding chairman's code")
}
