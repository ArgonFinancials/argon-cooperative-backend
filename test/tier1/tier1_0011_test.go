//go:build tier1

// Suite 0011 — SINGLE CONTRIBUTIONS AND WITHDRAWALS. The first money movement
// outside an import. Written before the migration, for review (approved
// 2026-10-11, then revised to four charge components, for re-approval).
//
// Decided 2026-10-07 (LEDGER_CONTRACT.md §8), 2026-10-09 (§9), 2026-10-11 (§10):
//
//   - CHARGES ARE CONFIGURATION WITH NO DEFAULT, REFUSED UNTIL SET, and the
//     SERVER computes them; a client-computed charge is never accepted.
//   - FOUR COMPONENTS, each its own series from its own NAMED SOURCE (a
//     required field, not a comment), each set explicitly or refused, each on
//     its own ledger line and account, so nobody later has to ask what the
//     bank took, what was remitted as tax, and what Argon earned:
//     partner_fee      the bank's fee: bands on the amount
//     partner_fee_vat  VAT: a RATE ON THE FEE, not on the amount (written as
//     a figure per band it would be right today and
//     silently wrong the next time the fee changed)
//     statutory_levy   the transfer levy: its own bands on the amount, so its
//     threshold never cuts the fee's bands. REFUSED UNTIL
//     COMPLIANCE ANSWERS whether it falls on the sender at
//     all (in law it may be on electronic receipts)
//     argon_margin     REFUSED UNTIL LEGAL ANSWERS; never assumed zero
//   - Nothing is seeded: a default that becomes stale regulation is worse than
//     a refusal, because nobody reviews a value that is already there.
//   - CONTRIBUTIONS carry no charge when the society has configured none.
//   - THE FEE IS ON TOP for a withdrawal: the member receives what they asked
//     for; amount plus every charge must fit within what they may withdraw.
//   - WITHDRAWABLE (D-10) = savings − guarantees held − the account floor (a
//     society setting with no default). Every loan the member guarantees holds
//     back its FULL outstanding balance, counted per PERSON.
//   - EVERY WITHDRAWAL NEEDS ITS SIGNATORIES at the society's quorum (no
//     default); the request names its member; NOBODY SIGNS A REQUEST THEY
//     BENEFIT FROM (§10): the member a withdrawal pays never signs it.
//   - AN APPROVED WITHDRAWAL IS POSTED WHEN AN OFFICER RECORDS THE PAYOUT, on
//     their own PIN step-up, naming the transfer. A single contribution rests
//     on its poster's step-up too, as an import does.
//
// THE FIGURES IN THESE FIXTURES ARE ILLUSTRATIVE AND SOURCED-AND-UNVERIFIED:
// the applied transfer figures supplied 2026-10-11 (fee ₦0 under ₦5,000, ₦10 to
// ₦50,000, ₦50 above; VAT 7.5% of the fee; levy ₦50 from ₦10,000), pending
// Compliance confirming the edition and the bands, and the levy's incidence.
// Argon's margin is set to zero AS TEST DATA ONLY. The contribution figures are
// LEDGER_CONTRACT.md §7a's example. The tests depend on the SHAPE, not on these
// numbers.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ---- the world ---------------------------------------------------------------

// mworld: the loan world (a chart with charge accounts; a borrower with ₦10,000
// of savings; two guarantors; two signatories), with VAT and levy accounts, the
// society's withdrawal quorum at 2, its floor at ₦1,000, and the illustrative
// withdrawal schedule in all four components.
type mworld struct {
	*lworld
	poster    string // the officer who records payouts and contributions
	vat, levy string // the VAT-payable and levy-payable accounts
}

const (
	floorKobo = int64(100000) // ₦1,000
	// What the borrower may withdraw: 1,000,000 − 100,000 floor − 0 held.
	borrowerWithdrawable = loanSavings - floorKobo
	payKobo              = int64(600000) // ₦6,000: in the ₦10 fee band, under the levy
)

const (
	srcFee    = "ILLUSTRATIVE: applied transfer fees (CBN Guide to Charges, edition unconfirmed) — sourced-and-unverified"
	srcVAT    = "ILLUSTRATIVE: VAT at 7.5% on the fee — sourced-and-unverified"
	srcLevy   = "ILLUSTRATIVE: Electronic Money Transfer Levy, ₦50 from ₦10,000 — sourced-and-unverified; its incidence is with Compliance"
	srcMargin = "TEST DATA ONLY: margin zero for the test; Legal has not decided"
)

