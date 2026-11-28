//go:build tier1

// ACCEPTANCE TESTS — 0044, PERIOD CLOSE (stage 4, 2026-11-27; LEDGER_CONTRACT
// §5; the reviewer's four defaults).
//
//   - the books are closed THROUGH a date the society chooses, only forward,
//     never a day to come;
//   - two officers: one proposes, a different one confirms or rejects (with
//     a reason), each on their own act, never the auditor; one waits at a time;
//   - confirmed only over books that sum to zero, the trial balance kept;
//   - closed is read-only: nothing posts with a value date on or before the
//     date, nor undated; a correction still posts, today;
//   - nothing is edited.
package tier1

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
)

const pcThrough = "2026-09-30" // the fixtures' money is dated 1 September 2026

func pcPropose(t *testing.T, actor, society, through, actKind string) (string, error) {
	t.Helper()
	if actKind == "" {
		actKind = "period_close"
	}
	proof := stepUp(t, actor, actKind)
	var id string
	err := tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, actKind, actor, proof).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(bg(), `INSERT INTO period_closes (society_id, through, proposed_by, proposed_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			society, through, actor, act).Scan(&id)
	})
	return id, err
}

func pcDecide(t *testing.T, actor, close string, confirmed bool, reason, actKind string) error {
	t.Helper()
	if actKind == "" {
		actKind = "period_close_decision"
	}
	proof := stepUp(t, actor, actKind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, actKind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO period_close_decisions (close_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			close, confirmed, actor, act, nz(reason))
		return err
	})
}

func closedThrough(t *testing.T, society string) string {
	t.Helper()
	var d *string
	fixture(t, db.QueryRow(bg(), `SELECT society_closed_through($1)::text`, society).Scan(&d), "the closed date")
	if d == nil {
		return ""
	}
	return *d
}

// pcClosed: the books closed through pcThrough by the treasurer and the chairman.
func pcClosed(t *testing.T, w *dqWorld) {
	t.Helper()
	c, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	fixture(t, err, "the treasurer's proposal")
	fixture(t, pcDecide(t, w.s2, c, true, "", ""), "the chairman's confirmation")
}

func pcContribution(w *dqWorld, t *testing.T, valueDate string) error {
	return w.post(t, move{kind: "contribution_in", actor: w.c.s.actor, valueDate: valueDate, lines: w.contribution(w.bMem, 10000, fees{})})
}

func TestPeriodClose01_TwoOfficersCloseAndTheTrialBalanceIsKept(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	fixture(t, err, "the treasurer's proposal")
	if d := closedThrough(t, w.c.s.id); d != "" {
		t.Fatalf("CLOSED — a proposal alone closed the books through %s; a second officer confirms", d)
	}
	refused(t, pcDecide(t, w.s1, c, true, "", ""), "the proposer confirming their own close", refusedAs("period_close_second_officer"))
	accepted(t, pcDecide(t, w.s2, c, true, "", ""), "the chairman confirming")
	if d := closedThrough(t, w.c.s.id); d != pcThrough {
		t.Fatalf("NOT CLOSED — confirmed, the books read closed through %q; want %s", d, pcThrough)
	}
	var raw []byte
	fixture(t, db.QueryRow(bg(), `SELECT trial_balance FROM period_close_decisions WHERE close_id = $1`, c).Scan(&raw), "the trial balance kept")
	var rows []struct {
		Code    string `json:"code"`
		Balance int64  `json:"balance_kobo"`
	}
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		t.Fatalf("NOT KEPT — the confirmation keeps no trial balance: %s", raw)
	}
	var sum, savings int64
	for _, r := range rows {
		sum += r.Balance
		if r.Code == "2000" {
			savings = r.Balance
		}
	}
	if sum != 0 || savings == 0 {
		t.Fatalf("WRONG TRIAL BALANCE — kept %s: sums to %d, savings %d; want every account's balance through the date, summing to zero", raw, sum, savings)
	}
}

func TestPeriodClose02_OnlyForwardNeverADayToCome(t *testing.T) {
	w := newDisputeWorld(t)
	var tomorrow string
	fixture(t, db.QueryRow(bg(), `SELECT (society_today() + 1)::text`).Scan(&tomorrow), "tomorrow")
	_, err := pcPropose(t, w.s1, w.c.s.id, tomorrow, "")
	refused(t, err, "a close through a day to come", refusedAs("period_close_not_future"))
	pcClosed(t, w)
	_, err = pcPropose(t, w.s1, w.c.s.id, "2026-09-15", "")
	refused(t, err, "a close through an earlier date than the books are closed", refusedAs("period_close_forward"))
	_, err = pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	refused(t, err, "a close through the date already closed", refusedAs("period_close_forward"))
	_, err = pcPropose(t, w.s1, w.c.s.id, "2026-10-31", "")
	accepted(t, err, "a close further forward")
}

func TestPeriodClose03_AClosedPeriodIsReadOnly(t *testing.T) {
	w := newDisputeWorld(t)
	pcClosed(t, w)
	refused(t, pcContribution(w, t, "2026-09-20"), "a contribution dated inside the closed period", refusedAs("period_closed"))
	refused(t, pcContribution(w, t, pcThrough), "a contribution dated the closed day itself", refusedAs("period_closed"))
	_, err := w.c.s.postEntry(entry{kind: "opening_balance", unknownDate: true},
		line{acct: w.c.bank, kobo: 5000}, line{acct: w.c.migration, kobo: -5000})
	refused(t, err, "an undated opening balance (before every date) after a close", refusedAs("period_closed"))
	accepted(t, pcContribution(w, t, "2026-10-01"), "a contribution dated after the closed period")
}

func TestPeriodClose04_OnlyBooksThatBalanceAreClosed(t *testing.T) {
	w := newDisputeWorld(t)
	pcUnbalance(t, w.entry, w.c.bank)
	c, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	fixture(t, err, "the proposal")
	refused(t, pcDecide(t, w.s2, c, true, "", ""), "a close over books that do not sum to zero", refusedAs("period_close_balances"))
}

func TestPeriodClose05_EachHalfIsAnOfficersOwnAct(t *testing.T) {
	w := newDisputeWorld(t)
	_, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "member_status")
	refused(t, err, "a proposal on another kind of act", refusedAs("period_close_act"))
	_, err = pcPropose(t, w.auditor, w.c.s.id, pcThrough, "")
	refused(t, err, "the internal auditor proposing a close", refusedAs("period_close_by_officer"))
	c, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	fixture(t, err, "the proposal")
	refused(t, pcDecide(t, w.s2, c, true, "", "member_status"), "a decision on another kind of act", refusedAs("period_close_decision_act"))
	refused(t, pcDecide(t, w.auditor, c, true, "", ""), "the internal auditor deciding a close", refusedAs("period_close_decision_by_officer"))
}

func TestPeriodClose06_OneWaitsAndARejectionSaysWhyAndClosesNothing(t *testing.T) {
	w := newDisputeWorld(t)
	c, err := pcPropose(t, w.s1, w.c.s.id, pcThrough, "")
	fixture(t, err, "the proposal")
	_, err = pcPropose(t, w.s2, w.c.s.id, "2026-09-29", "")
	refused(t, err, "a second close while one waits", refusedAs("period_close_waits"))
	refused(t, pcDecide(t, w.s2, c, false, "", ""), "a rejection that does not say why", refusedAs("period_close_rejection_says_why"))
	accepted(t, pcDecide(t, w.s2, c, false, "September's bank statement has not come yet", ""), "the rejection, saying why")
	if d := closedThrough(t, w.c.s.id); d != "" {
		t.Fatalf("CLOSED — a rejected close closed the books through %s", d)
	}
	refused(t, pcDecide(t, w.s2, c, true, "", ""), "a second decision on one close", want{codes: []string{"23505"}})
}

func TestPeriodClose07_NothingIsEdited(t *testing.T) {
	w := newDisputeWorld(t)
	pcClosed(t, w)
	refused(t, one(`UPDATE period_closes SET through = '2026-08-31' WHERE society_id = $1`, w.c.s.id), "a close's date moved", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM period_close_decisions WHERE close_id IN (SELECT id FROM period_closes WHERE society_id = $1)`, w.c.s.id), "a confirmation deleted (the books reopened)", refusedAs("vfd_records_append_only"))
}

