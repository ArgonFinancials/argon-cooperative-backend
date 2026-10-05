package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// THE SOCIETY'S BOOKS (LEDGER_CONTRACT §5): chart of accounts, trial balance,
// general ledger, journal. Written against the approved acceptance tests,
// test/tier1/tier1_api_books_test.go.
//
//   - Readers: an officer of the society holding a CURRENT post (the read-only
//     internal auditor included). Anyone else gets 404, byte-identical to a
//     society that does not exist.
//   - Date basis: the VALUE DATE. A NULL value date (an opening balance
//     "accumulated before migration") is before every date. Order: value date
//     (NULL first), then postedAt, then entry id.
//   - Figures are §0-signed: positive is a debit.

var booksNotFound = Problem{Code: "not_found", Message: "no such society"}

// mayReadBooks answers only yes or no: a society that does not exist and one
// the caller may not read are the same answer.
func (s *server) mayReadBooks(ctx context.Context, society string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $2) AND person_holds_post($1, $2)`,
		callerOf(ctx), society).Scan(&ok)
	return ok, err
}

func koboOf(v int64) Kobo { return Kobo(strconv.FormatInt(v, 10)) }

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

func dayOf(d *openapi_types.Date) string {
	if d == nil {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

func (s *server) GetChartOfAccounts(ctx context.Context, req GetChartOfAccountsRequestObject) (GetChartOfAccountsResponseObject, error) {
	if ok, err := s.mayReadBooks(ctx, req.SocietyId.String()); err != nil || !ok {
		return GetChartOfAccounts404JSONResponse(booksNotFound), err
	}
	rows, err := s.db.Query(ctx, `SELECT code, name, kind::text, role, postable FROM accounts WHERE society_id = $1 ORDER BY code`,
		req.SocietyId.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		var kind, role string
		if err := rows.Scan(&a.Code, &a.Name, &kind, &role, &a.Postable); err != nil {
			return nil, err
		}
		a.Kind, a.Role = AccountKind(kind), AccountRole(role)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return GetChartOfAccounts200JSONResponse(ChartOfAccounts{SocietyId: req.SocietyId, Accounts: out}), nil
}

// GetTrialBalance: every account of the chart, zeros included (an account
// missing from a trial balance reads as one at zero, and only one of those is
// correct). Turnover and balance per account; the totals sum to zero.
func (s *server) GetTrialBalance(ctx context.Context, req GetTrialBalanceRequestObject) (GetTrialBalanceResponseObject, error) {
	if ok, err := s.mayReadBooks(ctx, req.SocietyId.String()); err != nil || !ok {
		return GetTrialBalance404JSONResponse(booksNotFound), err
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.code, a.name, a.kind::text,
		       coalesce(sum(l.amount_kobo) FILTER (WHERE l.amount_kobo > 0), 0),
		       coalesce(sum(l.amount_kobo) FILTER (WHERE l.amount_kobo < 0), 0)
		  FROM accounts a
		  LEFT JOIN (ledger_lines l JOIN ledger_entries e
		               ON e.id = l.entry_id AND (e.value_date IS NULL OR e.value_date <= $2::date))
		         ON l.account_id = a.id
		 WHERE a.society_id = $1
		 GROUP BY a.id, a.code, a.name, a.kind
		 ORDER BY a.code`, req.SocietyId.String(), dayOf(&req.Params.AsOf))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrialBalanceRow{}
	var totD, totC int64
	for rows.Next() {
		var r TrialBalanceRow
		var kind string
		var d, c int64
		if err := rows.Scan(&r.Code, &r.Name, &kind, &d, &c); err != nil {
			return nil, err
		}
		totD, totC = totD+d, totC+c
		r.Kind, r.DebitsKobo, r.CreditsKobo, r.BalanceKobo = AccountKind(kind), koboOf(d), koboOf(c), koboOf(d+c)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Cannot fire while every entry balances at commit and 0006 keeps every
	// line in its entry's society. It stays: it is what stands between a
	// future rule change and books that silently stop summing to zero.
	if totD+totC != 0 {
		s.log.ErrorContext(ctx, "trial balance does not sum to zero", "society", req.SocietyId.String(),
			"asOf", dayOf(&req.Params.AsOf), "debits", totD, "credits", totC)
		return GetTrialBalance500JSONResponse(Problem{Code: "ledger_does_not_balance",
			Message: "the society's books do not sum to zero: a defect, not a variance"}), nil
	}
	return GetTrialBalance200JSONResponse(TrialBalance{SocietyId: req.SocietyId, AsOf: req.Params.AsOf,
		Rows: out, TotalDebitsKobo: koboOf(totD), TotalCreditsKobo: koboOf(totC)}), nil
}

