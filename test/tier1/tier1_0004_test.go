//go:build tier1

// Acceptance tests for migration 0004 — account roles. Written before 0004,
// for review. Every TestRole* name is in this suite.
//
// A role says what an account IS, independent of its society-specific code.
// Roles are where accounts meet lines, so the walk-arounds live there: a
// savings line that names no member, a member's line on the bank account, a
// partner charge on the margin account, and relabelling an account's role
// after lines exist — which reinterprets history without changing a line.
package tier1

import (
	"fmt"
	"testing"
)

// chart: one account per role in a fresh society, plus a member to own lines.
type chart struct {
	s                                                         *soc
	savings, wallet, loan, bank, partner, margin, migration   string
	member                                                    string
}

func newChart(t *testing.T) *chart {
	t.Helper()
	c := &chart{s: newSociety(t)}
	for _, a := range []struct {
		dst        *string
		code, kind string
		role       string
	}{
		{&c.savings, "2001", "liability", "member_savings"},
		{&c.wallet, "2201", "liability", "member_wallet"},
		{&c.loan, "1201", "asset", "member_loan"},
		{&c.bank, "1001", "asset", "bank"},
		{&c.partner, "2101", "liability", "charges_partner"},
		{&c.margin, "2111", "liability", "charges_margin"},
		{&c.migration, "3901", "equity", "migration_control"},
	} {
		fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			c.s.id, a.code, a.role, a.kind, a.role).Scan(a.dst), "account "+a.role)
	}
	c.member = c.s.member("")
	return c
}

// post writes one entry with the given lines in one transaction.
func (c *chart) post(ls ...line) error {
	_, err := c.s.postEntry(entry{}, ls...)
	return err
}

func TestRoleR1_AccountNeedsARole(t *testing.T) {
	s := newSociety(t)
	refused(t, one(`INSERT INTO accounts (society_id, code, name, kind) VALUES ($1, '9001', 'No role', 'asset')`, s.id),
		"an account with no role",
		want{codes: []string{"23502"}, column: "role"},
		want{codes: []string{"23514"}, constraint: "account_role_known"})
}

func TestRoleR1_RoleMustBeKnown(t *testing.T) {
	s := newSociety(t)
	refused(t, one(`INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, '9002', 'Odd', 'asset', 'savingz')`, s.id),
		"an account with a role nobody defined", w("23514", "account_role_known"))
}

func TestRoleR2_SavingsLineNamesAMember(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.bank, kobo: 10000}, line{acct: c.savings, kobo: -10000}),
		"a member-savings line that names no member — whose savings?", w("23514", "member_line_names_member"))
}

func TestRoleR2_LoanLineNamesAMember(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.loan, kobo: 10000}, line{acct: c.bank, kobo: -10000}),
		"a member-loan line that names no member", w("23514", "member_line_names_member"))
}

func TestRoleR3_MemberLineOnlyOnMemberAccounts(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.bank, kobo: 10000, member: c.member}, line{acct: c.savings, kobo: -10000, member: c.member}),
		"a member's line posted to the bank account", w("23514", "member_line_on_member_account"))
}

func TestRoleR4_PartnerChargeOnlyOnPartnerAccount(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.bank, kobo: 10000}, line{acct: c.savings, kobo: -9000, member: c.member},
		line{acct: c.margin, kobo: -1000, role: "partner_charge"}),
		"a partner charge posted to the Argon-margin account (the D-03 split, silently undone)", w("23514", "charge_role_matches_account"))
}

func TestRoleR4_MarginOnlyOnMarginAccount(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.bank, kobo: 10000}, line{acct: c.savings, kobo: -9000, member: c.member},
		line{acct: c.bank, kobo: -1000, role: "argon_margin"}),
		"an Argon-margin line on the bank account", w("23514", "charge_role_matches_account"))
}

// The reverse direction: a line on a charges account must say which charge.
func TestRoleR4_ChargeAccountLineIsACharge(t *testing.T) {
	c := newChart(t)
	refused(t, c.post(line{acct: c.bank, kobo: 10000}, line{acct: c.savings, kobo: -9000, member: c.member},
		line{acct: c.partner, kobo: -1000, role: "principal"}),
		"a 'principal' line on the partner-charges account", w("23514", "charge_role_matches_account"))
}

// Relabel the bank as savings, and every statement and report reads history
// differently without a single line changing.
func TestRoleR5_RoleIsFinalOnceUsed(t *testing.T) {
	c := newChart(t)
	fixture(t, c.post(line{acct: c.bank, kobo: 10000}, line{acct: c.savings, kobo: -10000, member: c.member}), "a contribution")
	refused(t, one(`UPDATE accounts SET role = 'member_wallet' WHERE id = $1`, c.savings),
		"relabelling a savings account that already carries lines", w("23514", "account_role_is_final"))
}

func TestRole_Control_ContributionWithEveryRoleRight(t *testing.T) {
	c := newChart(t)
	c.chargesLike7a(t) // 0011: the charge is the schedule's, so the society has one
	accepted(t, c.post(
		line{acct: c.bank, kobo: 2500000},
		line{acct: c.savings, kobo: -2491250, member: c.member},
		line{acct: c.partner, kobo: -5000, role: "partner_charge"},
		line{acct: c.margin, kobo: -3750, role: "argon_margin"}),
		"§7a: bank, the member's savings, partner charge and margin, each on its own account")
}

func TestRole_Control_UnusedAccountMayBeRelabelled(t *testing.T) {
	c := newChart(t)
	var fresh string
	fixture(t, db.QueryRow(bg(), `INSERT INTO accounts (society_id, code, name, kind, role) VALUES ($1, $2, 'Spare', 'liability', 'member_wallet') RETURNING id`,
		c.s.id, fmt.Sprintf("29%02d", seq.Add(1)%100)).Scan(&fresh), "unused account")
	accepted(t, one(`UPDATE accounts SET role = 'member_savings' WHERE id = $1`, fresh), "correcting the role of an account no line has used")
}

