//go:build tier1

// Suite 0011 — SINGLE CONTRIBUTIONS AND WITHDRAWALS. The first money movement
// outside an import. Written before the migration, for review.
//
// Decided 2026-10-07 (LEDGER_CONTRACT.md §8) and 2026-10-09:
//
//   - CHARGES ARE CONFIGURATION WITH NO DEFAULT, REFUSED UNTIL SET, and the
//     SERVER computes them; a client-computed charge is never accepted. The
//     configuration is BANDED: each band a flat fee and an optional rate and
//     cap, for the partner and for Argon's margin SEPARATELY. A schedule names
//     the EDITION of the regulation its figures were taken from (a required
//     field, not a comment). Nothing is seeded: a default that becomes stale
//     regulation is worse than a refusal, because nobody reviews a value that
//     is already there.
//   - ARGON'S MARGIN can be expressed and is refused until set. Whether there
//     may be one, and how much, is Legal's question; it is never assumed to be
//     zero or any figure.
//   - CONTRIBUTIONS carry no charge when the society has configured none; the
//     configuration can still charge them.
//   - THE FEE IS ON TOP for a withdrawal: the member receives what they asked
//     for, and amount plus fee must fit within what they may withdraw.
//   - WITHDRAWABLE (D-10) = savings − guarantees held − the account floor.
//     The floor is a society setting with no default; until it is set, a
//     withdrawal is refused and says why. Every loan the member guarantees
//     holds back its FULL outstanding balance (the conservative default,
//     pending the guarantor-liability decision), counted per PERSON.
//   - EVERY WITHDRAWAL NEEDS ITS SIGNATORIES: the society's quorum, which has
//     no default; the request names its member.
//   - AN APPROVED WITHDRAWAL IS POSTED WHEN AN OFFICER RECORDS THE PAYOUT, on
//     their own PIN step-up, naming the transfer that paid it. A single
//     contribution rests on its poster's step-up too, as an import does.
//
// Proposed here, for review (the class of finding 19): the member a withdrawal
// is for never signs it.
//
// THE FIGURES IN THESE FIXTURES ARE ILLUSTRATIVE. The withdrawal bands are
// shaped like the CBN Guide to Charges' 2020 electronic transfer tiers
// (SOURCED-AND-UNVERIFIED: not confirmed by the compliance owner, and a 2026
// revision is in draft). The contribution split is LEDGER_CONTRACT.md §7a's
// example. The tests depend on the SHAPE, not on these numbers.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ---- the world ---------------------------------------------------------------

// mworld: the loan world (a chart with charge accounts; a borrower with ₦10,000
// of savings; two guarantors; two signatories), with the society's withdrawal
// quorum at 2, its floor at ₦1,000, and the illustrative withdrawal schedule.
type mworld struct {
	*lworld
	poster string // the officer who records payouts and contributions
}

const (
	floorKobo = int64(100000) // ₦1,000
	// Withdrawable for the borrower: 1,000,000 − 100,000 floor − 0 held.
	borrowerWithdrawable = loanSavings - floorKobo
)

// The illustrative withdrawal bands (SOURCED-AND-UNVERIFIED, see the header):
// up to ₦5,000 → ₦10; to ₦50,000 → ₦25; above → ₦50; the partner's figure.
// Argon's margin is SET TO ZERO HERE AS TEST DATA, not as a product assumption.
var transferBands = []band{
	{from: 0, to: 500000, p: fee{flat: 1000}, m: fee{}},
	{from: 500001, to: 5000000, p: fee{flat: 2500}, m: fee{}},
	{from: 5000001, p: fee{flat: 5000}, m: fee{}},
}

func newMoneyWorld(t *testing.T) *mworld {
	t.Helper()
	w := &mworld{lworld: newLoanWorld(t)}
	w.poster = w.s1
	w.floor(t, floorKobo)
	w.quorum(t, 2)
	_, err := w.schedule("withdrawal_out", "2020-01-01", illustrativeEdition, transferBands...)
	fixture(t, err, "the withdrawal schedule")
	return w
}

