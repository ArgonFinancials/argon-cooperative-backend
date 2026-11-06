//go:build tier1

// Suite 0023 — A SOCIETY IS FOUNDED BY THE OPERATOR, ON ITS FOUNDING
// RESOLUTION, ON THE STANDARD CHART. Written before the migration, for review.
//
// Decided 2026-11-06 (LEDGER_CONTRACT.md §12, "society creation"):
//   - THE OPERATOR CREATES A SOCIETY, through a function only the operator's
//     role may call (an API endpoint would need an Argon staff identity that
//     does not exist). Like every operator act (0014): a named operator, the
//     server login that ran it, a reason, logged append-only.
//   - THE FOUNDING RESOLUTION SETS ALL THREE QUORUMS (withdrawals, write-offs,
//     other governed acts), each at least 2, never the column defaults:
//     stated, attributable, never inherited. Each is logged with the
//     resolution, as 0014 logs a change.
//   - EVERY SOCIETY STARTS ON THE STANDARD CHART: one account for every
//     posting role, the same codes and names for every society (reviewed
//     once, by the Tier 1 accountant in time; not twelve-way variation). A
//     society's own 'general' accounts come after, as they already may.
//   - A head-office branch, because a member cannot exist without a branch.
//   - All or nothing: a refused founding leaves no society, no account, no log
//     (the one transaction's property; asserted, not mutation-proven).
//
// The backup gate (BACKUP_STORE s3:// and a passing S3 drill within 8 days)
// is the SCRIPT's (deploy/46-create-society.sh; test/ops/create_society_test.sh),
// deliberately NOT the database's: the database could only know through a row
// someone writes, and the owner can write it by hand.
package tier1

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const foundWhy = "Onboarding the society on its founding resolution"

type founding struct {
	code, name, reg, directorate string
	withdrawal, writeOff, sigs   int
	operator, login, ref, date   string
	reason                       string
}

// aFounding: a valid founding, every quorum a value no column defaults to
// (signatures_required defaults to 3), so a default left standing is visible.
func aFounding() founding {
	return founding{
		code:        fmt.Sprintf("F%d-%d", time.Now().UnixNano()%1e9, seq.Add(1)),
		name:        "Ikeja Teachers' Cooperative Society",
		reg:         "LSCS/2019/0412",
		directorate: "Lagos",
		withdrawal:  2, writeOff: 4, sigs: 5,
		operator: opName, login: opHost, ref: "Founding resolution, inaugural meeting, item 3", date: resolved,
		reason: foundWhy,
	}
}

// found calls the operator's function as the owner (the operator's role).
func found(f founding) (string, error) {
	var id string
	err := tx(func(x pgx.Tx) error {
		return x.QueryRow(bg(), `SELECT operator_create_society($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::date, $12)::text`,
			f.code, f.name, f.reg, f.directorate, f.withdrawal, f.writeOff, f.sigs,
			nz(f.operator), nz(f.login), nz(f.ref), nz(f.date), nz(f.reason)).Scan(&id)
	})
	return id, err
}

func founded(t *testing.T, f founding) string {
	t.Helper()
	id, err := found(f)
	fixture(t, err, "a valid founding")
	return id
}

// nothingFor: a refused founding left no society, no account, no log.
func nothingFor(t *testing.T, code string) {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT (SELECT count(*) FROM societies WHERE code = $1)
		+ (SELECT count(*) FROM accounts a JOIN societies s ON s.id = a.society_id WHERE s.code = $1)
		+ (SELECT count(*) FROM society_settings_log l JOIN societies s ON s.id = l.society_id WHERE s.code = $1)`, code).Scan(&n), "what a refusal left")
	if n != 0 {
		t.Fatalf("HALF-FOUNDED — a refused founding left %d rows behind for %s", n, code)
	}
}

// rolesTheDatabaseKnows: every role an account may have except 'general', read from the
// database's own rule (account_role_known), never a list written here.
func rolesTheDatabaseKnows(t *testing.T) []string {
	t.Helper()
	var def string
	fixture(t, db.QueryRow(bg(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'account_role_known'`).Scan(&def), "the role rule")
	var roles []string
	for _, p := range strings.Split(def, "'") {
		if p != "" && !strings.ContainsAny(p, "(), :") && p != "general" && p != "text" {
			roles = append(roles, p)
		}
	}
	if len(roles) < 5 {
		t.Fatalf("FIXTURE: read %d roles from %q", len(roles), def)
	}
	return roles
}

// =============================================================================
// WHO, AND ON WHOSE DECISION
// =============================================================================

func TestFound01_AFoundingNamesItsOperator(t *testing.T) {
	f := aFounding()
	f.operator = " "
	_, err := found(f)
	refused(t, err, "a society founded by nobody in particular", refusedAs("operator_names_themselves"))
	nothingFor(t, f.code)
}

