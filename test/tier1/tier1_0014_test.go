//go:build tier1

// Suite 0014 — THE OPERATOR SETS A SOCIETY'S RULES, AND SAYS WHO, ON WHOSE
// DECISION. Written before the migration, for review.
//
// Decided 2026-10-13 (LEDGER_CONTRACT.md §10): a society's withdrawal quorum
// and account floor are bye-law decisions, and charges are configuration the
// app never writes. Until a request kind exists to change them, the OPERATOR
// sets them, through functions only the operator's role may call:
//
//	operator_set_withdrawal_quorum, operator_set_account_floor,
//	operator_add_charge_series
//
// Every call:
//   - NAMES THE OPERATOR, and records the server login that ran it. "Someone
//     with shell access" is not attribution.
//   - Is written to an append-only log (society_settings_log): the setting,
//     its old and new values, who, when, why.
//
// A quorum or floor change also carries THE SOCIETY'S RESOLUTION (minutes
// reference and date): the operator CARRIES OUT the society's decision; without
// it, an Argon staff member would be the author of a society's governance.
// Optional would mean absent under time pressure, so it is required. A
// resolution is passed before it is carried out: never dated in the future.
//
// A WITHDRAWAL QUORUM BELOW 2 IS REFUSED, and says why: a quorum of one is no
// quorum. It makes withdrawals unilateral, the single risk PRD 8.4 exists to
// prevent. A society cannot resolve its way out of the control that defines
// the product.
//
// A CHARGE SERIES IS NEVER BACKDATED: a contribution recorded late would be
// charged by a series that did not exist when the money arrived, at a figure
// the member was never told. A future date is allowed, so a society can be
// told what changes, and when, before it does.
package tier1

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

const (
	opName = "Adaeze Okafor (Argon operations)"
	opHost = "ubuntu@argon-prod (sudo by aokafor)"
)

// setQuorum calls the operator function as the owner (the operator's role).
func setQuorum(society string, n int, operator, login, ref, date, reason string) error {
	return one(`SELECT operator_set_withdrawal_quorum($1, $2, $3, $4, $5, $6::date, $7)`,
		society, n, nz(operator), nz(login), nz(ref), nz(date), nz(reason))
}

func setFloor(society string, kobo int64, operator, login, ref, date, reason string) error {
	return one(`SELECT operator_set_account_floor($1, $2, $3, $4, $5, $6::date, $7)`,
		society, kobo, nz(operator), nz(login), nz(ref), nz(date), nz(reason))
}

// addSeries: a series from JSON bands ([{"from":0,"to":null,"flat":1000,"bp":0,"cap":null}]).
func addSeries(society, kind, component, from, source, bands, operator, login, reason string) error {
	return one(`SELECT operator_add_charge_series($1, $2, $3, $4::date, $5, $6::jsonb, $7, $8, $9)`,
		society, kind, component, from, nz(source), bands, nz(operator), nz(login), nz(reason))
}

type logRow struct{ setting, oldV, newV, operator, login, ref, reason string }

