//go:build tier1

// ACCEPTANCE TESTS — 0035, DEMONSTRATION SOCIETIES (2026-11-21). The reviewer:
// "demonstration accounts I can hand to people, with no activation step and no
// slips ... It must refuse to run against a society that was not created by
// it, and the society must be plainly marked as test data ... Build it so it
// cannot be aimed at anything real."
//
// What these hold: a society is a demonstration only when its own function
// made it, while nobody belonged to it, named TEST DATA; it stays one, and its
// name keeps saying so. The operator sets a PIN only for a person who belongs
// to demonstration societies and to nothing real, and says who did it (the PIN
// is a hash: auth_credentials refuses anything else, 0003). (Sign-in on the PIN alone: tier1_demo_service_test.go.)
package tier1

import (
	"fmt"
	"testing"
)

// demoSociety: a demonstration society, made by its function.
func demoSociety(t *testing.T) string {
	t.Helper()
	var id string
	fixture(t, db.QueryRow(bg(), `SELECT operator_create_demo_society($1, 'Demonstration', 'Test Operator', 'tester@host', 'a test')`,
		fmt.Sprintf("DEMOT%d", seq.Add(1))).Scan(&id), "a demonstration society")
	return id
}

type demoPerson struct{ user, member, number, phone string }

// personIn: a new person with a member record in society (its head office).
func personIn(t *testing.T, society, name string) demoPerson {
	t.Helper()
	p := demoPerson{number: fmt.Sprintf("D-%d", seq.Add(1)), phone: fmt.Sprintf("+23480%08d", seq.Add(1))}
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ($1) RETURNING id`, name).Scan(&p.user), "a user")
	p.member = memberOf(t, society, p.user, p.number, p.phone)
	return p
}

func memberOf(t *testing.T, society, user, number, phone string) string {
	t.Helper()
	var m string
	fixture(t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id)
		SELECT $1, b.id, $2, u.full_name, $3, 'payroll', '2020-01-01', u.id FROM branches b, users u
		 WHERE b.society_id = $1 AND u.id = $4 ORDER BY b.code LIMIT 1 RETURNING id`, society, number, phone, user).Scan(&m), "a member record")
	return m
}

func setDemoPIN(user, hash, operator, login, reason string) error {
	return one(`SELECT operator_set_demo_pin($1, $2, $3, $4, $5)`, user, hash, operator, login, reason)
}

func TestDemo01_ADemonstrationIsMadeByItsFunctionAndSaysTestData(t *testing.T) {
	refused(t, one(`INSERT INTO societies (code, registered_name, registration_number, state_directorate, demo)
		VALUES ($1, 'TEST DATA: inserted', 'X', 'Lagos', true)`, fmt.Sprintf("DEMOI%d", seq.Add(1))),
		"a society inserted already marked a demonstration", refusedAs("demo_by_its_function"))
	id := demoSociety(t)
	var demo bool
	var name string
	fixture(t, db.QueryRow(bg(), `SELECT demo, registered_name FROM societies WHERE id = $1`, id).Scan(&demo, &name), "the society")
	if !demo || name != "TEST DATA: Demonstration" {
		t.Fatalf("UNMARKED — the function's society reads demo %v, %q; want a demonstration named \"TEST DATA: Demonstration\"", demo, name)
	}
}

func TestDemo02_ADemonstrationStaysOneAndKeepsSayingSo(t *testing.T) {
	id := demoSociety(t)
	refused(t, one(`UPDATE societies SET demo = false WHERE id = $1`, id), "a demonstration made real", refusedAs("demo_is_permanent"))
	refused(t, one(`UPDATE societies SET registered_name = 'Ikeja Teachers Cooperative' WHERE id = $1`, id),
		"a demonstration renamed to look real", refusedAs("demo_says_test_data"))
}