func TestFound02_AFoundingRecordsTheServerLogin(t *testing.T) {
	f := aFounding()
	f.login = ""
	_, err := found(f)
	refused(t, err, "a society founded with no record of the login that ran it", refusedAs("operator_login_recorded"))
}

func TestFound03_AFoundingCarriesTheFoundingResolution(t *testing.T) {
	f := aFounding()
	f.ref = ""
	_, err := found(f)
	refused(t, err, "a society founded on no resolution", refusedAs("society_resolution_required"))
	nothingFor(t, f.code)
}

func TestFound04_TheResolutionIsNotDatedInTheFuture(t *testing.T) {
	f := aFounding()
	f.date = today(t, 7)
	_, err := found(f)
	refused(t, err, "a society founded on a resolution dated next week", refusedAs("society_resolution_not_future"))
}

func TestFound05_AFoundingSaysWhy(t *testing.T) {
	f := aFounding()
	f.reason = "  "
	_, err := found(f)
	refused(t, err, "a society founded with no reason given", refusedAs("operator_gives_reason"))
}

// A registered society: name, registration number and state directorate,
// never blank (NOT NULL alone accepts an empty string).
func TestFound06_ASocietyIsRegistered(t *testing.T) {
	for _, blank := range []func(*founding){
		func(f *founding) { f.name = " " },
		func(f *founding) { f.reg = "" },
		func(f *founding) { f.directorate = "  " },
	} {
		f := aFounding()
		blank(&f)
		_, err := found(f)
		refused(t, err, fmt.Sprintf("a society founded with a blank registration detail (%+v)", f), refusedAs("society_is_registered"))
		nothingFor(t, f.code)
	}
}

// =============================================================================
// THE FOUNDING RESOLUTION SETS ALL THREE QUORUMS
// =============================================================================

func TestFound07_AWithdrawalQuorumBelowTwoIsRefused(t *testing.T) {
	f := aFounding()
	f.withdrawal = 1
	_, err := found(f)
	refused(t, err, "a society founded with a withdrawal quorum of one", refusedAs("withdrawal_quorum_at_least_two"))
	nothingFor(t, f.code)
}

func TestFound08_AWriteOffQuorumBelowTwoIsRefused(t *testing.T) {
	f := aFounding()
	f.writeOff = 1
	_, err := found(f)
	refused(t, err, "a society founded with a write-off quorum of one", refusedAs("write_off_quorum_at_least_two"))
	nothingFor(t, f.code)
}

func TestFound09_ASignatureCountBelowTwoIsRefused(t *testing.T) {
	f := aFounding()
	f.sigs = 1
	_, err := found(f)
	refused(t, err, "a society founded with a signature count of one", refusedAs("signatures_required_at_least_two"))
	nothingFor(t, f.code)
}

// Never inherited: each quorum is the resolution's, whatever the column's default.
func TestFound10_EachQuorumIsTheResolutionsNeverADefault(t *testing.T) {
	f := aFounding()
	id := founded(t, f)
	var w, wo, s int
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(withdrawal_signatures_required, 0), coalesce(write_off_signatures_required, 0), signatures_required
		FROM societies WHERE id = $1`, id).Scan(&w, &wo, &s), "the society's quorums")
	if w != f.withdrawal || wo != f.writeOff || s != f.sigs {
		t.Fatalf("INHERITED — the resolution said withdrawals %d, write-offs %d, other acts %d; the society has %d, %d, %d",
			f.withdrawal, f.writeOff, f.sigs, w, wo, s)
	}
}

// Each quorum logged as 0014 logs a change: the setting, its value, the
// operator, the login, the resolution, the reason. Nothing before it.
func TestFound11_EachQuorumIsLoggedWithTheResolution(t *testing.T) {
	f := aFounding()
	id := founded(t, f)
	want := map[string]int{"withdrawal_signatures_required": f.withdrawal, "write_off_signatures_required": f.writeOff, "signatures_required": f.sigs}
	rows, err := db.Query(bg(), `SELECT setting, old_value, new_value, operator_name, server_login, resolution_ref, resolution_date::text, reason
		FROM society_settings_log WHERE society_id = $1 AND setting = ANY($2)`, id, []string{"withdrawal_signatures_required", "write_off_signatures_required", "signatures_required"})
	fixture(t, err, "the settings log")
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var setting, newV string
		var oldV, operator, login, ref, date, reason *string
		fixture(t, rows.Scan(&setting, &oldV, &newV, &operator, &login, &ref, &date, &reason), "a log row")
		if oldV != nil || newV != fmt.Sprint(want[setting]) || operator == nil || *operator != f.operator || login == nil || *login != f.login ||
			ref == nil || *ref != f.ref || date == nil || *date != f.date || reason == nil || *reason != f.reason {
			t.Fatalf("MISLOGGED — %s: old %v new %s operator %v login %v resolution %v %v reason %v", setting, oldV, newV, operator, login, ref, date, reason)
		}
		seen[setting] = true
	}
	if len(seen) != 3 {
		t.Fatalf("UNRECORDED — %d of the three quorums logged (%v)", len(seen), seen)
	}
}

// The founding itself, logged: who founded it, on what resolution, why.
func TestFound12_TheFoundingIsLogged(t *testing.T) {
	f := aFounding()
	id := founded(t, f)
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM society_settings_log WHERE society_id = $1 AND setting = 'founded'
		AND new_value = $2 AND operator_name = $3 AND server_login = $4 AND resolution_ref = $5 AND resolution_date = $6::date AND reason = $7`,
		id, f.code, f.operator, f.login, f.ref, f.date, f.reason).Scan(&n), "the founding's log row")
	if n != 1 {
		t.Fatalf("UNRECORDED — the founding of %s has %d log rows naming its operator, resolution and reason; want 1", f.code, n)
	}
}

