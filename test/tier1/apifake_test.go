//go:build tier1

// THE MUTATION VEHICLE FOR THE API TESTS — test code, NOT the API.
//
// A reference implementation of GET /members/{id}/statement, mounted through
// the GENERATED router and strict-server types (so it already conforms to
// api/openapi.yaml), and breakable one named way per run
// (ARGON_TEST_API_FAKE=<break>). With no break it shows the tests are
// satisfiable; the real handlers live in internal/api and must pass the same
// tests on their own.
//
// Breaks:
//
//	member_sees_attribution  the member projection carries postedBy
//	officer_unattributed     the officer projection omits postedBy
//	two_queries              the officer's statement is a separate query that drops reversals
//	auditor_refused          (over-strict) the read-only auditor may not read statements
//	member_refused           (over-strict) a member may not read their own statement
//	charges_summed           partner and margin returned as one combined line
//	refund_as_charge         charge amounts unsigned: a refund reads as another charge
//	value_date_filled        a NULL value date is sent as the posting date
//	balance_contract_sign    balances sent §0-signed (in credit = negative)
//	direction_from_bank      direction read from a bank line ("in" when there is none)
//	unordered                rows ordered by entry id, not posting time
//	money_as_numbers         money fields sent as JSON numbers
//	nil_slices               empty lists sent as null
//	period_ignored           from/to ignored
//	leak_other_lines         every savings line of the entry is summed, other members' included
//	any_member_reads         any member of the society may read any member's statement
//	officer_any_society      an officer of ANY society may read
//	officer_ever             a post held at any time counts, not just now
//	not_found_says_why       a refusal says "not permitted" (distinguishable from "no such member")
//	ended_session_ok         an ended session still authenticates
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

type ctxUser struct{}

type fakeAPI struct {
	db  *pgxpool.Pool
	brk string
}

func newFakeAPI(pool *pgxpool.Pool, brk string) http.Handler {
	f := &fakeAPI{db: pool, brk: brk}
	h := api.HandlerFromMux(api.NewStrictHandler(f, nil), nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := f.caller(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(api.Problem{Code: "unauthenticated", Message: "sign in again"})
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxUser{}, user))
		if f.brk != "money_as_numbers" {
			h.ServeHTTP(w, r)
			return
		}
		rec := &recorder{header: http.Header{}}
		h.ServeHTTP(rec, r)
		body := regexp.MustCompile(`("[A-Za-z]*Kobo"):"(-?[0-9]+)"`).ReplaceAll(rec.buf.Bytes(), []byte(`$1:$2`))
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		w.Write(body)
	})
}

type recorder struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.buf.Write(b) }
func (r *recorder) WriteHeader(s int)           { r.status = s }

func (f *fakeAPI) caller(r *http.Request) (string, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		return "", false
	}
	var user string
	q := `SELECT user_id::text FROM auth_sessions WHERE token_digest = $1 AND session_valid($1)`
	if f.brk == "ended_session_ok" {
		q = `SELECT user_id::text FROM auth_sessions WHERE token_digest = $1`
	}
	if err := f.db.QueryRow(r.Context(), q, auth.TokenDigest(tok)).Scan(&user); err != nil {
		return "", false
	}
	return user, true
}

var notFound = api.GetMemberStatement404JSONResponse{Code: "not_found", Message: "no such member"}