// lastLog: the society's latest settings-log row.
func lastLog(t *testing.T, society string) logRow {
	t.Helper()
	var r logRow
	var oldV, ref *string
	// No row is what an unlogged change looks like: the test's failure, not
	// the fixture's (it reports UNRECORDED with an empty row).
	var operator, login, reason *string
	err := db.QueryRow(bg(), `SELECT setting, old_value, new_value, operator_name, server_login, resolution_ref, reason
		FROM society_settings_log WHERE society_id = $1 ORDER BY at DESC, id DESC LIMIT 1`, society).
		Scan(&r.setting, &oldV, &r.newV, &operator, &login, &ref, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return logRow{}
	}
	fixture(t, err, "the settings log")
	for _, p := range []struct {
		src *string
		dst *string
	}{{operator, &r.operator}, {login, &r.login}, {reason, &r.reason}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
	if oldV != nil {
		r.oldV = *oldV
	}
	if ref != nil {
		r.ref = *ref
	}
	return r
}

const (
	minutes  = "Minutes of the AGM, item 7 (resolution 2026/04)"
	resolved = "2026-09-30"
	opWhy    = "Carrying out the AGM's resolution"
)

func today(t *testing.T, plusDays int) string {
	t.Helper()
	var d string
	fixture(t, db.QueryRow(bg(), `SELECT (current_date + $1::int)::text`, plusDays).Scan(&d), "a date")
	return d
}

// =============================================================================
// WHO, AND ON WHOSE DECISION
// =============================================================================

func TestOperator01_AChangeNamesItsOperator(t *testing.T) {
	c := newChart(t)
	refused(t, setQuorum(c.s.id, 2, "  ", opHost, minutes, resolved, opWhy), "a quorum set by nobody in particular", refusedAs("operator_names_themselves"))
}

func TestOperator02_AChangeRecordsTheServerLogin(t *testing.T) {
	c := newChart(t)
	refused(t, setQuorum(c.s.id, 2, opName, "", minutes, resolved, opWhy), "a quorum set with no record of the login that ran it", refusedAs("operator_login_recorded"))
}

// The operator carries out the society's decision; they do not make it.
func TestOperator03_AQuorumChangeCarriesTheSocietysResolution(t *testing.T) {
	c := newChart(t)
	refused(t, setQuorum(c.s.id, 2, opName, opHost, "", resolved, opWhy), "a quorum set with no resolution behind it", refusedAs("society_resolution_required"))
}

func TestOperator04_AFloorChangeCarriesTheSocietysResolution(t *testing.T) {
	c := newChart(t)
	refused(t, setFloor(c.s.id, 100000, opName, opHost, minutes, "", opWhy), "a floor set with no resolution date", refusedAs("society_resolution_required"))
}

// A resolution is passed before it is carried out.
func TestOperator05_AResolutionIsNotDatedInTheFuture(t *testing.T) {
	c := newChart(t)
	refused(t, setQuorum(c.s.id, 2, opName, opHost, minutes, today(t, 7), opWhy), "a quorum carried out on a resolution dated next week",
		refusedAs("society_resolution_not_future"))
}

func TestOperator06_AChangeSaysWhy(t *testing.T) {
	c := newChart(t)
	refused(t, setFloor(c.s.id, 100000, opName, opHost, minutes, resolved, " "), "a floor set with no reason given", refusedAs("operator_gives_reason"))
}

// =============================================================================
// THE QUORUM AND THE FLOOR
// =============================================================================

// A quorum of one is no quorum: it makes withdrawals unilateral. And the
// refusal says so, rather than only refusing.
func TestOperator07_AWithdrawalQuorumBelowTwoIsRefused(t *testing.T) {
	c := newChart(t)
	err := setQuorum(c.s.id, 1, opName, opHost, minutes, resolved, opWhy)
	refused(t, err, "a withdrawal quorum of one", refusedAs("withdrawal_quorum_at_least_two"))
	if !strings.Contains(strings.ToLower(err.Error()), "unilateral") {
		t.Fatalf("UNEXPLAINED — the refusal does not say why a quorum of one is refused: %v", err)
	}
}

func TestOperator08_AFloorIsNeverNegative(t *testing.T) {
	c := newChart(t)
	refused(t, setFloor(c.s.id, -1, opName, opHost, minutes, resolved, opWhy), "a negative account floor", refusedAs("account_floor_not_negative"))
}

// =============================================================================
// THE LOG: OLD AND NEW, WHO, ON WHOSE DECISION, WHY
// =============================================================================

func TestOperator09_AQuorumChangeIsLoggedWithOldAndNew(t *testing.T) {
	c := newChart(t) // newSociety decided 2
	fixture(t, setQuorum(c.s.id, 3, opName, opHost, minutes, resolved, opWhy), "the quorum raised to 3")
	var q int
	fixture(t, db.QueryRow(bg(), `SELECT withdrawal_signatures_required FROM societies WHERE id = $1`, c.s.id).Scan(&q), "the society")
	r := lastLog(t, c.s.id)
	if q != 3 || r.setting != "withdrawal_signatures_required" || r.oldV != "2" || r.newV != "3" || r.operator != opName || r.login != opHost || r.ref != minutes || r.reason != opWhy {
		t.Fatalf("UNRECORDED — quorum %d, log %+v; want 3, and the setting, 2 → 3, the operator, the login, the resolution and the reason", q, r)
	}
}

func TestOperator10_AFloorChangeIsLogged(t *testing.T) {
	c := newChart(t)
	fixture(t, setFloor(c.s.id, 100000, opName, opHost, minutes, resolved, opWhy), "the floor set")
	r := lastLog(t, c.s.id)
	if r.setting != "account_floor_kobo" || r.oldV != "" || r.newV != "100000" || r.operator != opName {
		t.Fatalf("UNRECORDED — log %+v; want account_floor_kobo, unset → 100000, by the operator", r)
	}
}

func TestOperator11_TheLogIsNeverEdited(t *testing.T) {
	c := newChart(t)
	fixture(t, setFloor(c.s.id, 100000, opName, opHost, minutes, resolved, opWhy), "the floor set")
	refused(t, one(`UPDATE society_settings_log SET operator_name = 'someone else' WHERE society_id = $1`, c.s.id),
		"the settings log's operator rewritten", refusedAs("society_settings_log_immutable"))
}

// =============================================================================
// CHARGE SERIES
// =============================================================================

var oneBand = `[{"from":0,"to":null,"flat":1000,"bp":0,"cap":null}]`

func TestOperator12_ASeriesIsNeverBackdated(t *testing.T) {
	c := newChart(t)
	refused(t, addSeries(c.s.id, "withdrawal_out", "argon_margin", today(t, -1), srcArgonFee, oneBand, opName, opHost, opWhy),
		"a charge series effective from yesterday", refusedAs("charge_series_not_backdated"))
}

func TestOperator13_ASeriesNamesItsOperator(t *testing.T) {
	c := newChart(t)
	refused(t, addSeries(c.s.id, "withdrawal_out", "argon_margin", today(t, 1), srcArgonFee, oneBand, "", opHost, opWhy),
		"a charge series added by nobody in particular", refusedAs("operator_names_themselves"))
}

func TestOperator14_ASeriesIsLoggedWithItsSource(t *testing.T) {
	c := newChart(t)
	fixture(t, addSeries(c.s.id, "withdrawal_out", "argon_margin", today(t, 1), srcArgonFee, oneBand, opName, opHost, opWhy), "the series added")
	r := lastLog(t, c.s.id)
	if r.setting != "charge_series" || !strings.Contains(r.newV, "argon_margin") || !strings.Contains(r.newV, srcArgonFee) || r.operator != opName {
		t.Fatalf("UNRECORDED — log %+v; want a charge_series row naming the component and its source, by the operator", r)
	}
}

// =============================================================================
// CONTROLS
// =============================================================================

func TestOperator_Control_AQuorumOfTwoOnTheSocietysResolution(t *testing.T) {
	c := newChart(t)
	accepted(t, setQuorum(c.s.id, 2, opName, opHost, minutes, resolved, opWhy), "a quorum of two, carried out on the AGM's resolution")
}

// Today is not backdating, and the future is a courtesy the society is owed.
func TestOperator_Control_ASeriesFromTodayOrLater(t *testing.T) {
	c := newChart(t)
	accepted(t, addSeries(c.s.id, "withdrawal_out", "argon_margin", today(t, 0), srcArgonFee, oneBand, opName, opHost, opWhy),
		"a series effective today")
	accepted(t, addSeries(c.s.id, "withdrawal_out", "argon_margin", today(t, 30), srcArgonFee, oneBand, opName, opHost, opWhy),
		"a series effective in thirty days, announced now")
}