// =============================================================================
// THE STANDARD CHART
// =============================================================================

// One account for every posting role the database knows, and nothing else.
func TestFound13_ASocietyStartsWithOneAccountPerPostingRole(t *testing.T) {
	id := founded(t, aFounding())
	have := map[string]int{}
	rows, err := db.Query(bg(), `SELECT role, count(*) FROM accounts WHERE society_id = $1 GROUP BY role`, id)
	fixture(t, err, "the chart")
	for rows.Next() {
		var r string
		var n int
		fixture(t, rows.Scan(&r, &n), "a role")
		have[r] = n
	}
	rows.Close()
	for _, r := range rolesTheDatabaseKnows(t) {
		if have[r] != 1 {
			t.Fatalf("INCOMPLETE CHART — role %s has %d accounts; want 1 (chart: %v)", r, have[r], have)
		}
		delete(have, r)
	}
	if len(have) != 0 {
		t.Fatalf("EXTRA ACCOUNTS — beyond one per posting role: %v (a society adds its own general accounts after)", have)
	}
}

// The same chart for every society: the standard's codes, names and kinds.
func TestFound14_EverySocietyGetsTheSameStandardChart(t *testing.T) {
	a, b := founded(t, aFounding()), founded(t, aFounding())
	var diff, std int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM standard_chart`).Scan(&std), "the standard chart")
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM (
		  (SELECT code, name, kind::text, role FROM accounts WHERE society_id = $1 EXCEPT SELECT code, name, kind::text, role FROM standard_chart)
		  UNION ALL (SELECT code, name, kind::text, role FROM standard_chart EXCEPT SELECT code, name, kind::text, role FROM accounts WHERE society_id = $1)
		  UNION ALL (SELECT code, name, kind::text, role FROM accounts WHERE society_id = $2 EXCEPT SELECT code, name, kind::text, role FROM standard_chart)
		) d`, a, b).Scan(&diff), "the charts against the standard")
	if std == 0 || diff != 0 {
		t.Fatalf("NOT THE STANDARD — %d accounts differ from the standard chart (%d rows)", diff, std)
	}
}

// =============================================================================
// WHERE IT STARTS, AND ALL OR NOTHING
// =============================================================================

func TestFound15_ASocietyStartsAsAProspectWithAHeadOffice(t *testing.T) {
	id := founded(t, aFounding())
	var life string
	var branches int
	fixture(t, db.QueryRow(bg(), `SELECT lifecycle::text, (SELECT count(*) FROM branches WHERE society_id = s.id) FROM societies s WHERE id = $1`, id).Scan(&life, &branches), "the society")
	if life != "prospect" || branches != 1 {
		t.Fatalf("WRONG START — lifecycle %s with %d branches; want prospect (Argon moves it on, 0014) and one head office", life, branches)
	}
}

// (TestFound16 was "a refused founding leaves nothing". Removed before
// review: all-or-nothing is the one transaction's, and no wrong function can
// break it; a signature count of zero is refused by 0001's signatures_sane
// whatever the function does. It could not fail at this layer: decoration,
// not a test. nothingFor stays in the refusal tests as a stated property,
// asserted there, proven by no mutant.)

func TestFound_Control_AFoundingOnItsResolution(t *testing.T) {
	f := aFounding()
	f.withdrawal, f.writeOff, f.sigs = 2, 2, 2
	id, err := found(f)
	accepted(t, err, "a founding at the lowest quorums, on its resolution")
	if id == "" {
		t.Fatalf("NO ID — the founding returned no society")
	}
}