// The illustrative transfer series (see the header).
var (
	feeBands    = []band{{from: 0, to: 499999}, {from: 500000, to: 5000000, flat: 1000}, {from: 5000001, flat: 5000}}
	vatBands    = []band{{from: 0, bp: 750}}
	levyBands   = []band{{from: 0, to: 999999}, {from: 1000000, flat: 5000}}
	marginBands = []band{{from: 0}}
)

// charges: one movement's four components.
type fees struct{ fee, vat, levy, margin int64 }

func (c fees) total() int64 { return c.fee + c.vat + c.levy + c.margin }

// transferCharges: what the illustrative series charge `amount`.
func transferCharges(amount int64) fees {
	var c fees
	switch {
	case amount >= 5000001:
		c.fee = 5000
	case amount >= 500000:
		c.fee = 1000
	}
	c.vat = (c.fee*750 + 5000) / 10000
	if amount >= 1000000 {
		c.levy = 5000
	}
	return c
}

func newMoneyWorld(t *testing.T) *mworld {
	t.Helper()
	w := &mworld{lworld: newLoanWorld(t)}
	w.poster = w.s1
	w.vat, w.levy = w.chargeAccounts(t)
	w.floor(t, floorKobo)
	w.quorum(t, 2)
	for _, s := range []struct {
		comp, src string
		bands     []band
	}{{"partner_fee", srcFee, feeBands}, {"partner_fee_vat", srcVAT, vatBands}, {"statutory_levy", srcLevy, levyBands}, {"argon_margin", srcMargin, marginBands}} {
		_, err := chargeSeries(w.c.s.id, "withdrawal_out", s.comp, "2020-01-01", s.src, s.bands...)
		fixture(t, err, "the withdrawal "+s.comp+" series")
	}
	return w
}

// chargeAccounts: the VAT-payable and levy-payable accounts (0011's roles).
func (w *mworld) chargeAccounts(t *testing.T) (vat, levy string) {
	t.Helper()
	fixture(t, db.QueryRow(bg(), accountInsert("2102", "VAT payable on charges", "liability", "charges_vat"), w.c.s.id).Scan(&vat), "VAT account")
	fixture(t, db.QueryRow(bg(), accountInsert("2103", "Transfer levy payable", "liability", "statutory_levy_payable"), w.c.s.id).Scan(&levy), "levy account")
	return
}

func (w *mworld) floor(t *testing.T, kobo int64) {
	t.Helper()
	fixture(t, one(`UPDATE societies SET account_floor_kobo = $2 WHERE id = $1`, w.c.s.id, kobo), "the account floor")
}

func (w *mworld) quorum(t *testing.T, n int) {
	t.Helper()
	fixture(t, one(`UPDATE societies SET withdrawal_signatures_required = $2 WHERE id = $1`, w.c.s.id, n), "the withdrawal quorum")
}

// band: from/to (to 0: no upper bound), a flat fee, a rate in basis points,
// and a cap on the rate part (0: none).
type band struct{ from, to, flat, bp, cap int64 }