const illustrativeEdition = "ILLUSTRATIVE TEST DATA shaped like the CBN Guide to Charges, 2020 edition (sourced-and-unverified)"

func (w *mworld) floor(t *testing.T, kobo int64) {
	t.Helper()
	fixture(t, one(`UPDATE societies SET account_floor_kobo = $2 WHERE id = $1`, w.c.s.id, kobo), "the account floor")
}

func (w *mworld) quorum(t *testing.T, n int) {
	t.Helper()
	fixture(t, one(`UPDATE societies SET withdrawal_signatures_required = $2 WHERE id = $1`, w.c.s.id, n), "the withdrawal quorum")
}

// fee: one component of a band. set=false (the zero value with unset: true)
// writes NULLs: the figure has not been decided.
type fee struct {
	flat, bp int64
	cap      int64 // 0: no cap
	unset    bool
}

type band struct {
	from, to int64 // to 0: no upper bound
	p, m     fee
}

// schedule writes a schedule and its bands in one transaction, as an operator
// does (as the owner: the app only reads it).
func (w *mworld) schedule(kind, from, edition string, bands ...band) (string, error) {
	return chargeSchedule(w.c.s.id, kind, from, edition, bands...)
}

func chargeSchedule(society, kind, from, edition string, bands ...band) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		if err := x.QueryRow(bg(), `INSERT INTO charge_schedules (society_id, kind, effective_from, edition)
			VALUES ($1, $2, $3, $4) RETURNING id`, society, kind, from, nz(edition)).Scan(&id); err != nil {
			return err
		}
		for _, b := range bands {
			if _, err := x.Exec(bg(), `INSERT INTO charge_bands (schedule_id, from_kobo, to_kobo,
				partner_flat_kobo, partner_bp, partner_cap_kobo, margin_flat_kobo, margin_bp, margin_cap_kobo)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, b.from, zeroNull(b.to),
				b.p.flatOrNull(), b.p.bpOrNull(), b.p.capOrNull(), b.m.flatOrNull(), b.m.bpOrNull(), b.m.capOrNull()); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func zeroNull(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func (f fee) flatOrNull() any {
	if f.unset {
		return nil
	}
	return f.flat
}
func (f fee) bpOrNull() any {
	if f.unset {
		return nil
	}
	return f.bp
}
func (f fee) capOrNull() any {
	if f.unset {
		return nil
	}
	return zeroNull(f.cap)
}

// chargesLike7a: the society charges contributions §7a's split (0.20% to the
// partner, 0.15% to Argon), the figures the read-side fixtures post by hand.
// Where the schema has no charge schedules (before 0011) it does nothing:
// those fixtures' charges were then the client's to give.
func (c *chart) chargesLike7a(t *testing.T) {
	t.Helper()
	c.chargesContributions(t, 20, 15, "LEDGER_CONTRACT.md §7a example (test data)")
}

// chargesContributions: the society charges contributions partnerBP and
// marginBP, no flat fee, no cap. A no-op before 0011.
func (c *chart) chargesContributions(t *testing.T, partnerBP, marginBP int64, edition string) {
	t.Helper()
	var has bool
	fixture(t, db.QueryRow(bg(), `SELECT to_regclass('public.charge_schedules') IS NOT NULL`).Scan(&has), "is there a charge schedule table")
	if !has {
		return
	}
	_, err := chargeSchedule(c.s.id, "contribution_in", "2020-01-01", edition, band{p: fee{bp: partnerBP}, m: fee{bp: marginBP}})
	fixture(t, err, "the society's contribution charges")
}

// quote asks the database for a charge.
func quote(society, kind string, amount int64, on string) (partner, margin int64, err error) {
	err = db.QueryRow(bg(), `SELECT partner_kobo, margin_kobo FROM charge_quote($1, $2, $3, $4)`, society, kind, amount, on).
		Scan(&partner, &margin)
	return
}

func wantQuote(t *testing.T, society, kind string, amount int64, on string, partner, margin int64, what string) {
	t.Helper()
	p, m, err := quote(society, kind, amount, on)
	if err != nil {
		t.Fatalf("REFUSED — %s: %v", what, err)
	}
	if p != partner || m != margin {
		t.Fatalf("WRONG CHARGE — %s: partner %d, margin %d; want %d and %d", what, p, m, partner, margin)
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

// contribution: gross in at the bank; the member is credited gross less the
// charges; each charge on its own line. A zero line is not written (§3.4).
func (w *mworld) contribution(member string, gross, partner, margin int64) []line {
	ls := []line{{acct: w.c.bank, kobo: gross}, {acct: w.c.savings, kobo: -(gross - partner - margin), member: member}}
	return append(ls, w.chargeLines(partner, margin)...)
}

// payout: the fee ON TOP. The member's savings fall by amount + fee; the bank
// pays out exactly the amount asked.
func (w *mworld) payout(member string, amount, partner, margin int64) []line {
	ls := []line{{acct: w.c.savings, kobo: amount + partner + margin, member: member}, {acct: w.c.bank, kobo: -amount}}
	return append(ls, w.chargeLines(partner, margin)...)
}

func (w *mworld) chargeLines(partner, margin int64) []line {
	var ls []line
	if partner != 0 {
		ls = append(ls, line{acct: w.c.partner, kobo: -partner, role: "partner_charge"})
	}
	if margin != 0 {
		ls = append(ls, line{acct: w.c.margin, kobo: -margin, role: "argon_margin"})
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
// illustrative bands.
func (w *mworld) pay(t *testing.T, req, member string, amount int64) error {
	t.Helper()
	return w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-" + digest()[:10], lines: w.payout(member, amount, transferFee(amount), 0)})
}

func transferFee(amount int64) int64 {
	for _, b := range transferBands {
		if amount >= b.from && (b.to == 0 || amount <= b.to) {
			return b.p.flat
		}
	}
	panic("no band")
}

// saved: a free contribution of `kobo` to `member`'s savings.
func (w *mworld) saved(t *testing.T, member string, kobo int64) {
	t.Helper()
	fixture(t, w.post(t, move{kind: "contribution_in", valueDate: "2026-09-01", lines: w.contribution(member, kobo, 0, 0)}), "savings")
}

// =============================================================================
// THE CONFIGURATION
// =============================================================================

func TestMoney01_AScheduleNamesItsEdition(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.schedule("transfer_out", "2026-01-01", "  ", band{p: fee{flat: 1000}, m: fee{}})
	refused(t, err, "a charge schedule that does not say which edition of the regulation its figures come from",
		refusedAs("charge_schedule_names_edition"))
}

// A correction is a new version, effective from a date; the figures a charge
// was computed from are never rewritten under it.
func TestMoney02_TheConfigurationIsNeverEdited(t *testing.T) {
	w := newMoneyWorld(t)
	sched, err := w.schedule("transfer_out", "2026-01-01", illustrativeEdition, band{p: fee{flat: 1000}, m: fee{}})
	fixture(t, err, "a schedule")
	refused(t, one(`UPDATE charge_bands SET partner_flat_kobo = 1 WHERE schedule_id = $1`, sched),
		"a band's fee rewritten in place", refusedAs("charge_configuration_immutable"))
}

func TestMoney03_BandsDoNotOverlap(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.schedule("transfer_out", "2026-01-01", illustrativeEdition,
		band{from: 0, to: 500000, p: fee{flat: 1000}, m: fee{}}, band{from: 500000, to: 900000, p: fee{flat: 2500}, m: fee{}})
	refused(t, err, "two bands both covering ₦5,000", refusedAs("charge_bands_do_not_overlap"))
}

// A component is decided or not: a flat fee with no rate is neither.
func TestMoney04_AChargeIsSetWhollyOrNotAtAll(t *testing.T) {
	w := newMoneyWorld(t)
	var sched string
	fixture(t, db.QueryRow(bg(), `INSERT INTO charge_schedules (society_id, kind, effective_from, edition)
		VALUES ($1, 'transfer_out', '2026-01-01', $2) RETURNING id`, w.c.s.id, illustrativeEdition).Scan(&sched), "a schedule")
	refused(t, one(`INSERT INTO charge_bands (schedule_id, from_kobo, partner_flat_kobo, partner_bp, margin_flat_kobo, margin_bp)
		VALUES ($1, 0, 1000, NULL, 0, 0)`, sched), "a partner fee with a flat figure and no rate decided", refusedAs("charge_component_whole"))
}

// =============================================================================
// THE CHARGE THE SERVER COMPUTES
// =============================================================================

func TestMoney05_AWithdrawalWithNoScheduleIsRefused(t *testing.T) {
	c := newChart(t)
	_, _, err := quote(c.s.id, "withdrawal_out", 300000, "2026-10-05")
	refused(t, err, "a charge asked for a withdrawal in a society that has configured none", refusedAs("charge_schedule_unset"))
}

func TestMoney06_AnAmountNoBandCoversIsRefused(t *testing.T) {
	c := newChart(t)
	_, err := chargeSchedule(c.s.id, "withdrawal_out", "2020-01-01", illustrativeEdition, band{from: 0, to: 500000, p: fee{flat: 1000}, m: fee{}})
	fixture(t, err, "a schedule covering only to ₦5,000")
	_, _, err = quote(c.s.id, "withdrawal_out", 600000, "2026-10-05")
	refused(t, err, "a charge for ₦6,000, which no band covers", refusedAs("charge_band_missing"))
}

func TestMoney07_AnUnsetPartnerChargeRefuses(t *testing.T) {
	c := newChart(t)
	_, err := chargeSchedule(c.s.id, "withdrawal_out", "2020-01-01", illustrativeEdition, band{p: fee{unset: true}, m: fee{}})
	fixture(t, err, "a band with no partner charge decided")
	_, _, err = quote(c.s.id, "withdrawal_out", 300000, "2026-10-05")
	refused(t, err, "a charge computed from an undecided partner fee", refusedAs("charge_partner_unset"))
}

// Argon's margin waits on Legal. Undecided is never zero.
func TestMoney08_AnUnsetMarginRefusesUntilLegalAnswers(t *testing.T) {
	c := newChart(t)
	_, err := chargeSchedule(c.s.id, "withdrawal_out", "2020-01-01", illustrativeEdition, band{p: fee{flat: 1000}, m: fee{unset: true}})
	fixture(t, err, "a band whose margin is not decided")
	_, _, err = quote(c.s.id, "withdrawal_out", 300000, "2026-10-05")
	refused(t, err, "a charge computed as if an undecided margin were zero", refusedAs("charge_margin_unset"))
}

func TestMoney09_TheBandsFlatFee(t *testing.T) {
	w := newMoneyWorld(t)
	wantQuote(t, w.c.s.id, "withdrawal_out", 2000000, "2026-10-05", 2500, 0, "a ₦20,000 withdrawal in the ₦5,001–₦50,000 band")
}

// §7a's rates, half up to the kobo: 0.20% of ₦1,428.57 is 285.714 kobo → 286
// (rounding down would give 285); 0.15% is 214.2855 → 214.
func TestMoney10_TheRateRoundsHalfUp(t *testing.T) {
	c := newChart(t)
	_, err := chargeSchedule(c.s.id, "withdrawal_out", "2020-01-01", illustrativeEdition, band{p: fee{bp: 20}, m: fee{bp: 15}})
	fixture(t, err, "a rate schedule")
	wantQuote(t, c.s.id, "withdrawal_out", 142857, "2026-10-05", 286, 214, "0.20% and 0.15% of ₦1,428.57 (285.714 and 214.2855 kobo)")
}

// The cap limits the RATE part: 0.35% of ₦100,000 is ₦350, capped at ₦200.
func TestMoney11_TheCapLimitsTheRate(t *testing.T) {
	c := newChart(t)
	_, err := chargeSchedule(c.s.id, "withdrawal_out", "2020-01-01", illustrativeEdition,
		band{p: fee{flat: 1000, bp: 35, cap: 20000}, m: fee{}})
	fixture(t, err, "a flat fee plus a capped rate")
	wantQuote(t, c.s.id, "withdrawal_out", 10000000, "2026-10-05", 21000, 0, "₦10 + 0.35% of ₦100,000 capped at ₦200")
}

// A movement is charged by the schedule in force on its value date, not by
// the newest one.
func TestMoney12_TheScheduleInForceOnTheValueDate(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.schedule("withdrawal_out", "2026-10-01", illustrativeEdition, band{p: fee{flat: 9999}, m: fee{}})
	fixture(t, err, "a later schedule")
	wantQuote(t, w.c.s.id, "withdrawal_out", 300000, "2026-09-30", 1000, 0, "a withdrawal value-dated the day before the new schedule")
}

func TestMoney_Control_AnUnconfiguredContributionIsFree(t *testing.T) {
	c := newChart(t)
	wantQuote(t, c.s.id, "contribution_in", 2500000, "2026-10-05", 0, 0, "a contribution in a society that charges none")
}

// =============================================================================
// AN ENTRY CARRIES THE CHARGE THE SERVER COMPUTED, NOT ONE IT WAS GIVEN
// =============================================================================

func (w *mworld) chargedContributions(t *testing.T) {
	t.Helper()
	_, err := w.schedule("contribution_in", "2020-01-01", "LEDGER_CONTRACT.md §7a example (test data)", band{p: fee{bp: 20}, m: fee{bp: 15}})
	fixture(t, err, "a contribution schedule (§7a's split)")
}

func TestMoney13_AContributionCarriesTheSchedulesPartnerCharge(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, 4000, 3750)}),
		"a ₦25,000 contribution charged ₦40 to the partner where the schedule says ₦50", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney14_AFreeContributionCarriesNoCharge(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, 1000, 0)}),
		"a ₦10 charge on a contribution the society charges nothing for", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney15_TheMarginLineIsTheSchedulesToo(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	refused(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, 5000, 9000)}),
		"the partner's charge right and Argon's margin ₦90 where the schedule says ₦37.50", refusedAs("entry_charge_is_the_schedules"))
}

func TestMoney_Control_AContributionWithTheSchedulesCharge(t *testing.T) {
	w := newMoneyWorld(t)
	w.chargedContributions(t)
	accepted(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(w.bMem, 2500000, 5000, 3750)}),
		"a ₦25,000 contribution charged exactly ₦50 and ₦37.50")
}

// =============================================================================
// A SINGLE MOVEMENT RESTS ON ITS POSTER'S OWN STEP-UP
// =============================================================================

func TestMoney16_ASingleContributionRestsOnAStepUp(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", act: "none", lines: w.contribution(w.bMem, 500000, 0, 0)}),
		"a contribution recorded with no step-up behind it", refusedAs("entry_needs_step_up"))
}

func TestMoney17_TheActIsThePostersOwn(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", actBy: w.s2, lines: w.contribution(w.bMem, 500000, 0, 0)}),
		"the treasurer's contribution resting on the chairman's step-up", refusedAs("entry_act_is_actors"))
}

func TestMoney18_TheActIsForThisMovement(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "contribution_in", act: "withdrawal_payout", lines: w.contribution(w.bMem, 500000, 0, 0)}),
		"a contribution resting on a step-up taken to record a payout", refusedAs("entry_act_kind"))
}

func TestMoney19_TheActIsFromThisTransaction(t *testing.T) {
	w := newMoneyWorld(t)
	act := anAct(t, w.poster, "contribution_record")
	refused(t, w.post(t, move{kind: "contribution_in", actID: act, lines: w.contribution(w.bMem, 500000, 0, 0)}),
		"a contribution resting on an act committed earlier", refusedAs("entry_act_same_transaction"))
}

func TestMoney20_OneStepUpPostsOnce(t *testing.T) {
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
			if err := w.c.s.insertLines(x, id, w.contribution(w.bMem, 500000, 0, 0)...); err != nil {
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
	accepted(t, w.post(t, move{kind: "contribution_in", lines: w.contribution(member, 500000, 0, 0)}),
		"the treasurer records a contribution on their own step-up, in one transaction")
}

// =============================================================================
// A WITHDRAWAL IS POSTED WHEN ITS PAYOUT IS RECORDED
// =============================================================================

func TestMoney21_APayoutNamesItsRequest(t *testing.T) {
	w := newMoneyWorld(t)
	refused(t, w.post(t, move{kind: "withdrawal_out", ref: "NIP-1", lines: w.payout(w.bMem, 300000, 1000, 0)}),
		"money paid out of a member's savings with no request behind it", refusedAs("withdrawal_names_request"))
}

func TestMoney22_TheRequestIsApproved(t *testing.T) {
	w := newMoneyWorld(t)
	req, err := w.raise(w.bMem, 300000, 2)
	fixture(t, err, "a request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "one of two signatures")
	refused(t, w.pay(t, req, w.bMem, 300000), "a payout on a request with one signature of the two", refusedAs("withdrawal_request_approved"))
}

func TestMoney23_APayoutNamesItsTransfer(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 300000)
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "  ", lines: w.payout(w.bMem, 300000, 1000, 0)}),
		"a payout recorded without the transfer that paid it", refusedAs("withdrawal_names_payout_reference"))
}

func TestMoney24_ARequestIsPaidOnce(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 300000)
	fixture(t, w.pay(t, req, w.bMem, 300000), "the payout")
	refused(t, w.pay(t, req, w.bMem, 300000), "the same approved request paid a second time", uniqueOrCheck("withdrawal_posted_once"))
}

func TestMoney25_ThePayoutIsTheAmountApproved(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 300000)
	refused(t, w.pay(t, req, w.bMem, 400000), "₦4,000 paid on a request approved for ₦3,000", refusedAs("withdrawal_matches_request"))
}

func TestMoney26_ThePayoutComesFromTheRequestersSavings(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.g1Mem, loanSavings)
	req := w.approved(t, w.bMem, 300000)
	refused(t, w.pay(t, req, w.g1Mem, 300000), "the borrower's approved withdrawal paid out of a guarantor's savings",
		refusedAs("withdrawal_matches_request"))
}

// On top, not deducted: asked for ₦3,000, the member receives ₦3,000.
func TestMoney27_TheFeeIsOnTopNotDeducted(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 300000)
	deducted := []line{{acct: w.c.savings, kobo: 300000, member: w.bMem}, {acct: w.c.bank, kobo: -299000},
		{acct: w.c.partner, kobo: -1000, role: "partner_charge"}}
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-1", lines: deducted}),
		"₦3,000 approved, ₦2,990 paid: the fee taken out of what the member asked for", refusedAs("withdrawal_matches_request"))
}

func TestMoney28_AWithdrawalCarriesItsCharge(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 300000)
	refused(t, w.post(t, move{kind: "withdrawal_out", req: req, ref: "NIP-1", lines: w.payout(w.bMem, 300000, 0, 0)}),
		"a ₦3,000 withdrawal paid with no charge where the schedule says ₦10", refusedAs("entry_charge_is_the_schedules"))
}

// =============================================================================
// WHAT A MEMBER MAY WITHDRAW (D-10)
// =============================================================================

func TestMoney29_AnUnsetFloorRefuses(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, one(`UPDATE societies SET account_floor_kobo = NULL WHERE id = $1`, w.c.s.id), "the floor undecided")
	req := w.approved(t, w.bMem, 300000)
	refused(t, w.pay(t, req, w.bMem, 300000), "a withdrawal in a society that has not set its account floor", refusedAs("account_floor_unset"))
}

func TestMoney30_TheFloorStaysBehind(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 950000)
	refused(t, w.pay(t, req, w.bMem, 950000), "₦9,500 + ₦25 out of ₦10,000 savings with a ₦1,000 floor",
		refusedAs("withdrawal_within_withdrawable"))
}

// ₦8,990 fits within the ₦9,000 withdrawable; ₦8,990 + ₦25 does not.
func TestMoney31_TheFeeCountsAgainstWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	req := w.approved(t, w.bMem, 899000)
	refused(t, w.pay(t, req, w.bMem, 899000), "₦8,990 + ₦25 against ₦9,000 withdrawable", refusedAs("withdrawal_within_withdrawable"))
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
func TestMoney32_AGuaranteeHoldsBackTheLoansOutstanding(t *testing.T) {
	w, _ := guarantorWorld(t)
	req := w.approved(t, w.g1Mem, 300000)
	refused(t, w.pay(t, req, w.g1Mem, 300000), "₦3,000 out of a guarantor's savings while ₦24,000 they guarantee is outstanding",
		refusedAs("withdrawal_within_withdrawable"))
}

// Per PERSON, not per member record (signatory_approvals' lesson): the
// guarantor's other record does not escape the hold.
func TestMoney33_AGuaranteeHoldsThroughThePersonsOtherRecord(t *testing.T) {
	w, _ := guarantorWorld(t)
	other := w.c.s.member(w.g1)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, other), "the guarantor's second record")
	w.saved(t, other, loanSavings)
	req := w.approved(t, other, 300000)
	refused(t, w.pay(t, req, other, 300000), "the guarantor withdrawing through their second member record",
		refusedAs("withdrawal_within_withdrawable"))
}

func TestMoney_Control_WithdrawingExactlyWhatMayBeWithdrawn(t *testing.T) {
	w := newMoneyWorld(t)
	amount := borrowerWithdrawable - 2500 // ₦8,975 + ₦25 = ₦9,000
	req := w.approved(t, w.bMem, amount)
	accepted(t, w.pay(t, req, w.bMem, amount), "₦8,975 + ₦25, exactly the ₦9,000 the borrower may withdraw")
}

func TestMoney_Control_ARepaidLoanHoldsNothing(t *testing.T) {
	w, loan := guarantorWorld(t)
	_, err := w.repay(loan, loanPrincipal)
	fixture(t, err, "the loan repaid in full")
	req := w.approved(t, w.g1Mem, 300000)
	accepted(t, w.pay(t, req, w.g1Mem, 300000), "a guarantor withdrawing once the loan they guaranteed owes nothing")
}

// =============================================================================
// WHO AUTHORISES A WITHDRAWAL
// =============================================================================

func TestMoney34_AWithdrawalRequestNamesItsMember(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise("", 300000, 2)
	refused(t, err, "a withdrawal request out of nobody's savings", refusedAs("withdrawal_request_names_member"))
}

func TestMoney35_AnUndecidedQuorumRefuses(t *testing.T) {
	w := newMoneyWorld(t)
	fixture(t, one(`UPDATE societies SET withdrawal_signatures_required = NULL WHERE id = $1`, w.c.s.id), "the quorum undecided")
	_, err := w.raise(w.bMem, 300000, 2)
	refused(t, err, "a withdrawal request in a society that has not decided how many signatures one needs",
		refusedAs("withdrawal_quorum_unset"))
}

// The guard threshold is interface friction; it never makes a withdrawal
// unilateral (§8).
func TestMoney36_TheQuorumIsTheSocietys(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise(w.bMem, 300000, 1)
	refused(t, err, "a withdrawal request asking for one signature where the society decided two",
		refusedAs("withdrawal_quorum_is_societys"))
}

// Proposed (the class of finding 19): the member a withdrawal is for never
// signs it, even when they hold a signing post.
func TestMoney37_AMemberNeverSignsTheirOwnWithdrawal(t *testing.T) {
	w := newMoneyWorld(t)
	w.saved(t, w.s1Mem, loanSavings)
	req, err := w.raise(w.s1Mem, 300000, 2)
	fixture(t, err, "the treasurer's own withdrawal request")
	refused(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the treasurer signing the withdrawal of their own savings",
		refusedAs("requester_never_signs_own_withdrawal"))
}

func TestMoney_Control_ARequestAtTheSocietysQuorum(t *testing.T) {
	w := newMoneyWorld(t)
	_, err := w.raise(w.bMem, 300000, 2)
	accepted(t, err, "a withdrawal request at the society's two signatures")
}