// A society that closes September and then finds an error in September still
// fixes it: the correction posts today, outside the closed period.
func TestPeriodClose_Control_ACorrectionOfAClosedPeriodPostsToday(t *testing.T) {
	w := newDisputeWorld(t)
	pcClosed(t, w)
	c, err := cxPropose(t, w.s1, w.entry, "reversal", cxWhy, nil, "")
	accepted(t, err, "a reversal proposed of an entry in the closed period")
	accepted(t, cxDecide(t, w.s2, c, true, "", ""), "the reversal confirmed, posted today")
}

// pcUnbalance: a defect made by hand as the superuser, a line the balance
// check never saw (the app cannot make one; that is the point of the check).
func pcUnbalance(t *testing.T, entry, account string) {
	t.Helper()
	u, err := url.Parse(adminURL)
	fixture(t, err, "the admin route")
	u.Path = "/" + dbName
	ac, err := pgx.Connect(bg(), u.String())
	fixture(t, err, "admin connect to the scratch database")
	defer ac.Close(bg())
	fixture(t, pgx.BeginFunc(bg(), ac, func(x pgx.Tx) error {
		if _, err := x.Exec(bg(), `SET LOCAL session_replication_role = replica`); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo) VALUES ($1, $2, 100)`, entry, account)
		return err
	}), "books that do not balance")
}