func zeroNull(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// chargeSeries writes one component's schedule and its bands in one
// transaction, as an operator does (as the owner: the app only reads it).
func chargeSeries(society, kind, component, from, source string, bands ...band) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		if err := x.QueryRow(bg(), `INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, society, kind, component, from, nz(source)).Scan(&id); err != nil {
			return err
		}
		for _, b := range bands {
			if _, err := x.Exec(bg(), `INSERT INTO charge_bands (schedule_id, from_kobo, to_kobo, flat_kobo, bp, cap_kobo)
				VALUES ($1, $2, $3, $4, $5, $6)`, id, b.from, zeroNull(b.to), b.flat, b.bp, zeroNull(b.cap)); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

// series: every component but `skip` configured for `kind` in `society`, from
// the illustrative transfer series.
func allSeriesBut(t *testing.T, society, kind, skip string) {
	t.Helper()
	for _, s := range []struct {
		comp, src string
		bands     []band
	}{{"partner_fee", srcFee, feeBands}, {"partner_fee_vat", srcVAT, vatBands}, {"statutory_levy", srcLevy, levyBands}, {"argon_margin", srcMargin, marginBands}} {
		if s.comp == skip {
			continue
		}
		_, err := chargeSeries(society, kind, s.comp, "2020-01-01", s.src, s.bands...)
		fixture(t, err, "the "+s.comp+" series")
	}
}

// chargesLike7a: the society charges contributions §7a's split (0.20% fee,
// 0.15% margin) with VAT and levy explicitly zero — the figures the read-side
// fixtures post by hand. Before 0011 (no charge schedules) it does nothing.
func (c *chart) chargesLike7a(t *testing.T) {
	t.Helper()
	c.chargesContributions(t, 20, 15, "LEDGER_CONTRACT.md §7a example (test data)")
}

// chargesContributions: contributions charged a partnerBP fee and a marginBP
// margin; VAT and levy set to zero (test data). A no-op before 0011.
func (c *chart) chargesContributions(t *testing.T, partnerBP, marginBP int64, source string) {
	t.Helper()
	var has bool
	fixture(t, db.QueryRow(bg(), `SELECT to_regclass('public.charge_schedules') IS NOT NULL`).Scan(&has), "is there a charge schedule table")
	if !has {
		return
	}
	for comp, b := range map[string]band{"partner_fee": {bp: partnerBP}, "partner_fee_vat": {}, "statutory_levy": {}, "argon_margin": {bp: marginBP}} {
		_, err := chargeSeries(c.s.id, "contribution_in", comp, "2020-01-01", source, b)
		fixture(t, err, "the society's contribution "+comp)
	}
}

// quote asks the database for a charge.
func quote(society, kind string, amount int64, on string) (c fees, err error) {
	err = db.QueryRow(bg(), `SELECT partner_fee_kobo, partner_vat_kobo, statutory_levy_kobo, argon_margin_kobo
		FROM charge_quote($1, $2, $3, $4)`, society, kind, amount, on).Scan(&c.fee, &c.vat, &c.levy, &c.margin)
	return
}

func wantQuote(t *testing.T, society, kind string, amount int64, on string, want fees, what string) {
	t.Helper()
	got, err := quote(society, kind, amount, on)
	if err != nil {
		t.Fatalf("REFUSED — %s: %v", what, err)
	}
	if got != want {
		t.Fatalf("WRONG CHARGE — %s: got %+v; want %+v", what, got, want)
	}
}

// ---- movements -----------------------------------------------------------------

// move: one single movement (no batch), posted by `actor` (default the
// poster). act: "" the right act, taken in this transaction; "none" no act;
// any other act kind, an act of that kind; actID, an act from elsewhere.
type move struct {
	kind, actor, act, actID string
	actBy                   string // the act's owner, if not the actor
	req, ref, valueDate     string
	lines                   []line
}

func (w *mworld) post(t *testing.T, mv move) error {
	t.Helper()
	if mv.actor == "" {
		mv.actor = w.poster
	}
	if mv.valueDate == "" {
		mv.valueDate = "2026-10-05"
	}
	actKind := map[string]string{"contribution_in": "contribution_record", "withdrawal_out": "withdrawal_payout"}[mv.kind]
	if mv.act != "" && mv.act != "none" {
		actKind = mv.act
	}
	actBy := mv.actor
	if mv.actBy != "" {
		actBy = mv.actBy
	}
	var proof string
	if mv.act != "none" && mv.actID == "" {
		proof = stepUp(t, actBy, actKind)
	}
	return tx(func(x pgx.Tx) error {
		act := mv.actID
		if mv.act != "none" && act == "" {
			if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
				actKind, actBy, proof).Scan(&act); err != nil {
				return fmt.Errorf("FIXTURE: the step-up act: %w", err)
			}
		}
		var id string
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post,
			  actor_post_label, act_id, request_id, payout_reference)
			VALUES ($1, $2, $3, $4, 'treasurer', 'Treasurer', $5, $6, $7) RETURNING id`,
			w.c.s.id, mv.kind, mv.valueDate, mv.actor, nz(act), nz(mv.req), nz(mv.ref)).Scan(&id); err != nil {
			return err
		}
		return w.c.s.insertLines(x, id, mv.lines...)
	})
}

// contribution: gross in at the bank; the member credited gross less every
// charge; each component on its own line. A zero line is not written (§3.4).
func (w *mworld) contribution(member string, gross int64, ch fees) []line {
	ls := []line{{acct: w.c.bank, kobo: gross}, {acct: w.c.savings, kobo: -(gross - ch.total()), member: member}}
	return append(ls, w.chargeLines(ch)...)
}

// payout: the charges ON TOP. The member's savings fall by amount + charges;
// the bank pays out exactly the amount asked.
func (w *mworld) payout(member string, amount int64, ch fees) []line {
	ls := []line{{acct: w.c.savings, kobo: amount + ch.total(), member: member}, {acct: w.c.bank, kobo: -amount}}
	return append(ls, w.chargeLines(ch)...)
}

func (w *mworld) chargeLines(ch fees) []line {
	var ls []line
	for _, c := range []struct {
		kobo       int64
		acct, role string
	}{{ch.fee, w.c.partner, "partner_charge"}, {ch.vat, w.vat, "partner_vat"}, {ch.levy, w.levy, "statutory_levy"}, {ch.margin, w.c.margin, "argon_margin"}} {
		if c.kobo != 0 {
			ls = append(ls, line{acct: c.acct, kobo: -c.kobo, role: c.role})
		}
	}
	return ls
}

