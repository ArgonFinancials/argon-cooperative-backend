//go:build tier1

// ACCEPTANCE TESTS — 0037, DEFAULTS NOBODY CHOSE (2026-11-24, the reviewer, on
// finding 75). A society's request window and guard threshold come from its
// founding resolution and are logged with it; a member without a status and a
// loan product without an interest rate are refused rather than given one;
// accounts.postable and the wallets tables, which nothing maintained, are gone.
package tier1

import (
	"fmt"
	"testing"
)

func foundWith(code string, days, guard any) error {
	return one(`SELECT operator_create_society($1, 'Ikeja Teachers Cooperative', 'LSCS/2019/0412', 'Lagos', 2, 2, 2, $2, $3,
		'Test Operator', 'test@host', 'Founding resolution', current_date, 'a test')`, code, days, guard)
}

func notNull(t *testing.T, err error, what string) {
	refused(t, err, what, want{codes: []string{"23502"}})
}

func TestDefaults01_FoundingSaysTheRequestWindowAndTheGuardThreshold(t *testing.T) {
	c := fmt.Sprintf("DF%d", seq.Add(1))
	fixture(t, foundWith(c, 14, 25000000), "a founding")
	var days int
	var guard int64
	var logged int
	fixture(t, db.QueryRow(bg(), `SELECT s.withdrawal_request_days, s.guard_threshold_kobo,
		(SELECT count(*) FROM society_settings_log l WHERE l.society_id = s.id AND (l.setting, l.new_value) IN (('withdrawal_request_days', '14'), ('guard_threshold_kobo', '25000000')))
		FROM societies s WHERE s.code = $1`, c).Scan(&days, &guard, &logged), "the society")
	if days != 14 || guard != 25000000 || logged != 2 {
		t.Fatalf("NOT THE RESOLUTION'S — the society reads %d days and %d kobo, %d of 2 logged; want 14, 25000000, both logged with the founding", days, guard, logged)
	}
}

func TestDefaults02_FoundingRefusesARequestWindowOutOfBounds(t *testing.T) {
	refused(t, foundWith(fmt.Sprintf("DF%d", seq.Add(1)), 0, 25000000), "a request window of no days", refusedAs("withdrawal_request_days_sane"))
}

func TestDefaults03_AMemberWithoutAStatusIsRefused(t *testing.T) {
	s := newSociety(t)
	notNull(t, one(`INSERT INTO members (society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on)
		VALUES ($1, $2, $3, 'No status', '+2347000000000', 'cash', '2020-01-01')`, s.id, s.branch, fmt.Sprintf("NS-%d", seq.Add(1))),
		"a member with no status said")
}

func TestDefaults04_ALoanProductWithoutAnInterestRateIsRefused(t *testing.T) {
	s := newSociety(t)
	notNull(t, one(`INSERT INTO loan_products (society_id, code, name, max_term_months, guarantors_required, savings_multiple_pct, allows_prepayment)
		VALUES ($1, 'unsaid', 'No rate said', 12, 2, 300, true)`, s.id), "a loan product with no interest rate said")
}

// An absence, proven against a schema that still has them (the mutants).
func TestDefaults05_PostableAndTheWalletsTablesAreGone(t *testing.T) {
	var postable, wallets, recs bool
	fixture(t, db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'accounts' AND column_name = 'postable'),
		to_regclass('public.wallets') IS NOT NULL, to_regclass('public.wallet_reconciliations') IS NOT NULL`).Scan(&postable, &wallets, &recs), "the schema")
	if postable || wallets || recs {
		t.Fatalf("STILL THERE — accounts.postable %v, wallets %v, wallet_reconciliations %v; nothing maintained them", postable, wallets, recs)
	}
}

// The control: a founding that states everything is accepted.
func TestDefaults_Control_AFoundingThatSaysEverythingIsAccepted(t *testing.T) {
	accepted(t, foundWith(fmt.Sprintf("DF%d", seq.Add(1)), 7, 10000000), "a founding with a week's window and a ₦100,000 guard threshold")
}
