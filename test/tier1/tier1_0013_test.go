//go:build tier1

// Suite 0013 — VAT IS CHARGED ON ARGON'S OWN FEE. Written before the
// migration, for review.
//
// Decided 2026-10-13 (LEDGER_CONTRACT.md §9, §6): the illustrative ₦10 / ₦50
// transfer fee is ARGON'S, not the bank's. The bank's VAT is part of whatever
// the bank charges Argon: it is inside the partner component, not a line we
// compute. We charge our own fee, so we account for VAT on our own fee. Which
// is also why Argon may be supplying a VATable service in its own right: a
// question for Legal, alongside the fee, not arithmetic.
//
// And the names say so, because a name that says VAT on the bank's fee when it
// is VAT on Argon's fee is how the next person gets it wrong:
//
//	component partner_fee_vat → vat_on_argon_fee
//	line role partner_vat     → vat
//	columns   partner_vat_kobo → vat_kobo (the quote, the hold)
//
// The partner figures below are TEST DATA ONLY. VFD's charge to Argon is
// unknown; in reality the partner component stays refused until whoever holds
// the VFD relationship says what it is. Here it is set to ₦20 so that VAT on
// the partner's fee and VAT on Argon's fee give different answers.
package tier1

import "testing"

const srcPartnerTestData = "TEST DATA ONLY — NOT THE ANSWER: a partner figure set so the two VAT bases differ; VFD's charge to Argon is unknown"

// vatWorld: a chart charging withdrawals a ₦20 partner fee (test data), Argon's
// illustrative fee, VAT 7.5% (of Argon's fee), no levy.
func vatWorld(t *testing.T, argonFee []band) *chart {
	t.Helper()
	c := newChart(t)
	for _, s := range []seriesSpec{
		{"partner_fee", srcPartnerTestData, []band{{flat: 2000}}},
		{"vat_on_argon_fee", srcVAT, vatBands},
		{"statutory_levy", srcLevy, zeroBands},
		{"argon_margin", srcArgonFee, argonFee},
	} {
		_, err := chargeSeries(c.s.id, "withdrawal_out", s.comp, "2020-01-01", s.src, s.bands...)
		fixture(t, err, "the "+s.comp+" series")
	}
	return c
}

// 7.5% of Argon's ₦10 is ₦0.75; of the partner's ₦20 it would be ₦1.50.
func TestVAT01_VATIsChargedOnArgonsFeeNotThePartners(t *testing.T) {
	c := vatWorld(t, []band{{flat: 1000}})
	wantQuote(t, c.s.id, "withdrawal_out", payKobo, "2026-10-05", fees{fee: 2000, vat: 75, margin: 1000},
		"₦6,000 with a ₦20 partner fee and Argon's ₦10: VAT is 7.5% of Argon's ₦10")
}

// VAT follows Argon's fee band, not the partner's and not the amount: above
// ₦50,000 Argon's fee is ₦50, so VAT is ₦3.75.
func TestVAT02_VATFollowsArgonsFee(t *testing.T) {
	c := vatWorld(t, feeBands)
	wantQuote(t, c.s.id, "withdrawal_out", 6000000, "2026-10-05", fees{fee: 2000, vat: 375, margin: 5000},
		"₦60,000: Argon's ₦50, and VAT of 7.5% on it")
}

// The old name is gone, not kept beside the new one: a series named the way
// that meant "VAT on the bank's fee" is refused.
func TestVAT03_TheOldVATComponentIsRefused(t *testing.T) {
	c := newChart(t)
	_, err := chargeSeries(c.s.id, "withdrawal_out", "partner_fee_vat", "2020-01-01", srcVAT, vatBands...)
	refused(t, err, "a VAT series under the old name, partner_fee_vat", refusedAs("charge_schedules_component_check"))
}

// On an account no pairing guards, in a contribution charged nothing, so the
// only thing that can refuse the old role is the role list itself. (On the VAT
// account, that account's own rule would refuse it too and cover for this.)
func TestVAT04_AVATLineIsNamedVAT(t *testing.T) {
	w := newMoneyWorld(t)
	ls := []line{{acct: w.c.bank, kobo: 10000}, {acct: w.c.savings, kobo: -9925, member: w.bMem},
		{acct: w.c.migration, kobo: -75, role: "partner_vat"}}
	refused(t, w.post(t, move{kind: "contribution_in", lines: ls}),
		"a line under the old VAT role, partner_vat", refusedAs("ledger_lines_charge_role_check"))
}

// Remitted, not earned: a VAT line sits on the VAT account and nowhere else.
func TestVAT05_AVATLineBelongsOnTheVATAccount(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.misposted("vat", w.c.migration, "")}),
		"the right VAT, posted to the migration control account", refusedAs("charge_role_matches_account"))
}

// A hold's VAT is on Argon's fee too: ₦0.75 on Argon's ₦10, with a ₦20
// partner fee (test data) beside it.
func TestVAT06_AHoldsVATIsOnArgonsFee(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", "partner_fee", "2026-10-01", srcPartnerTestData, band{flat: 2000})
	fixture(t, err, "a ₦20 partner fee (test data)")
	req := w.approved(t, w.bMem, payKobo)
	h, err := w.hold(t, holdSpec{req: req, member: w.bMem, amount: payKobo})
	fixture(t, err, "the hold")
	var fee, vat, margin int64
	fixture(t, db.QueryRow(bg(), `SELECT partner_fee_kobo, vat_kobo, argon_margin_kobo FROM withdrawal_holds WHERE id = $1`, h).
		Scan(&fee, &vat, &margin), "the hold's charges")
	if fee != 2000 || margin != 1000 || vat != 75 {
		t.Fatalf("WRONG CHARGE — the hold carries fee %d, VAT %d, margin %d; want 2000, 75 (7.5%% of Argon's 1000), 1000", fee, vat, margin)
	}
}

// Where the two bases give the same answer (equal rates), a VAT line named vat
// on the VAT account is simply accepted.
func TestVAT_Control_AVATLineOnTheVATAccount(t *testing.T) {
	w := newMoneyWorld(t)
	for _, s := range []seriesSpec{
		{"partner_fee", srcPartnerTestData, []band{{bp: 20}}},
		{"vat_on_argon_fee", srcVAT, vatBands},
		{"statutory_levy", srcLevy, zeroBands},
		{"argon_margin", srcArgonFee, []band{{bp: 20}}},
	} {
		_, err := chargeSeries(w.c.s.id, "contribution_in", s.comp, "2020-01-01", s.src, s.bands...)
		fixture(t, err, "the contribution "+s.comp)
	}
	accepted(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, fees{fee: 5000, vat: 375, margin: 5000})}),
		"₦25,000 charged ₦50 and ₦50, with VAT of ₦3.75 on its own line, on the VAT account")
}