// GetGeneralLedger: one account's movements with a running balance. Rows
// before `from` (every undated one among them) make the opening balance.
func (s *server) GetGeneralLedger(ctx context.Context, req GetGeneralLedgerRequestObject) (GetGeneralLedgerResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.mayReadBooks(ctx, society); err != nil || !ok {
		return GetGeneralLedger404JSONResponse(booksNotFound), err
	}
	var acct Account
	var accountID, kind, role string
	err := s.db.QueryRow(ctx, `SELECT id::text, code, name, kind::text, role, postable FROM accounts WHERE society_id = $1 AND code = $2`,
		society, req.AccountCode).Scan(&accountID, &acct.Code, &acct.Name, &kind, &role, &acct.Postable)
	if errors.Is(err, pgx.ErrNoRows) {
		return GetGeneralLedger404JSONResponse(Problem{Code: "not_found", Message: "no such account"}), nil
	}
	if err != nil {
		return nil, err
	}
	acct.Kind, acct.Role = AccountKind(kind), AccountRole(role)

	rows, err := s.db.Query(ctx, `
		SELECT e.id::text, e.value_date, e.posted_at, e.kind::text, b.reference, e.reason,
		       e.reverses_id::text, e.adjusts_id::text, sum(l.amount_kobo)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id
		  LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE l.account_id = $1
		 GROUP BY e.id, b.reference
		 ORDER BY e.value_date NULLS FIRST, e.posted_at, e.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	from, to := dayOf(req.Params.From), dayOf(req.Params.To)
	out := []LedgerRow{}
	var opening, bal int64
	for rows.Next() {
		var r LedgerRow
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
		if from != "" && (day == "" || day < from) { // undated: accumulated before migration, before every date
			opening += amt
			bal += amt
			continue
		}
		if to != "" && day != "" && day > to {
			continue
		}
		bal += amt
		r.EntryId, r.Kind, r.ValueDate = uuidOf(id), MovementKind(kind), dateOf(vd)
		r.ReversesEntryId, r.AdjustsEntryId = uuidPtr(rev), uuidPtr(adj)
		r.AmountKobo, r.BalanceKobo = koboOf(amt), koboOf(bal)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return GetGeneralLedger200JSONResponse(GeneralLedger{SocietyId: req.SocietyId, Account: acct,
		From: req.Params.From, To: req.Params.To, OpeningBalanceKobo: koboOf(opening), ClosingBalanceKobo: koboOf(bal), Rows: out}), nil
}

// GetJournal: entries with every line. Filters combine (AND). With `from`
// given, undated entries are before it and left out.
func (s *server) GetJournal(ctx context.Context, req GetJournalRequestObject) (GetJournalResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.mayReadBooks(ctx, society); err != nil || !ok {
		return GetJournal404JSONResponse(booksNotFound), err
	}
	p := req.Params
	var ref, member, actor, from, to any
	if p.Reference != nil {
		ref = *p.Reference
	}
	if p.MemberId != nil {
		member = p.MemberId.String()
	}
	if p.ActorUserId != nil {
		actor = p.ActorUserId.String()
	}
	if p.From != nil {
		from = dayOf(p.From)
	}
	if p.To != nil {
		to = dayOf(p.To)
	}
	rows, err := s.db.Query(ctx, `
		SELECT e.id::text, e.value_date, e.posted_at, e.kind::text, b.reference, e.reason,
		       e.reverses_id::text, e.adjusts_id::text, e.actor_user_id::text, e.actor_post::text, e.actor_post_label,
		       coalesce((SELECT array_agg(r.id::text ORDER BY r.posted_at, r.id) FROM ledger_entries r WHERE r.reverses_id = e.id), '{}')
		  FROM ledger_entries e LEFT JOIN ledger_batches b ON b.id = e.batch_id
		 WHERE e.society_id = $1
		   AND ($2::text IS NULL OR b.reference = $2)
		   AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM ledger_lines l WHERE l.entry_id = e.id AND l.member_id = $3::uuid))
		   AND ($4::uuid IS NULL OR e.actor_user_id = $4::uuid)
		   AND ($5::date IS NULL OR (e.value_date IS NOT NULL AND e.value_date >= $5::date))
		   AND ($6::date IS NULL OR e.value_date IS NULL OR e.value_date <= $6::date)
		 ORDER BY e.value_date NULLS FIRST, e.posted_at, e.id`, society, ref, member, actor, from, to)
	if err != nil {
		return nil, err
	}
	entries := []JournalEntry{}
	for rows.Next() {
		var e JournalEntry
		var id, kind, actorID, label string
		var vd *time.Time
		var rev, adj, post *string
		var reversedBy []string
		if err := rows.Scan(&id, &vd, &e.PostedAt, &kind, &e.Reference, &e.Reason, &rev, &adj, &actorID, &post, &label, &reversedBy); err != nil {
			rows.Close()
			return nil, err
		}
		e.EntryId, e.Kind, e.ValueDate = uuidOf(id), MovementKind(kind), dateOf(vd)
		e.ReversesEntryId, e.AdjustsEntryId = uuidPtr(rev), uuidPtr(adj)
		e.PostedBy = PostedBy{UserId: uuidOf(actorID), PostLabel: label}
		if post != nil {
			pp := PostedByPost(*post)
			e.PostedBy.Post = &pp
		}
		e.ReversedBy = []openapi_types.UUID{}
		for _, r := range reversedBy {
			e.ReversedBy = append(e.ReversedBy, uuidOf(r))
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range entries {
		if entries[i].Lines, err = s.journalLines(ctx, entries[i].EntryId.String()); err != nil {
			return nil, err
		}
	}
	return GetJournal200JSONResponse(Journal{SocietyId: req.SocietyId, Entries: entries}), nil
}

// journalLines: every line of one entry, debits first, then by account code.
func (s *server) journalLines(ctx context.Context, entry string) ([]JournalLine, error) {
	rows, err := s.db.Query(ctx, `SELECT a.code, a.name, l.amount_kobo, l.charge_role, l.member_id::text
		FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		WHERE l.entry_id = $1 ORDER BY (l.amount_kobo < 0), a.code, l.id`, entry)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JournalLine{}
	for rows.Next() {
		var jl JournalLine
		var amt int64
		var role, mem *string
		if err := rows.Scan(&jl.AccountCode, &jl.AccountName, &amt, &role, &mem); err != nil {
			return nil, err
		}
		jl.AmountKobo, jl.MemberId = koboOf(amt), uuidPtr(mem)
		if role != nil {
			cr := ChargeRole(*role)
			jl.ChargeRole = &cr
		}
		out = append(out, jl)
	}
	return out, rows.Err()
}
