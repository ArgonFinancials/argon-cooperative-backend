//go:build tier1

// Suite 0006 — A LINE POSTS TO AN ACCOUNT OF ITS ENTRY'S SOCIETY (§3).
// Written before the migration, for review.
//
// Found while specifying the trial balance (2026-10-05): 0002's
// ledger_line_admissible refuses a line naming a MEMBER of another society,
// but nothing checks the line's ACCOUNT. An entry of society S can post a line
// to society T's account. The entry still sums to zero, yet S's books lose a
// leg and T's gain one, and NEITHER society's trial balance sums to zero.
// "The trial balance sums to zero by construction" (§3) has this hole in it.
//
// The rule is per account, whatever the account is: a bank account, a charges
// account, a member's savings. The refusal is check_violation with the
// constraint ledger_line_account_in_society.
package tier1

import "testing"

var lineSociety = want{codes: []string{"23514"}, constraint: "ledger_line_account_in_society"}

func TestLineSociety1_LineCannotPostToAnotherSocietysAccount(t *testing.T) {
	s, other := newChart(t), newChart(t)
	refused(t, s.post(line{acct: other.bank, kobo: 10000}, line{acct: s.savings, kobo: -10000, member: s.member}),
		"society S's entry debiting society T's bank account", lineSociety)
}

func TestLineSociety2_ChargeLineCannotPostToAnotherSocietysChargesAccount(t *testing.T) {
	s, other := newChart(t), newChart(t)
	refused(t, s.post(line{acct: s.bank, kobo: 10000}, line{acct: s.savings, kobo: -9000, member: s.member},
		line{acct: other.partner, kobo: -1000, role: "partner_charge"}),
		"society S's entry crediting its partner charge to society T's charges account", lineSociety)
}

// The member is S's own, so 0002's member rule passes it; the ACCOUNT is T's.
func TestLineSociety3_MemberLineCannotPostToAnotherSocietysSavings(t *testing.T) {
	s, other := newChart(t), newChart(t)
	refused(t, s.post(line{acct: s.bank, kobo: 10000}, line{acct: other.savings, kobo: -10000, member: s.member}),
		"society S's entry crediting S's member on society T's savings account", lineSociety)
}

func TestLineSociety_Control_OwnAccountsAccepted(t *testing.T) {
	s := newChart(t)
	s.chargesContributions(t, 600, 400, "test data: 6% and 4%, the figures below") // 0011: the charge is the schedule's
	accepted(t, s.post(line{acct: s.bank, kobo: 10000}, line{acct: s.savings, kobo: -9000, member: s.member},
		line{acct: s.partner, kobo: -600, role: "partner_charge"}, line{acct: s.margin, kobo: -400, role: "argon_margin"}),
		"an entry whose every line is on its own society's accounts, charges included")
}