func (f *fakeAPI) GetMemberStatement(ctx context.Context, req api.GetMemberStatementRequestObject) (api.GetMemberStatementResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	var m struct{ id, number, name, society string; user *string }
	if err := f.db.QueryRow(ctx, `SELECT id::text, member_number, full_name, society_id::text, user_id::text FROM members WHERE id = $1`,
		req.MemberId.String()).Scan(&m.id, &m.number, &m.name, &m.society, &m.user); err != nil {
		return notFound, nil
	}
	refuse := func() (api.GetMemberStatementResponseObject, error) {
		if f.brk == "not_found_says_why" {
			return api.GetMemberStatement404JSONResponse{Code: "not_permitted", Message: "you may not read this statement"}, nil
		}
		return notFound, nil
	}

	// Who may read, and which projection.
	projection := ""
	if m.user != nil && *m.user == caller && f.brk != "member_refused" {
		projection = "member"
	} else {
		term := `AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`
		if f.brk == "officer_ever" {
			term = ""
		}
		soc := `AND a.society_id = $2`
		if f.brk == "officer_any_society" {
			soc = `AND $2::text IS NOT NULL`
		}
		aud := ""
		if f.brk == "auditor_refused" {
			aud = `AND a.post <> 'internal_auditor'`
		}
		var officer bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
			WHERE am.user_id = $1 `+soc+` `+term+` `+aud+`)`, caller, m.society).Scan(&officer)
		if officer {
			projection = "officer"
		} else if f.brk == "any_member_reads" {
			var same bool
			f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE user_id = $1 AND society_id = $2)`, caller, m.society).Scan(&same)
			if same {
				projection = "member"
			}
		}
	}
	if projection == "" {
		return refuse()
	}

	// The ONE query (two_queries gives the officer a different one).
	savingsFilter := `l.member_id = $1`
	if f.brk == "leak_other_lines" {
		savingsFilter = `true`
	}
	extra := ""
	if f.brk == "two_queries" && projection == "officer" {
		extra = `AND e.reverses_id IS NULL`
	}
	order := `e.posted_at, e.id`
	if f.brk == "unordered" {
		order = `e.id`
	}
	rows, err := f.db.Query(ctx, `
		SELECT e.id::text, e.posted_at, e.value_date::text, e.kind::text, b.reference, e.reverses_id::text, e.adjusts_id::text, e.reason,
		       e.actor_user_id::text, e.actor_post::text, e.actor_post_label,
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		         WHERE l.entry_id = e.id AND a.role = 'member_savings' AND `+savingsFilter+`),
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l WHERE l.entry_id = e.id AND l.charge_role = 'partner_charge'),
		       (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l WHERE l.entry_id = e.id AND l.charge_role = 'argon_margin'),
		       EXISTS (SELECT 1 FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'bank' AND l.amount_kobo < 0)
		  FROM ledger_entries e LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE e.id IN (SELECT l.entry_id FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		                 WHERE l.member_id = $1 AND a.role = 'member_savings') `+extra+`
		 ORDER BY `+order, m.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out             []api.StatementRow
		opening, run    int64
		partner, margin int64
	)
	if f.brk != "nil_slices" {
		out = []api.StatementRow{}
	}
	from, to := req.Params.From, req.Params.To
	for rows.Next() {
		var (
			id, kind                                    string
			postedAt                                    time.Time
			valueDate, ref, rev, adj, reason, post      *string
			actor, label                                string
			sav, pc, am                                 int64
			bankOut                                     bool
		)
		if err := rows.Scan(&id, &postedAt, &valueDate, &kind, &ref, &rev, &adj, &reason, &actor, &post, &label, &sav, &pc, &am, &bankOut); err != nil {
			return nil, err
		}
		day := postedAt.Format("2006-01-02")
		if f.brk != "period_ignored" {
			if from != nil && day < from.Time.Format("2006-01-02") {
				opening -= sav
				run -= sav
				continue
			}
			if to != nil && day > to.Time.Format("2006-01-02") {
				continue
			}
		}
		run -= sav
		partner -= pc
		margin -= am
		if f.brk == "refund_as_charge" {
			partner += pc + abs(pc) // count every charge line as charged, refunds included
			margin += am + abs(am)
		}

		r := api.StatementRow{
			EntryId: uuidOf(id), PostedAt: postedAt, Kind: api.MovementKind(kind), Reference: ref,
			GrossKobo:       api.Magnitude(strconv.FormatInt(abs(sav+pc+am), 10)),
			SavingsLineKobo: api.Kobo(strconv.FormatInt(sav, 10)),
			SavingsBalanceKobo: api.SavingsBalance(strconv.FormatInt(run, 10)),
			Reason:          reason,
		}
		if f.brk == "balance_contract_sign" {
			r.SavingsBalanceKobo = api.SavingsBalance(strconv.FormatInt(-run, 10))
		}
		if valueDate != nil {
			d, _ := time.Parse("2006-01-02", *valueDate)
			r.ValueDate = &openapi_types.Date{Time: d}
		} else if f.brk == "value_date_filled" {
			r.ValueDate = &openapi_types.Date{Time: postedAt}
		}
		r.Direction = "in"
		if sav > 0 {
			r.Direction = "out"
		}
		if f.brk == "direction_from_bank" { // the frontend's defect: "in" unless a BANK line is a credit
			r.Direction = "in"
			if bankOut {
				r.Direction = "out"
			}
		}
		if rev != nil {
			u := uuidOf(*rev)
			r.ReversesEntryId = &u
		}
		if adj != nil {
			u := uuidOf(*adj)
			r.AdjustsEntryId = &u
		}
		if f.brk != "nil_slices" {
			r.Charges = []api.ChargeLine{}
		}
		amt := func(v int64) api.Kobo {
			if f.brk == "refund_as_charge" {
				return api.Kobo(strconv.FormatInt(-abs(v), 10))
			}
			return api.Kobo(strconv.FormatInt(v, 10))
		}
		if f.brk == "charges_summed" && (pc != 0 || am != 0) {
			r.Charges = append(r.Charges, api.ChargeLine{Role: "partner_charge", AmountKobo: amt(pc + am)})
		} else {
			if pc != 0 {
				r.Charges = append(r.Charges, api.ChargeLine{Role: "partner_charge", AmountKobo: amt(pc)})
			}
			if am != 0 {
				r.Charges = append(r.Charges, api.ChargeLine{Role: "argon_margin", AmountKobo: amt(am)})
			}
		}
		attributed := projection == "officer"
		if f.brk == "member_sees_attribution" {
			attributed = true
		}
		if f.brk == "officer_unattributed" {
			attributed = false
		}
		if attributed {
			pb := struct {
				Post      *api.StatementRowPostedByPost `json:"post"`
				PostLabel string                        `json:"postLabel"`
				UserId    openapi_types.UUID            `json:"userId"`
			}{PostLabel: label, UserId: uuidOf(actor)}
			if post != nil {
				p := api.StatementRowPostedByPost(*post)
				pb.Post = &p
			}
			r.PostedBy = &pb
		}
		out = append(out, r)
	}
	st := api.MemberStatement{
		MemberId: uuidOf(m.id), MemberNumber: m.number, MemberName: m.name, SocietyId: uuidOf(m.society),
		Projection: api.MemberStatementProjection(projection), From: from, To: to,
		OpeningSavingsBalanceKobo: api.SavingsBalance(strconv.FormatInt(opening, 10)),
		ClosingSavingsBalanceKobo: api.SavingsBalance(strconv.FormatInt(run, 10)),
		ChargedInPeriod: api.ChargedInPeriod{
			PartnerChargeKobo: api.SavingsBalance(strconv.FormatInt(partner, 10)),
			ArgonMarginKobo:   api.SavingsBalance(strconv.FormatInt(margin, 10)),
		},
		Rows: out,
	}
	if f.brk == "balance_contract_sign" {
		st.ClosingSavingsBalanceKobo = api.SavingsBalance(strconv.FormatInt(-run, 10))
	}
	return api.GetMemberStatement200JSONResponse(st), nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func uuidOf(s string) openapi_types.UUID {
	var u openapi_types.UUID
	u.UnmarshalText([]byte(s))
	return u
}

// The other paths in the spec, so the fake satisfies the generated interface.
// Not under test here.
func (f *fakeAPI) GetHealthz(context.Context, api.GetHealthzRequestObject) (api.GetHealthzResponseObject, error) {
	return api.GetHealthz200Response{}, nil
}

func (f *fakeAPI) PostLedgerBatches(context.Context, api.PostLedgerBatchesRequestObject) (api.PostLedgerBatchesResponseObject, error) {
	return api.PostLedgerBatches422Response{}, nil
}
