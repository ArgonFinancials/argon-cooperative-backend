//go:build tier1

// THE SPEC'S LISTS ARE THE SCHEMA'S (2026-10-22). Every enumeration the spec
// writes beside a database type or constraint is compared with it, here, read
// from the catalog: what the schema ALLOWS, never a list someone maintains.
//
// Found by the frontend generating its client: the spec's AccountRole lacked
// four of the schema's roles (loan_loss and loan_recovery_income since 0009,
// charges_vat and statutory_levy_payable since 0011), so the books endpoints
// could return a role the spec said did not exist, to a client with no type
// for it. The sixth hand-written list beside a schema to drift. Anything
// enumerated beside a schema drifts; only generating it or failing the build
// stops it. TestApiMoney01 is the model: it reads what the schema allows.
package tier1

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/argon/backend/internal/api"
)

// specEnum: the enum at a schema, or at one of its properties, in the spec the
// server serves (api.GetSwagger: generated from api/openapi.yaml).
func specEnum(t *testing.T, doc *openapi3.T, schema, prop string) []string {
	t.Helper()
	ref, ok := doc.Components.Schemas[schema]
	if !ok || ref.Value == nil {
		t.Fatalf("FIXTURE: the spec has no schema %s", schema)
	}
	s := ref.Value
	if prop != "" {
		p, ok := s.Properties[prop]
		if !ok || p.Value == nil {
			t.Fatalf("FIXTURE: the spec's %s has no property %s", schema, prop)
		}
		s = p.Value
	}
	var out []string
	for _, v := range s.Enum {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	// The sabotage that proves the comparison can fail (break spec_list_drift):
	// the spec read as if it had lost one value.
	if os.Getenv("ARGON_TEST_API_FAKE") == "spec_list_drift" && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out
}

// dbEnumType: the values of a database enum type.
func dbEnumType(t *testing.T, typ string) []string {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT e.enumlabel FROM pg_enum e JOIN pg_type ty ON ty.oid = e.enumtypid WHERE ty.typname = $1 ORDER BY e.enumsortorder`, typ)
	fixture(t, err, "the enum type "+typ)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		fixture(t, rows.Scan(&v), "an enum label")
		out = append(out, v)
	}
	if len(out) == 0 {
		t.Fatalf("FIXTURE: the schema has no enum type %s", typ)
	}
	return out
}

var quotedLiteral = regexp.MustCompile(`'([a-z_]+)'`)

// dbCheck: the values a CHECK constraint lists, from its definition.
func dbCheck(t *testing.T, table, constraint string) []string {
	t.Helper()
	var def string
	fixture(t, db.QueryRow(bg(), `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid
		WHERE r.relname = $1 AND c.conname = $2`, table, constraint).Scan(&def), "the constraint "+table+"."+constraint)
	var out []string
	for _, m := range quotedLiteral.FindAllStringSubmatch(def, -1) {
		out = append(out, m[1])
	}
	return out
}

func setDiff(a, b []string) (onlyA, onlyB []string) {
	in := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	for x := range ma {
		if !mb[x] {
			onlyA = append(onlyA, x)
		}
	}
	for x := range mb {
		if !ma[x] {
			onlyB = append(onlyB, x)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return
}

// Every pair: the spec's list, the schema's, and any difference that is
// DELIBERATE, named with why. A difference not named here fails.
func TestApiSpec01_TheSpecsListsAreTheSchemas(t *testing.T) {
	doc, err := api.GetSwagger()
	fixture(t, err, "the spec the server serves")
	without := func(xs []string, drop ...string) []string {
		var out []string
		for _, x := range xs {
			keep := true
			for _, d := range drop {
				if x == d {
					keep = false
				}
			}
			if keep {
				out = append(out, x)
			}
		}
		return out
	}
	pairs := []struct {
		what         string
		spec, schema []string
	}{
		{"MovementKind / movement_kind", specEnum(t, doc, "MovementKind", ""), dbEnumType(t, "movement_kind")},
		{"OfficerPost / officer_post", specEnum(t, doc, "OfficerPost", ""), dbEnumType(t, "officer_post")},
		{"PostedBy.post / officer_post", specEnum(t, doc, "PostedBy", "post"), dbEnumType(t, "officer_post")},
		{"AccountKind / account_kind", specEnum(t, doc, "AccountKind", ""), dbEnumType(t, "account_kind")},
		{"AccountRole / accounts.account_role_known", specEnum(t, doc, "AccountRole", ""), dbCheck(t, "accounts", "account_role_known")},
		{"ChargeRole / ledger_lines.charge_role", specEnum(t, doc, "ChargeRole", ""), dbCheck(t, "ledger_lines", "ledger_lines_charge_role_check")},
		// A statement's charge line is a CHARGE: every role but the principal.
		{"ChargeLine.role / ledger_lines.charge_role less principal", specEnum(t, doc, "ChargeLine", "role"),
			without(dbCheck(t, "ledger_lines", "ledger_lines_charge_role_check"), "principal")},
		// "referred" is the MEMBER's word for a request rejected as looks_wrong
		// (the fraud path): a projection, never a stored state.
		{"ApprovalStatus / approval_state, plus referred", without(specEnum(t, doc, "ApprovalStatus", ""), "referred"), dbEnumType(t, "approval_state")},
		// NOT COMPARED, and why: Discrepancy.kind has no schema list to compare
		// with. ledger_discrepancies.kind is unconstrained TEXT (0012): only the
		// triggers decide what is written. A gap, recorded (finding 55), not a
		// pair to invent.
	}
	var drift []string
	for _, p := range pairs {
		onlySpec, onlySchema := setDiff(p.spec, p.schema)
		if len(onlySpec) > 0 || len(onlySchema) > 0 {
			drift = append(drift, p.what+": the schema has "+strings.Join(onlySchema, ", ")+" the spec lacks; the spec has "+strings.Join(onlySpec, ", ")+" the schema lacks")
		}
	}
	if len(drift) > 0 {
		t.Fatalf("DRIFTED — the spec's lists are not the schema's:\n  %s", strings.Join(drift, "\n  "))
	}
}
