//go:build tier1

// ACCEPTANCE TESTS — 0020: ONE ACCOUNT PER ROLE IN A SOCIETY (onboarding piece
// A, approved 2026-10-30; a finding of its own). A role names where the code
// posts; two accounts of one role is a chart the code can only read by
// guessing. 'general' accounts are the society's own and may be many; each
// society has its own chart.
package tier1

import (
	"fmt"
	"testing"
)

// postingRoles: every role the code posts to by role (all but 'general').
var postingRoles = []struct{ role, kind string }{
	{"member_savings", "liability"}, {"member_wallet", "liability"}, {"member_loan", "asset"}, {"bank", "asset"},
	{"charges_partner", "liability"}, {"charges_vat", "liability"}, {"charges_margin", "liability"},
	{"statutory_levy_payable", "liability"}, {"migration_control", "equity"}, {"loan_loss", "expense"},
	{"loan_recovery_income", "income"},
}

func addAccount(society, code, role, kind string) error {
	return one(`INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, $2, $3, $4, $3)`, society, code, role, kind)
}

func TestChartRole01_ASecondAccountOfARoleIsRefused(t *testing.T) {
	for i, r := range postingRoles {
		s := newSociety(t) // already holds one bank and one member_savings account
		var have bool
		fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM accounts WHERE society_id = $1 AND role = $2)`, s.id, r.role).Scan(&have), "the chart")
		if !have {
			fixture(t, addAccount(s.id, fmt.Sprintf("8%03d", i), r.role, r.kind), "the first "+r.role)
		}
		refused(t, addAccount(s.id, fmt.Sprintf("9%03d", i), r.role, r.kind),
			"a second "+r.role+" account in one society", w("23505", "account_role_once"))
	}
}

func TestChartRole02_Control_GeneralAccountsAreMany(t *testing.T) {
	s := newSociety(t)
	for i := 0; i < 3; i++ {
		accepted(t, addAccount(s.id, fmt.Sprintf("50%02d", i), "general", "expense"), "the society's own general account")
	}
}

func TestChartRole03_Control_EachSocietyHasItsOwn(t *testing.T) {
	// Bare societies, no chart: newSociety's own accounts would trip an
	// over-strict rule in the fixture, before this control could say so.
	for i := 0; i < 2; i++ {
		var id string
		fixture(t, db.QueryRow(bg(), `INSERT INTO societies (code, registered_name, registration_number, state_directorate)
			VALUES ($1, 'Bare society', 'REG', 'Lagos') RETURNING id`, fmt.Sprintf("BARE%d", seq.Add(1))).Scan(&id), "a bare society")
		accepted(t, addAccount(id, "2201", "member_wallet", "liability"), "member_wallet in each of two societies")
	}
}

// suspendRoleOnce: for a test of a SECOND defence (the code refusing a chart
// the database already refuses): the index is dropped for this test only and
// restored after it. Tests in this package never run in parallel.
func suspendRoleOnce(t *testing.T) {
	t.Helper()
	var had bool
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'account_role_once')`).Scan(&had), "the index")
	if !had {
		return
	}
	fixture(t, one(`DROP INDEX account_role_once`), "suspending account_role_once")
	t.Cleanup(func() {
		// A chart this test made may now break the rule: relabel its extras so
		// the rule can come back exactly as it was.
		// The extra is the one with no lines, kept beside one that has lines
		// (or, if neither has, the higher code).
		one(`UPDATE accounts a SET role = 'general' WHERE role <> 'general'
			AND NOT EXISTS (SELECT 1 FROM ledger_lines l WHERE l.account_id = a.id)
			AND EXISTS (SELECT 1 FROM accounts b WHERE b.society_id = a.society_id AND b.role = a.role AND b.id <> a.id
			            AND (EXISTS (SELECT 1 FROM ledger_lines l WHERE l.account_id = b.id) OR b.code < a.code))`)
		if err := one(`CREATE UNIQUE INDEX account_role_once ON accounts (society_id, role) WHERE role <> 'general'`); err != nil {
			t.Fatalf("FIXTURE: account_role_once could not be restored: %v", err)
		}
	})
}
