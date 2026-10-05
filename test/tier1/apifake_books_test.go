//go:build tier1

// THE MUTATION VEHICLE FOR THE BOOKS TESTS — test code, NOT the API.
//
// A reference implementation of the society's four book reads (LEDGER_CONTRACT
// §5): chart of accounts, trial balance, general ledger, journal. Mounted
// through the generated router like the statement fake (apifake_test.go), and
// breakable one named way per run (ARGON_TEST_API_FAKE=<break>).
//
// Breaks — who may read:
//
//	books_member_reads           a member of the society (no post) may read the books
//	books_officer_any_society    an officer of ANY society may read
//	books_officer_ever           a post held at any time counts, not just now
//	books_not_found_says_why     a refusal says "not permitted" (tells a society exists)
//	books_auditor_refused        (over-strict) the read-only internal auditor may not read
//
// Chart:
//
//	books_chart_by_name          ordered by name, not code
//	books_chart_all_postable     every account reported postable
//	books_chart_all_societies    every society's accounts
//
// Trial balance:
//
//	books_tb_posting_basis       as-at compares the POSTING date, not the value date
//	books_tb_undated_excluded    an opening balance with no as-at date is left out
//	books_tb_asof_ignored        entries after asOf are included
//	books_tb_quiet_accounts_dropped  accounts with no lines are left out
//	books_tb_netted              debits and credits netted: only the balance's side shown
//	books_tb_credits_unsigned    credits sent as a positive magnitude
//	books_asof_optional          a missing asOf means "today" (wrapper, apifake_test.go)
//
// General ledger:
//
//	books_gl_posting_order       rows ordered by posting time, not value date
//	books_gl_undated_last        NULL-dated rows sorted last
//	books_gl_opening_ignored     rows before `from` dropped, opening balance left at zero
//	books_gl_all_lines           a row's amount is the whole entry (every account), not this account
//	books_gl_to_by_posting       `to` compares the posting date
//	books_gl_unknown_account_ok  an unknown account code answers 200 with nothing in it
//
// Journal:
//
//	books_j_all_societies        other societies' entries included
//	books_j_reference_ignored    the reference filter is ignored
//	books_j_member_ignored       the member filter is ignored
//	books_j_actor_ignored        the actor filter is ignored
//	books_j_filters_or           filters combine with OR, not AND
//	books_j_charges_dropped      charge lines left out (an entry no longer sums to zero)
//	books_j_reversed_by_missing  reversedBy never filled
//	books_j_lines_by_code_desc   lines ordered by account code, descending
//	books_j_undated_after_from   with `from` given, NULL-dated entries still included
//
// Shared with the statement fake: money_as_numbers, nil_slices.
package tier1

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

const booksNotFoundMsg = "no such society"

// mayReadBooks: an officer of the society holding a CURRENT post, the
// internal auditor included. The second result is false when the refusal
// must look exactly like a society that does not exist.
func (f *fakeAPI) mayReadBooks(ctx context.Context, society string) (ok, exists bool) {
	caller := ctx.Value(ctxUser{}).(string)
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&exists)
	if !exists {
		return false, false
	}
	term := `AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`
	if f.brk == "books_officer_ever" {
		term = ""
	}
	soc := `AND a.society_id = $2`
	if f.brk == "books_officer_any_society" {
		soc = `AND $2::text IS NOT NULL`
	}
	aud := ""
	if f.brk == "books_auditor_refused" {
		aud = `AND a.post <> 'internal_auditor'`
	}
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
		WHERE am.user_id = $1 `+soc+` `+term+` `+aud+`)`, caller, society).Scan(&ok)
	if !ok && f.brk == "books_member_reads" {
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE user_id = $1 AND society_id = $2)`, caller, society).Scan(&ok)
	}
	return ok, true
}