// raise: a withdrawal request for `member`, at `required` signatures.
func (w *mworld) raise(member string, amount int64, required int) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at)
		VALUES ($1, $2, $3, 'withdrawal_out', $4, $5, now() + interval '7 days') RETURNING id`,
		w.c.s.id, fmt.Sprintf("WDR-%d", seq.Add(1)), nz(member), amount, required).Scan(&id)
	return id, err
}

// approved: a withdrawal request raised at the society's quorum and signed by
// both signatories.
func (w *mworld) approved(t *testing.T, member string, amount int64) string {
	t.Helper()
	req, err := w.raise(member, amount, 2)
	fixture(t, err, "the withdrawal request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "first signature")
	fixture(t, w.c.s.sign(req, w.s2Mem, "chairman"), "second signature")
	return req
}

// pay: the officer records the payout of an approved request, charged by the
// illustrative series.
func (w *mworld) pay(t *testing.T, req, member string, amount int64) error {
	t.Helper()
	return w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-" + digest()[:10], lines: w.payout(member, amount, transferCharges(amount))})
}

// saved: a free contribution of `kobo` to `member`'s savings.
func (w *mworld) saved(t *testing.T, member string, kobo int64) {
	t.Helper()
	fixture(t, w.post(t, move{kind: "contribution_in", valueDate: "2026-09-01", lines: w.contribution(member, kobo, fees{})}), "savings")
}

// =============================================================================
// THE CONFIGURATION
// =============================================================================

func TestMoney01_ASeriesNamesItsSource(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := chargeSeries(w.c.s.id, "transfer_out", "partner_fee", "2026-01-01", "  ", band{flat: 1000})
	refused(t, err, "a fee series that does not say which source its figures were taken from", refusedAs("charge_schedule_names_source"))
}

// A correction is a new series, effective from a date; the figures a charge was
// computed from are never rewritten under it.
func TestMoney02_TheConfigurationIsNeverEdited(t *testing.T) {
	w := newMoneyWorld(t)
	sched, err := chargeSeries(w.c.s.id, "transfer_out", "partner_fee", "2026-01-01", srcFee, band{flat: 1000})
	fixture(t, err, "a series")
	refused(t, one(`UPDATE charge_bands SET flat_kobo = 1 WHERE schedule_id = $1`, sched),
		"a band's fee rewritten in place", refusedAs("charge_configuration_immutable"))
}

func TestMoney03_BandsDoNotOverlap(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := chargeSeries(w.c.s.id, "transfer_out", "partner_fee", "2026-01-01", srcFee,
		band{from: 0, to: 500000, flat: 1000}, band{from: 500000, to: 900000, flat: 2500})
	refused(t, err, "two bands both covering ₦5,000", refusedAs("charge_bands_do_not_overlap"))
}

// A band is decided or not: a flat fee with no rate is neither.
func TestMoney04_ABandIsSetWhollyOrNotAtAll(t *testing.T) {
	w := newMoneyWorld(t)
	var sched string
	fixture(t, db.QueryRow(bg(), `INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
		VALUES ($1, 'transfer_out', 'partner_fee', '2026-01-01', $2) RETURNING id`, w.c.s.id, srcFee).Scan(&sched), "a series")
	refused(t, one(`INSERT INTO charge_bands (schedule_id, from_kobo, flat_kobo, bp) VALUES ($1, 0, 1000, NULL)`, sched),
		"a band with a flat fee and no rate decided", refusedAs("charge_band_whole"))
}

// =============================================================================
// THE CHARGE THE SERVER COMPUTES
// =============================================================================

func TestMoney05_AWithdrawalWithNoScheduleIsRefused(t *testing.T) {
	c := newChart(t)
	_, err := quote(c.s.id, "withdrawal_out", payKobo, "2026-10-05")
	refused(t, err, "a charge asked for a withdrawal in a society that has configured none", refusedAs("charge_partner_fee_unset"))
}

func TestMoney06_AnAmountNoBandCoversIsRefused(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "partner_fee")
	_, err := chargeSeries(c.s.id, "withdrawal_out", "partner_fee", "2020-01-01", srcFee, band{from: 0, to: 500000, flat: 1000})
	fixture(t, err, "a fee series covering only to ₦5,000")
	_, err = quote(c.s.id, "withdrawal_out", payKobo, "2026-10-05")
	refused(t, err, "a charge for ₦6,000, which no fee band covers", refusedAs("charge_band_missing"))
}

func TestMoney07_AnUnsetVATRefuses(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "partner_fee_vat")
	_, err := quote(c.s.id, "withdrawal_out", payKobo, "2026-10-05")
	refused(t, err, "a charge computed with no VAT decided", refusedAs("charge_vat_unset"))
}

// Whether the levy falls on the sender at all is with Compliance. Undecided is
// never "it applies", and never zero.
func TestMoney08_AnUnsetLevyRefusesUntilComplianceAnswers(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "statutory_levy")
	_, err := quote(c.s.id, "withdrawal_out", payKobo, "2026-10-05")
	refused(t, err, "a charge computed as if an undecided levy were settled", refusedAs("charge_levy_unset"))
}

// Argon's margin waits on Legal. Undecided is never zero.
func TestMoney09_AnUnsetMarginRefusesUntilLegalAnswers(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "argon_margin")
	_, err := quote(c.s.id, "withdrawal_out", payKobo, "2026-10-05")
	refused(t, err, "a charge computed as if an undecided margin were zero", refusedAs("charge_margin_unset"))
}

func TestMoney10_TheBandsFlatFee(t *testing.T) {
	w := newMoneyWorld(t)
	wantQuote(t, w.c.s.id, "withdrawal_out", payKobo, "2026-10-05", fees{fee: 1000, vat: 75},
		"a ₦6,000 withdrawal: ₦10 fee, ₦0.75 VAT, under the levy")
}

// §7a's rate, half up to the kobo: 0.20% of ₦1,428.57 is 285.714 kobo → 286
// (rounding down would give 285).
func TestMoney11_TheRateRoundsHalfUp(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "partner_fee")
	_, err := chargeSeries(c.s.id, "withdrawal_out", "partner_fee", "2020-01-01", srcFee, band{bp: 20})
	fixture(t, err, "a rate fee")
	wantQuote(t, c.s.id, "withdrawal_out", 142857, "2026-10-05", fees{fee: 286, vat: 21},
		"0.20% of ₦1,428.57, and 7.5% of that fee (21.45 kobo → 21)")
}

// The cap limits the RATE part: 0.35% of ₦100,000 is ₦350, capped at ₦200.
func TestMoney12_TheCapLimitsTheRate(t *testing.T) {
	c := newChart(t)
	allSeriesBut(t, c.s.id, "withdrawal_out", "partner_fee")
	_, err := chargeSeries(c.s.id, "withdrawal_out", "partner_fee", "2020-01-01", srcFee, band{flat: 1000, bp: 35, cap: 20000})
	fixture(t, err, "a flat fee plus a capped rate")
	wantQuote(t, c.s.id, "withdrawal_out", 10000000, "2026-10-05", fees{fee: 21000, vat: 1575, levy: 5000},
		"₦10 + 0.35% of ₦100,000 capped at ₦200; 7.5% of that; the levy")
}

// A movement is charged by the series in force on its value date, not by the
// newest one.
func TestMoney13_TheScheduleInForceOnTheValueDate(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := chargeSeries(w.c.s.id, "withdrawal_out", "partner_fee", "2026-10-01", srcFee, band{flat: 9999})
	fixture(t, err, "a later fee series")
	wantQuote(t, w.c.s.id, "withdrawal_out", payKobo, "2026-09-30", fees{fee: 1000, vat: 75},
		"a withdrawal value-dated the day before the new fee")
}

// VAT is what it is: a rate on the fee. On the amount it would be ₦450.
func TestMoney14_VATIsARateOnTheFeeNotTheAmount(t *testing.T) {
	w := newMoneyWorld(t)
	wantQuote(t, w.c.s.id, "withdrawal_out", payKobo, "2026-10-05", fees{fee: 1000, vat: 75},
		"VAT on a ₦6,000 withdrawal's ₦10 fee")
}

// The levy has its own threshold: ₦20,000 is in the same fee band as ₦6,000,
// and over the levy's.
func TestMoney15_TheLevyHasItsOwnBands(t *testing.T) {
	w := newMoneyWorld(t)
	wantQuote(t, w.c.s.id, "withdrawal_out", 2000000, "2026-10-05", fees{fee: 1000, vat: 75, levy: 5000},
		"a ₦20,000 withdrawal: the same ₦10 fee as ₦6,000, and the ₦50 levy")
}

func TestMoney_Control_AnUnconfiguredContributionIsFree(t *testing.T) {
	c := newChart(t)
	wantQuote(t, c.s.id, "contribution_in", 2500000, "2026-10-05", fees{}, "a contribution in a society that charges none")
}

// =============================================================================
// AN ENTRY CARRIES THE CHARGE THE SERVER COMPUTED, NOT ONE IT WAS GIVEN
// =============================================================================

// contributions: §7a's 0.20% fee and 0.15% margin, VAT 7.5% of the fee, and a
// ₦50 levy from ₦10,000 (all test data). ₦25,000 → ₦50, ₦3.75, ₦50, ₦37.50.
var charged25k = fees{fee: 5000, vat: 375, levy: 5000, margin: 3750}

func (w *mworld) chargedContributions(t *testing.T) {
	t.Helper()
	for _, s := range []struct {
		comp  string
		bands []band
	}{{"partner_fee", []band{{bp: 20}}}, {"partner_fee_vat", vatBands}, {"statutory_levy", levyBands}, {"argon_margin", []band{{bp: 15}}}} {
		_, err := chargeSeries(w.c.s.id, "contribution_in", s.comp, "2020-01-01", "test data (§7a's split; the illustrative VAT and levy)", s.bands...)
		fixture(t, err, "the contribution "+s.comp)
	}
}

func with(c fees, f func(*fees)) fees { f(&c); return c }

func TestMoney16_AContributionCarriesTheSchedulesFee(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, with(charged25k, func(c *fees) { c.fee = 4000 }))}),
		"a ₦25,000 contribution charged a ₦40 fee where the schedule says ₦50", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney17_AFreeContributionCarriesNoCharge(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, fees{fee: 1000})}),
		"a ₦10 charge on a contribution the society charges nothing for", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney18_TheVATLineIsTheSchedules(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, with(charged25k, func(c *fees) { c.vat = 400 }))}),
		"VAT of ₦4 where 7.5% of the ₦50 fee is ₦3.75", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney19_TheLevyLineIsTheSchedules(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, with(charged25k, func(c *fees) { c.levy = 0 }))}),
		"no levy on ₦25,000 where the series says ₦50", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney20_TheMarginLineIsTheSchedules(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, with(charged25k, func(c *fees) { c.margin = 9000 }))}),
		"Argon's margin ₦90 where the schedule says ₦37.50", refusedAs("entry_charge_is_the_schedules"))
}

// Remitted, not earned: VAT and the levy each on their own payable account.
// Each test posts the schedule's exact charges with ONE line on the wrong
// account, so nothing but the account rule can refuse it.
func (w *mworld) misposted(role, acct string) []line {
	ls := w.contribution(w.bMem, 2500000, charged25k)
	for i := range ls {
		if ls[i].role == role {
			ls[i].acct = acct
		}
	}
	return ls
}

func TestMoney21_AVATLineIsOnTheVATAccount(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.misposted("partner_vat", w.c.migration)}),
		"the right VAT, posted to the migration control account", refusedAs("charge_role_matches_account"))
}

func TestMoney22_ALevyLineIsOnTheLevyAccount(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	// Not the VAT account: that account's own rule (it carries only VAT) would
	// refuse a levy line too, covering for the rule under test.
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.misposted("statutory_levy", w.c.migration)}),
		"the right levy, posted to the migration control account", refusedAs("charge_role_matches_account"))
}

func TestMoney_Control_AContributionWithTheSchedulesCharge(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	accepted(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, charged25k)}),
		"a ₦25,000 contribution charged exactly ₦50, ₦3.75, ₦50 and ₦37.50, each on its own line")
}

// =============================================================================
// A SINGLE MOVEMENT RESTS ON ITS POSTER'S OWN STEP-UP
// =============================================================================

func TestMoney23_ASingleContributionRestsOnAStepUp(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", act: "none", lines: w.contribution(w.bMem, 500000, fees{})}),
		"a contribution recorded with no step-up behind it", refusedAs("entry_needs_step_up"))
}

func TestMoney24_TheActIsThePostersOwn(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", actBy: w.s2, lines: w.contribution(w.bMem, 500000, fees{})}),
		"the treasurer's contribution resting on the chairman's step-up", refusedAs("entry_act_is_actors"))
}

func TestMoney25_TheActIsForThisMovement(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", act: "withdrawal_payout", lines: w.contribution(w.bMem, 500000, fees{})}),
		"a contribution resting on a step-up taken to record a payout", refusedAs("entry_act_kind"))
}

func TestMoney26_TheActIsFromThisTransaction(t *testing.T) {
	w := newMoneyWorld(t)
	act := anAct(t, w.poster, "contribution_record")
	refused(t, w.post(t, move{kind: "contribution_in", actID: act, lines: w.contribution(w.bMem, 500000, fees{})}),
		"a contribution resting on an act committed earlier", refusedAs("entry_act_same_transaction"))
}

func TestMoney27_OneStepUpPostsOnce(t *testing.T) {
	w := newMoneyWorld(t)
	proof := stepUp(t, w.poster, "contribution_record")
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('contribution_record', $1, $2) RETURNING id`,
			w.poster, proof).Scan(&act); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			var id string
			if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id)
				VALUES ($1, 'contribution_in', '2026-10-05', $2, 'treasurer', 'Treasurer', $3) RETURNING id`, w.c.s.id, w.poster, act).Scan(&id); err != nil {
				return err
			}
			if err := w.c.s.insertLines(x, id, w.contribution(w.bMem, 500000, fees{})...); err != nil {
				return err
			}
		}
		return nil
	})
	refused(t, err, "one step-up recording two contributions", uniqueOrCheck("entry_act_once"))
}

// A bare chart, not the money world: that world's own savings are posted on
// acts, so an over-strict rule about acts would break it before the control
// could say anything.
func TestMoney_Control_AContributionOnThePostersOwnStepUp(t *testing.T) {
	w := &mworld{lworld: &lworld{c: newChart(t)}}
	w.poster = w.c.s.user("Treasurer")
	member := w.c.s.member(w.c.s.user("Member"))
	accepted(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(member, 500000, fees{})}),
		"the treasurer records a contribution on their own step-up, in one transaction")
}

// =============================================================================
// A WITHDRAWAL IS POSTED WHEN ITS PAYOUT IS RECORDED
// =============================================================================

var payCharges = transferCharges(payKobo) // ₦10 + ₦0.75

func TestMoney28_APayoutNamesItsRequest(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "withdrawal_out", ref: "NIP-1", lines: w.payout(w.bMem, payKobo, payCharges)}),
		"money paid out of a member's savings with no request behind it", refusedAs("withdrawal_names_request"))
}

func TestMoney29_TheRequestIsApproved(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "a request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "one of two signatures")
	refused(t, w.pay(t, req, w.bMem, payKobo), "a payout on a request with one signature of the two", refusedAs("withdrawal_request_approved"))
}

func TestMoney30_APayoutNamesItsTransfer(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "  ", lines: w.payout(w.bMem, payKobo, payCharges)}),
		"a payout recorded without the transfer that paid it", refusedAs("withdrawal_names_payout_reference"))
}

// Savings enough for two payouts, so a second one could only be refused for
// being the same request paid again, not for the money having run out.
func TestMoney31_ARequestIsPaidOnce(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.bMem, loanSavings)
	req := w.approved(t, w.bMem, payKobo)
	fixture(t, w.pay(t, req, w.bMem, payKobo), "the payout")
	refused(t, w.pay(t, req, w.bMem, payKobo), "the same approved request paid a second time", uniqueOrCheck("withdrawal_posted_once"))
}

func TestMoney32_ThePayoutIsTheAmountApproved(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	refused(t, w.pay(t, req, w.bMem, 700000), "₦7,000 paid on a request approved for ₦6,000", refusedAs("withdrawal_matches_request"))
}

func TestMoney33_ThePayoutComesFromTheRequestersSavings(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, loanSavings)
	req := w.approved(t, w.bMem, payKobo)
	refused(t, w.pay(t, req, w.g1Mem, payKobo), "the borrower's approved withdrawal paid out of a guarantor's savings",
		refusedAs("withdrawal_matches_request"))
}

// On top, not deducted: asked for ₦6,000, the member receives ₦6,000.
func TestMoney34_TheChargeIsOnTopNotDeducted(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	deducted := append([]line{{acct: w.c.savings, kobo: payKobo, member: w.bMem}, {acct: w.c.bank, kobo: -(payKobo - payCharges.total())}},
		w.chargeLines(payCharges)...)
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-1", lines: deducted}),
		"₦6,000 approved, ₦5,989.25 paid: the charges taken out of what the member asked for", refusedAs("withdrawal_matches_request"))
}

func TestMoney35_AWithdrawalCarriesItsCharge(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, payKobo)
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-1", lines: w.payout(w.bMem, payKobo, fees{})}),
		"a ₦6,000 withdrawal paid with no charge where the series say ₦10.75", refusedAs("entry_charge_is_the_schedules"))
}

// =============================================================================
// WHAT A MEMBER MAY WITHDRAW (D-10)
// =============================================================================

func TestMoney36_AnUnsetFloorRefuses(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "the floor undecided")
	req := w.approved(t, w.bMem, payKobo)
	refused(t, w.pay(t, req, w.bMem, payKobo), "a withdrawal in a society that has not set its account floor", refusedAs("account_floor_unset"))
}

func TestMoney37_TheFloorStaysBehind(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 950000)
	refused(t, w.pay(t, req, w.bMem, 950000), "₦9,500 + ₦10.75 out of ₦10,000 savings with a ₦1,000 floor",
		refusedAs("withdrawal_within_withdrawable"))
}

// ₦8,990 fits within the ₦9,000 withdrawable; ₦8,990 + ₦10.75 does not.
func TestMoney38_TheChargesCountAgainstWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 899000)
	refused(t, w.pay(t, req, w.bMem, 899000), "₦8,990 + ₦10.75 against ₦9,000 withdrawable", refusedAs("withdrawal_within_withdrawable"))
}

// guarantorWorld: a disbursed ₦24,000 loan both guarantors guarantee, and the
// first guarantor's ₦10,000 savings.
func guarantorWorld(t *testing.T) (*mworld, string) {
	t.Helper()
	w := newMoneyWorld(t)
	loan, _ := w.ready(t)
	_, err := w.disburse(loan, loanPrincipal)
	fixture(t, err, "the disbursement")
	w.saved(t, w.g1Mem, loanSavings)
	return w, loan
}

// The conservative default: the FULL outstanding ₦24,000 is held back while
// the guarantee stands, so nothing of the ₦9,000 is free.
func TestMoney39_AGuaranteeHoldsBackTheLoansOutstanding(t *testing.T) {
	w, _ := guarantorWorld(t)
	req := w.approved(t, w.g1Mem, payKobo)
	refused(t, w.pay(t, req, w.g1Mem, payKobo), "₦6,000 out of a guarantor's savings while ₦24,000 they guarantee is outstanding",
		refusedAs("withdrawal_within_withdrawable"))
}

// Per PERSON, not per member record: the guarantor's other record does not
// escape the hold.
func TestMoney40_AGuaranteeHoldsThroughThePersonsOtherRecord(t *testing.T) {
	w, _ := guarantorWorld(t)
	other := w.c.s.member(w.g1)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, other), "the guarantor's second record")
	w.saved(t, other, loanSavings)
	req := w.approved(t, other, payKobo)
	refused(t, w.pay(t, req, other, payKobo), "the guarantor withdrawing through their second member record",
		refusedAs("withdrawal_within_withdrawable"))
}

func TestMoney_Control_WithdrawingExactlyWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	amount := borrowerWithdrawable - transferCharges(borrowerWithdrawable).total() // ₦8,989.25 + ₦10.75 = ₦9,000
	if amount+transferCharges(amount).total() != borrowerWithdrawable {
		t.Fatalf("FIXTURE: the arithmetic: %d + %d is not %d", amount, transferCharges(amount).total(), borrowerWithdrawable)
	}
	req := w.approved(t, w.bMem, amount)
	accepted(t, w.pay(t, req, w.bMem, amount), "₦8,989.25 + ₦10.75, exactly the ₦9,000 the borrower may withdraw")
}

func TestMoney_Control_ARepaidLoanHoldsNothing(t *testing.T) {
	w, loan := guarantorWorld(t)
	_, err := w.repay(loan, loanPrincipal)
	fixture(t, err, "the loan repaid in full")
	req := w.approved(t, w.g1Mem, payKobo)
	accepted(t, w.pay(t, req, w.g1Mem, payKobo), "a guarantor withdrawing once the loan they guaranteed owes nothing")
}

// =============================================================================
// WHO AUTHORISES A WITHDRAWAL
// =============================================================================

func TestMoney41_AWithdrawalRequestNamesItsMember(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise("", payKobo, 2)
	refused(t, err, "a withdrawal request out of nobody's savings", refusedAs("withdrawal_request_names_member"))
}

func TestMoney42_AnUndecidedQuorumRefuses(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, one(`UPDATE societies SET withdrawal_signatures_required = NULL WHERE id = $1`, w.c.s.id), "the quorum undecided")
	_, err := w.raise(w.bMem, payKobo, 2)
	refused(t, err, "a withdrawal request in a society that has not decided how many signatures one needs",
		refusedAs("withdrawal_quorum_unset"))
}

// The guard threshold is interface friction; it never makes a withdrawal
// unilateral (§8).
func TestMoney43_TheQuorumIsTheSocietys(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise(w.bMem, payKobo, 1)
	refused(t, err, "a withdrawal request asking for one signature where the society decided two",
		refusedAs("withdrawal_quorum_is_societys"))
}

// §10: nobody signs a request they benefit from. Accepted 2026-10-11.
func TestMoney44_AMemberNeverSignsTheirOwnWithdrawal(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.s1Mem, loanSavings)
	req, err := w.raise(w.s1Mem, payKobo, 2)
	fixture(t, err, "the treasurer's own withdrawal request")
	refused(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the treasurer signing the withdrawal of their own savings",
		refusedAs("requester_never_signs_own_withdrawal"))
}

func TestMoney_Control_ARequestAtTheSocietysQuorum(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise(w.bMem, payKobo, 2)
	accepted(t, err, "a withdrawal request at the society's two signatures")
}