// The function marks the society it just made; nothing marks a society that
// people already belong to, which is how it cannot be aimed at a real one.
func TestDemo03_ASocietyPeopleBelongToIsNeverMadeADemonstration(t *testing.T) {
	s := newSociety(t)
	fixture(t, one(`UPDATE societies SET registered_name = 'TEST DATA: was real' WHERE id = $1`, s.id), "a TEST DATA name")
	s.member(s.user("A real member"))
	refused(t, one(`UPDATE societies SET demo = true WHERE id = $1`, s.id), "a society with members made a demonstration", refusedAs("demo_only_when_empty"))
}

func TestDemo04_ADemonstrationIsNamedTestData(t *testing.T) {
	var id string // empty, and named as a real society is
	fixture(t, db.QueryRow(bg(), `INSERT INTO societies (code, registered_name, registration_number, state_directorate)
		VALUES ($1, 'Ikeja Teachers Cooperative', 'LSCS/2019/1', 'Lagos') RETURNING id`, fmt.Sprintf("REAL%d", seq.Add(1))).Scan(&id), "an empty society")
	refused(t, one(`UPDATE societies SET demo = true WHERE id = $1`, id), "an empty society with a real name made a demonstration", refusedAs("demo_says_test_data"))
}

// The operator's PIN: only for someone in demonstration societies and nothing real.
func TestDemo05_TheOperatorSetsAPinOnlyForADemonstrationsPeople(t *testing.T) {
	demo := demoSociety(t)
	real := newSociety(t)
	r := personIn(t, real.id, "A real member")
	refused(t, setDemoPIN(r.user, hashPIN("380915"), "Test Operator", "tester@host", "a test"), "a PIN set for a real society's member", refusedAs("demo_pin_only_for_demo"))
	both := personIn(t, demo, "In both")
	memberOf(t, real.id, both.user, fmt.Sprintf("R-%d", seq.Add(1)), fmt.Sprintf("+23481%08d", seq.Add(1)))
	refused(t, setDemoPIN(both.user, hashPIN("380915"), "Test Operator", "tester@host", "a test"),
		"a PIN set for someone in a demonstration AND a real society", refusedAs("demo_pin_only_for_demo"))
	var nobody string
	fixture(t, db.QueryRow(bg(), `INSERT INTO users (full_name) VALUES ('Belongs nowhere') RETURNING id`).Scan(&nobody), "a user")
	refused(t, setDemoPIN(nobody, hashPIN("380915"), "Test Operator", "tester@host", "a test"), "a PIN set for someone in no society", refusedAs("demo_pin_only_for_demo"))
}

func TestDemo06_TheOperatorSaysWhoAndWhy(t *testing.T) {
	p := personIn(t, demoSociety(t), "Demo member")
	refused(t, setDemoPIN(p.user, hashPIN("380915"), "", "tester@host", "a test"), "a PIN set by nobody named", refusedAs("operator_names_themselves"))
	refused(t, setDemoPIN(p.user, hashPIN("380915"), "Test Operator", "", "a test"), "a PIN set from no login", refusedAs("operator_login_recorded"))
	refused(t, setDemoPIN(p.user, hashPIN("380915"), "Test Operator", "tester@host", ""), "a PIN set for no reason", refusedAs("operator_gives_reason"))
}

// The control: a demonstration's own person gets the PIN, and the record says who.
func TestDemo_Control_ADemonstrationsPersonGetsTheOperatorsPin(t *testing.T) {
	p := personIn(t, demoSociety(t), "Demo member")
	accepted(t, setDemoPIN(p.user, hashPIN("380915"), "Test Operator", "tester@host", "a test"), "a PIN for a demonstration's person")
	var n, logged int
	fixture(t, db.QueryRow(bg(), `SELECT (SELECT count(*) FROM auth_credentials WHERE user_id = $1), (SELECT count(*) FROM demo_pins WHERE user_id = $1 AND operator = 'Test Operator')`, p.user).Scan(&n, &logged), "the PIN")
	if n != 1 || logged != 1 {
		t.Fatalf("NOT SET — %d PIN(s), %d record(s) of who set it; want one of each", n, logged)
	}
}