func (f *fakeAPI) booksRefusal(exists bool) api.Problem {
	if exists && f.brk == "books_not_found_says_why" {
		return api.Problem{Code: "not_permitted", Message: "you may not read this society's books"}
	}
	return api.Problem{Code: "not_found", Message: booksNotFoundMsg}
}

func koboOf(v int64) api.Kobo { return api.Kobo(strconv.FormatInt(v, 10)) }

func dateOf(d *time.Time) *openapi_types.Date {
	if d == nil {
		return nil
	}
	return &openapi_types.Date{Time: *d}
}

func uuidPtr(s *string) *openapi_types.UUID {
	if s == nil {
		return nil
	}
	u := uuidOf(*s)
	return &u
}

// ---- chart -------------------------------------------------------------------

func (f *fakeAPI) GetChartOfAccounts(ctx context.Context, req api.GetChartOfAccountsRequestObject) (api.GetChartOfAccountsResponseObject, error) {
	society := req.SocietyId.String()
	if ok, exists := f.mayReadBooks(ctx, society); !ok {
		return api.GetChartOfAccounts404JSONResponse(f.booksRefusal(exists)), nil
	}
	where, order := `WHERE society_id = $1`, `ORDER BY code`
	if f.brk == "books_chart_all_societies" {
		where = `WHERE $1::uuid IS NOT NULL`
	}
	if f.brk == "books_chart_by_name" {
		order = `ORDER BY name, code`
	}
	rows, err := f.db.Query(ctx, `SELECT code, name, kind::text, role, postable FROM accounts `+where+` `+order, society)
	if err != nil {
		return nil, err
	}
	out := []api.Account{}
	if f.brk == "nil_slices" {
		out = nil
	}
	for rows.Next() {
		var a api.Account
		var kind, role string
		if err := rows.Scan(&a.Code, &a.Name, &kind, &role, &a.Postable); err != nil {
			return nil, err
		}
		a.Kind, a.Role = api.AccountKind(kind), api.AccountRole(role)
		if f.brk == "books_chart_all_postable" {
			a.Postable = true
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return api.GetChartOfAccounts200JSONResponse(api.ChartOfAccounts{SocietyId: req.SocietyId, Accounts: out}), nil
}

// ---- trial balance -------------------------------------------------------------

func (f *fakeAPI) GetTrialBalance(ctx context.Context, req api.GetTrialBalanceRequestObject) (api.GetTrialBalanceResponseObject, error) {
	society := req.SocietyId.String()
	if ok, exists := f.mayReadBooks(ctx, society); !ok {
		return api.GetTrialBalance404JSONResponse(f.booksRefusal(exists)), nil
	}
	asOf := req.Params.AsOf.Time.Format("2006-01-02")
	inDate := `(e.value_date IS NULL OR e.value_date <= $2::date)`
	switch f.brk {
	case "books_tb_posting_basis":
		inDate = `e.posted_at::date <= $2::date`
	case "books_tb_undated_excluded":
		inDate = `e.value_date <= $2::date`
	case "books_tb_asof_ignored":
		inDate = `$2::date IS NOT NULL`
	}
	having := ``
	if f.brk == "books_tb_quiet_accounts_dropped" {
		having = `HAVING count(l.id) > 0`
	}
	rows, err := f.db.Query(ctx, `
		SELECT a.code, a.name, a.kind::text,
		       coalesce(sum(l.amount_kobo) FILTER (WHERE l.amount_kobo > 0), 0),
		       coalesce(sum(l.amount_kobo) FILTER (WHERE l.amount_kobo < 0), 0)
		  FROM accounts a
		  LEFT JOIN (ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id AND `+inDate+`)
		         ON l.account_id = a.id
		 WHERE a.society_id = $1
		 GROUP BY a.id, a.code, a.name, a.kind `+having+`
		 ORDER BY a.code`, society, asOf)
	if err != nil {
		return nil, err
	}
	out := []api.TrialBalanceRow{}
	var totD, totC int64
	for rows.Next() {
		var r api.TrialBalanceRow
		var kind string
		var d, c int64
		if err := rows.Scan(&r.Code, &r.Name, &kind, &d, &c); err != nil {
			return nil, err
		}
		totD, totC = totD+d, totC+c
		bal := d + c
		if f.brk == "books_tb_netted" {
			d, c = max(bal, 0), min(bal, 0)
		}
		if f.brk == "books_tb_credits_unsigned" {
			c = -c
		}
		r.Kind, r.DebitsKobo, r.CreditsKobo, r.BalanceKobo = api.AccountKind(kind), koboOf(d), koboOf(c), koboOf(bal)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if totD+totC != 0 {
		return api.GetTrialBalance500JSONResponse(api.Problem{Code: "ledger_does_not_balance",
			Message: "the books do not sum to zero: a defect, not a variance"}), nil
	}
	if f.brk == "books_tb_credits_unsigned" {
		totC = -totC
	}
	return api.GetTrialBalance200JSONResponse(api.TrialBalance{SocietyId: req.SocietyId, AsOf: req.Params.AsOf,
		Rows: out, TotalDebitsKobo: koboOf(totD), TotalCreditsKobo: koboOf(totC)}), nil
}

// ---- general ledger -------------------------------------------------------------

func (f *fakeAPI) GetGeneralLedger(ctx context.Context, req api.GetGeneralLedgerRequestObject) (api.GetGeneralLedgerResponseObject, error) {
	society := req.SocietyId.String()
	if ok, exists := f.mayReadBooks(ctx, society); !ok {
		return api.GetGeneralLedger404JSONResponse(f.booksRefusal(exists)), nil
	}
	var acct api.Account
	var accountID, kind, role string
	err := f.db.QueryRow(ctx, `SELECT id::text, code, name, kind::text, role, postable FROM accounts WHERE society_id = $1 AND code = $2`,
		society, req.AccountCode).Scan(&accountID, &acct.Code, &acct.Name, &kind, &role, &acct.Postable)
	if errors.Is(err, pgx.ErrNoRows) {
		if f.brk == "books_gl_unknown_account_ok" {
			return api.GetGeneralLedger200JSONResponse(api.GeneralLedger{SocietyId: req.SocietyId, Account: api.Account{Code: req.AccountCode},
				From: req.Params.From, To: req.Params.To, OpeningBalanceKobo: "0", ClosingBalanceKobo: "0", Rows: []api.LedgerRow{}}), nil
		}
		return api.GetGeneralLedger404JSONResponse(api.Problem{Code: "not_found", Message: "no such account"}), nil
	}
	if err != nil {
		return nil, err
	}
	acct.Kind, acct.Role = api.AccountKind(kind), api.AccountRole(role)

	amount := `sum(l.amount_kobo)`
	if f.brk == "books_gl_all_lines" {
		amount = `(SELECT sum(x.amount_kobo) FROM ledger_lines x WHERE x.entry_id = e.id)`
	}
	order := `e.value_date NULLS FIRST, e.posted_at, e.id`
	switch f.brk {
	case "books_gl_posting_order":
		order = `e.posted_at, e.id`
	case "books_gl_undated_last":
		order = `e.value_date NULLS LAST, e.posted_at, e.id`
	}
	rows, err := f.db.Query(ctx, `
		SELECT e.id::text, e.value_date, e.posted_at, e.kind::text, b.reference, e.reason,
		       e.reverses_id::text, e.adjusts_id::text, `+amount+`
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id
		  LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE l.account_id = $1
		 GROUP BY e.id, b.reference
		 ORDER BY `+order, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var from, to string
	if req.Params.From != nil {
		from = req.Params.From.Time.Format("2006-01-02")
	}
	if req.Params.To != nil {
		to = req.Params.To.Time.Format("2006-01-02")
	}
	out := []api.LedgerRow{}
	var opening, bal int64
	for rows.Next() {
		var r api.LedgerRow
		var id, kind string
		var vd *time.Time
		var rev, adj *string
		var amt int64
		if err := rows.Scan(&id, &vd, &r.PostedAt, &kind, &r.Reference, &r.Reason, &rev, &adj, &amt); err != nil {
			return nil, err
		}
		day := ""
		if vd != nil {
			day = vd.Format("2006-01-02")
		}
		if f.brk == "books_gl_to_by_posting" {
			day = r.PostedAt.Format("2006-01-02")
		}
		if from != "" && (day == "" || day < from) { // NULL: accumulated before migration, before every date
			if f.brk != "books_gl_opening_ignored" {
				opening += amt
				bal += amt
			}
			continue
		}
		if to != "" && day != "" && day > to {
			continue
		}
		bal += amt
		r.EntryId, r.Kind, r.ValueDate = uuidOf(id), api.MovementKind(kind), dateOf(vd)
		r.ReversesEntryId, r.AdjustsEntryId = uuidPtr(rev), uuidPtr(adj)
		r.AmountKobo, r.BalanceKobo = koboOf(amt), koboOf(bal)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return api.GetGeneralLedger200JSONResponse(api.GeneralLedger{SocietyId: req.SocietyId, Account: acct,
		From: req.Params.From, To: req.Params.To, OpeningBalanceKobo: koboOf(opening), ClosingBalanceKobo: koboOf(bal), Rows: out}), nil
}

// ---- journal ---------------------------------------------------------------------

func (f *fakeAPI) GetJournal(ctx context.Context, req api.GetJournalRequestObject) (api.GetJournalResponseObject, error) {
	society := req.SocietyId.String()
	if ok, exists := f.mayReadBooks(ctx, society); !ok {
		return api.GetJournal404JSONResponse(f.booksRefusal(exists)), nil
	}
	p := req.Params
	var ref, member, actor, from, to any
	if p.Reference != nil && f.brk != "books_j_reference_ignored" {
		ref = *p.Reference
	}
	if p.MemberId != nil && f.brk != "books_j_member_ignored" {
		member = p.MemberId.String()
	}
	if p.ActorUserId != nil && f.brk != "books_j_actor_ignored" {
		actor = p.ActorUserId.String()
	}
	if p.From != nil {
		from = p.From.Time.Format("2006-01-02")
	}
	if p.To != nil {
		to = p.To.Time.Format("2006-01-02")
	}
	scope := `e.society_id = $1`
	if f.brk == "books_j_all_societies" {
		scope = `$1::uuid IS NOT NULL`
	}
	fromCond := `($5::date IS NULL OR (e.value_date IS NOT NULL AND e.value_date >= $5::date))`
	if f.brk == "books_j_undated_after_from" {
		fromCond = `($5::date IS NULL OR e.value_date IS NULL OR e.value_date >= $5::date)`
	}
	conds := []string{
		`($2::text IS NULL OR b.reference = $2)`,
		`($3::uuid IS NULL OR EXISTS (SELECT 1 FROM ledger_lines l WHERE l.entry_id = e.id AND l.member_id = $3::uuid))`,
		`($4::uuid IS NULL OR e.actor_user_id = $4::uuid)`,
		fromCond,
		`($6::date IS NULL OR e.value_date IS NULL OR e.value_date <= $6::date)`,
	}
	filters := conds[0] + ` AND ` + conds[1] + ` AND ` + conds[2] + ` AND ` + conds[3] + ` AND ` + conds[4]
	if f.brk == "books_j_filters_or" {
		// Each GIVEN filter alone is enough; with none given, everything.
		filters = `(($2::text IS NULL AND $3::uuid IS NULL AND $4::uuid IS NULL AND $5::date IS NULL AND $6::date IS NULL)
		  OR ($2::text IS NOT NULL AND b.reference = $2)
		  OR ($3::uuid IS NOT NULL AND EXISTS (SELECT 1 FROM ledger_lines l WHERE l.entry_id = e.id AND l.member_id = $3::uuid))
		  OR ($4::uuid IS NOT NULL AND e.actor_user_id = $4::uuid)
		  OR ($5::date IS NOT NULL AND e.value_date >= $5::date)
		  OR ($6::date IS NOT NULL AND e.value_date <= $6::date))`
	}
	rows, err := f.db.Query(ctx, `
		SELECT e.id::text, e.value_date, e.posted_at, e.kind::text, b.reference, e.reason,
		       e.reverses_id::text, e.adjusts_id::text, e.actor_user_id::text, e.actor_post::text, e.actor_post_label,
		       coalesce((SELECT array_agg(r.id::text ORDER BY r.posted_at, r.id) FROM ledger_entries r WHERE r.reverses_id = e.id), '{}')
		  FROM ledger_entries e LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE `+scope+` AND `+filters+`
		 ORDER BY e.value_date NULLS FIRST, e.posted_at, e.id`, society, ref, member, actor, from, to)
	if err != nil {
		return nil, err
	}
	var entries []api.JournalEntry
	for rows.Next() {
		var e api.JournalEntry
		var id, kind, actorID, label string
		var vd *time.Time
		var rev, adj, post *string
		var reversedBy []string
		if err := rows.Scan(&id, &vd, &e.PostedAt, &kind, &e.Reference, &e.Reason, &rev, &adj, &actorID, &post, &label, &reversedBy); err != nil {
			rows.Close()
			return nil, err
		}
		e.EntryId, e.Kind, e.ValueDate = uuidOf(id), api.MovementKind(kind), dateOf(vd)
		e.ReversesEntryId, e.AdjustsEntryId = uuidPtr(rev), uuidPtr(adj)
		e.PostedBy = api.PostedBy{UserId: uuidOf(actorID), PostLabel: label}
		if post != nil {
			pp := api.PostedByPost(*post)
			e.PostedBy.Post = &pp
		}
		e.ReversedBy = []openapi_types.UUID{}
		if f.brk == "nil_slices" {
			e.ReversedBy = nil
		}
		if f.brk != "books_j_reversed_by_missing" {
			for _, r := range reversedBy {
				e.ReversedBy = append(e.ReversedBy, uuidOf(r))
			}
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	lineWhere, lineOrder := ``, `(l.amount_kobo < 0), a.code, l.id`
	if f.brk == "books_j_charges_dropped" {
		lineWhere = `AND coalesce(l.charge_role, 'principal') = 'principal'`
	}
	if f.brk == "books_j_lines_by_code_desc" {
		lineOrder = `a.code DESC, l.id`
	}
	for i := range entries {
		lrows, err := f.db.Query(ctx, `SELECT a.code, a.name, l.amount_kobo, l.charge_role, l.member_id::text
			FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
			WHERE l.entry_id = $1 `+lineWhere+` ORDER BY `+lineOrder, entries[i].EntryId.String())
		if err != nil {
			return nil, err
		}
		entries[i].Lines = []api.JournalLine{}
		for lrows.Next() {
			var jl api.JournalLine
			var amt int64
			var role, mem *string
			if err := lrows.Scan(&jl.AccountCode, &jl.AccountName, &amt, &role, &mem); err != nil {
				lrows.Close()
				return nil, err
			}
			jl.AmountKobo, jl.MemberId = koboOf(amt), uuidPtr(mem)
			if role != nil {
				cr := api.ChargeRole(*role)
				jl.ChargeRole = &cr
			}
			entries[i].Lines = append(entries[i].Lines, jl)
		}
		lrows.Close()
	}
	if entries == nil && f.brk != "nil_slices" {
		entries = []api.JournalEntry{}
	}
	return api.GetJournal200JSONResponse(api.Journal{SocietyId: req.SocietyId, Entries: entries}), nil
}
