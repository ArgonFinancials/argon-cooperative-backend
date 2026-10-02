//go:build tier1

// THE MUTATION VEHICLE FOR postAll — test code, NOT the API.
//
// A reference implementation of POST /societies/{id}/ledger/batches, mounted
// through the generated router like the statement fake, and breakable one
// named way per run (ARGON_TEST_API_FAKE=<break>). With no break it shows the
// batch tests are satisfiable; internal/api must pass them on its own.
//
// Breaks:
//
//	wrong_side                  savings debited and migration control credited
//	unattributed                entries carry no actor post
//	source_line_dropped         entries do not record the file row they came from
//	asof_null_as_today          an unknown as-at date is stored as today
//	per_row_commit              each row commits on its own: a failure leaves the rows before it
//	claim_then_write            the reference is committed first, the entries in a second transaction
//	refusal_claims_key          a refused batch still records its reference
//	no_idempotency              a second call tries to post again
//	idempotency_after_validation  the body is judged before the reference is looked up
//	asof_from_request           the repeat's response carries the repeat's as-at date
//	record_from_request         the repeat's response counts the repeat's rows and total
//	asof_null_reads_as_posting_date  an unknown as-at date is answered as the posting date
//	race_unhandled              the reference is checked outside the transaction; a racing duplicate is a 500
//	no_society_lock             imports for one society are not serialised
//	balance_check_file_only     only the file is checked for members who already have a balance
//	reversed_still_counts       (over-strict) a reversed opening balance still blocks a re-import
//	total_trusted               the confirmed total is not compared with the rows
//	duplicate_member_ok         one member twice in one file is accepted
//	foreign_member_ok           a member of another society is accepted
//	unknown_member_says_why     another society's member is refused with a different answer from a nonexistent one
//	empty_ok                    an empty batch commits
//	zero_written                a zero amount is passed to the ledger
//	negative_as_magnitude       "-500" is read as 500
//	future_ok                   an as-at date in the future is accepted
//	period_closed_as_500        the closed-period refusal surfaces as a server failure
//	any_officer_imports         any current officer of the society may import
//	auditor_imports             the read-only internal auditor may import
//	outsider_told_forbidden     someone with no post here is told 403, which says the society exists
//	former_secretary_imports    a post held at any time counts
//	chart_any_account           with no migration_control account, any equity account is used
//	key_global                  the reference is looked up across every society
//
// Step-up breaks (0005; tier1_api_stepup_test.go):
//
//	proof_optional              no proof, and the batch posts anyway
//	batch_mints_own_proof       no proof, so the handler mints one and spends it in the same call
//	proof_any_act               a proof minted for another act is accepted
//	proof_reusable              the proof is checked but never spent
//	proof_never_stale           freshness is not checked (and the proof is not spent by an act)
//	proof_anyones               another person's proof is accepted
//	repeat_needs_proof          (over-strict) a repeat of a committed reference demands a proof
//	refusal_spends_proof        the proof is spent on arrival, before the body is judged
//	failure_spends_proof        the act is committed on its own, before the write lands
//	batch_unlinked              the act is written, but the batch does not name it
package tier1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

var batchNotFound = api.PostLedgerBatch404JSONResponse{Code: "not_found", Message: "no such society"}

type batchRefused struct {
	code string
	msg  string
	rows []api.BatchRow
}

func (r batchRefused) Error() string { return r.code }

func (r batchRefused) response() api.PostLedgerBatch422JSONResponse {
	out := api.PostLedgerBatch422JSONResponse{Code: api.BatchRefusalCode(r.code), Message: r.msg}
	out.Rows = []struct {
		Line     int                `json:"line"`
		MemberId openapi_types.UUID `json:"memberId"`
	}{}
	for _, row := range r.rows {
		out.Rows = append(out.Rows, struct {
			Line     int                `json:"line"`
			MemberId openapi_types.UUID `json:"memberId"`
		}{row.Line, row.MemberId})
	}
	return out
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (f *fakeAPI) PostLedgerBatch(ctx context.Context, req api.PostLedgerBatchRequestObject) (api.PostLedgerBatchResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	ref := req.Params.IdempotencyKey
	b := req.Body

	// 1. May this caller import for this society?
	term := `AND a.held_from <= auth_today() AND (a.held_until IS NULL OR a.held_until >= auth_today())`
	if f.brk == "former_secretary_imports" {
		term = ""
	}
	posts := `'secretary'`
	switch f.brk {
	case "any_officer_imports":
		posts = `'secretary','chairman','treasurer','branch_officer','loan_officer'`
	case "auditor_imports":
		posts = `'secretary','internal_auditor'`
	}
	var importer, officer, exists bool
	if err := f.db.QueryRow(ctx, `SELECT
		  EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		           WHERE m.user_id = $1 AND a.society_id = $2 AND a.post IN (`+posts+`) `+term+`),
		  person_holds_post($1, $2),
		  EXISTS (SELECT 1 FROM societies WHERE id = $2)`, caller, society).Scan(&importer, &officer, &exists); err != nil {
		return nil, err
	}
	if !importer {
		if officer || (f.brk == "outsider_told_forbidden" && exists) {
			return api.PostLedgerBatch403JSONResponse{Code: "not_permitted", Message: "importing opening balances is the secretary's"}, nil
		}
		return batchNotFound, nil
	}

	// race_unhandled: the reference is looked up before, and outside, the
	// transaction, with no lock; a duplicate that lands meanwhile is a 500.
	if f.brk == "race_unhandled" {
		if rec, ok, err := f.priorRecord(ctx, f.db, society, ref); err != nil || ok {
			return rec, err
		}
	}

	// claim_then_write: the reference is committed before anything else.
	if f.brk == "claim_then_write" {
		if rec, ok, err := f.priorRecord(ctx, f.db, society, ref); err != nil || ok {
			return rec, err
		}
		if _, err := f.db.Exec(ctx, `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo)
			VALUES ($1, $2, $3, 0, 0)`, society, ref, caller); err != nil {
			return nil, err
		}
	}

	var out api.PostLedgerBatchResponseObject
	err := pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		if f.brk != "no_society_lock" && f.brk != "race_unhandled" {
			if _, err := x.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('argon.batch:' || $1))`, society); err != nil {
				return err
			}
		}
		// 2. Already committed? Before the body is judged: a retry must be
		// recognised whatever it now says.
		if f.brk != "idempotency_after_validation" && f.brk != "no_idempotency" && f.brk != "claim_then_write" && f.brk != "race_unhandled" {
			rec, ok, err := f.priorRecord(ctx, x, society, ref)
			if err != nil {
				return err
			}
			if ok {
				if f.brk == "repeat_needs_proof" && b.PinProofId == nil {
					return stepUpRefused
				}
				out = f.fromRequest(rec, b)
				return nil
			}
		}
		if f.brk == "refusal_spends_proof" && b.PinProofId != nil {
			// The proof is spent on arrival, before the body is judged.
			f.db.Exec(ctx, `UPDATE auth_proofs SET consumed_at = auth_now() WHERE id = $1`, b.PinProofId.String())
		}
		// 3. Is the body sound?
		amounts, refusal := f.judge(ctx, x, society, b)
		if refusal != nil {
			return *refusal
		}
		if f.brk == "idempotency_after_validation" {
			rec, ok, err := f.priorRecord(ctx, x, society, ref)
			if err != nil {
				return err
			}
			if ok {
				out = rec
				return nil
			}
		}
		// 4. The accounts, by role.
		migration, savings, err := f.chart(ctx, x, society)
		if err != nil {
			return err
		}
		// 4b. The step-up (0005), after the body is judged and inside this
		// transaction: a refusal never touches the proof, a failure rolls the
		// spend back.
		act, err := f.stepUp(ctx, x, caller, b.PinProofId)
		if err != nil {
			return err
		}
		// 5. Write.
		var batch string
		if f.brk == "claim_then_write" {
			if err := x.QueryRow(ctx, `SELECT id::text FROM ledger_batches WHERE society_id = $1 AND reference = $2`, society, ref).Scan(&batch); err != nil {
				return err
			}
		} else {
			var total int64
			for _, a := range amounts {
				total += a
			}
			if err := x.QueryRow(ctx, `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`, society, ref, caller, len(b.Rows), total, nz(act)).Scan(&batch); err != nil {
				return err
			}
		}
		var asOf any
		if b.AsOf != nil {
			asOf = b.AsOf.Time.Format("2006-01-02")
		} else if f.brk == "asof_null_as_today" {
			asOf = time.Now().Format("2006-01-02")
		}
		var batchOf any = batch
		post, label := any("secretary"), "Secretary"
		if f.brk == "unattributed" {
			post, label = nil, "Officer"
		}
		for i, row := range b.Rows {
			var src any = row.Line
			if f.brk == "source_line_dropped" {
				src = nil
			}
			write := func(q querier) error {
				var entry string
				if err := q.QueryRow(ctx, `INSERT INTO ledger_entries (society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label, source_line)
					VALUES ($1, $2, 'opening_balance', $3, $4, $5, $6, $7) RETURNING id::text`,
					society, batchOf, asOf, caller, post, label, src).Scan(&entry); err != nil {
					return err
				}
				dr, cr := migration, savings
				if f.brk == "wrong_side" {
					dr, cr = savings, migration
				}
				drMember, crMember := any(nil), any(row.MemberId.String())
				if f.brk == "wrong_side" {
					drMember, crMember = crMember, drMember
				}
				if _, err := q.Exec(ctx, `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id)
					VALUES ($1, $2, $3, 'principal', $4), ($1, $5, $6, 'principal', $7)`,
					entry, dr, amounts[i], drMember, cr, -amounts[i], crMember); err != nil {
					return err
				}
				return nil
			}
			if f.brk == "per_row_commit" {
				// Each row on its own: the entry cannot point at the batch row,
				// which is still uncommitted in the outer transaction.
				batchOf = nil
				if err := pgx.BeginFunc(ctx, f.db, func(own pgx.Tx) error { return write(own) }); err != nil {
					return err
				}
				continue
			}
			if err := write(x); err != nil {
				return err
			}
		}
		rec, _, err := f.priorRecord(ctx, x, society, ref)
		if err != nil {
			return err
		}
		created := rec.(api.PostLedgerBatch200JSONResponse)
		created.AlreadyPosted = false
		out = api.PostLedgerBatch201JSONResponse(created)
		return nil
	})

	var refusal batchRefused
	var pg *pgconn.PgError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &refusal) && refusal.code == "__step_up":
		return api.PostLedgerBatch403JSONResponse{Code: "step_up_required", Message: refusal.msg}, nil
	case errors.As(err, &refusal) && refusal.code == "__bad_request":
		return api.PostLedgerBatch400JSONResponse{Code: "bad_request", Message: refusal.msg}, nil
	case errors.As(err, &refusal):
		if f.brk == "refusal_claims_key" {
			f.db.Exec(ctx, `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo)
				VALUES ($1, $2, $3, 0, 0)`, society, ref, caller)
		}
		return refusal.response(), nil
	case errors.As(err, &pg) && pg.ConstraintName == "entry_not_in_closed_period" && f.brk != "period_closed_as_500":
		return batchRefused{code: "period_closed", msg: "the as-at date falls in a closed period; nothing was posted"}.response(), nil
	case errors.As(err, &pg) && pg.ConstraintName == "ledger_batches_society_id_reference_key" && f.brk != "race_unhandled" && f.brk != "no_idempotency":
		// A duplicate committed while this one ran: answer with what it did.
		rec, ok, rerr := f.priorRecord(ctx, f.db, society, ref)
		if rerr == nil && ok {
			return rec, nil
		}
	case errors.Is(err, errChart):
		return api.PostLedgerBatch409JSONResponse{Code: "chart_incomplete", Message: err.Error()}, nil
	}
	return api.PostLedgerBatch500JSONResponse{Code: "post_failed",
		Message: "Nothing was posted. Retrying with the same reference is safe."}, nil
}

var errChart = errors.New("the society needs exactly one migration_control and one member_savings account")

func (f *fakeAPI) chart(ctx context.Context, q querier, society string) (migration, savings string, err error) {
	pick := func(role string) (string, error) {
		rows, err := q.Query(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND role = $2`, society, role)
		if err != nil {
			return "", err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return "", err
		}
		if len(ids) == 0 && role == "migration_control" && f.brk == "chart_any_account" {
			err := q.QueryRow(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND kind = 'equity' LIMIT 1`, society).Scan(&migration)
			if err == nil {
				return migration, nil
			}
			if err := q.QueryRow(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND role <> 'member_savings' LIMIT 1`, society).Scan(&migration); err == nil {
				return migration, nil
			}
		}
		if len(ids) != 1 {
			return "", errChart
		}
		return ids[0], nil
	}
	if migration, err = pick("migration_control"); err != nil {
		return
	}
	savings, err = pick("member_savings")
	return
}

// judge: every refusal, before anything is written. Returns each row's amount.
func (f *fakeAPI) judge(ctx context.Context, q querier, society string, b *api.LedgerBatch) ([]int64, *batchRefused) {
	if len(b.Rows) == 0 && f.brk != "empty_ok" {
		return nil, &batchRefused{code: "nothing_to_post", msg: "the batch has no rows"}
	}
	var amounts []int64
	var total int64
	var zero []api.BatchRow
	for _, row := range b.Rows {
		s := string(row.AmountKobo)
		if f.brk == "negative_as_magnitude" {
			s = strings.TrimPrefix(s, "-")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 || !digitsOnly(s) {
			return nil, &batchRefused{code: "__bad_request", msg: fmt.Sprintf("line %d: amountKobo must be a non-negative decimal kobo string", row.Line)}
		}
		if n == 0 {
			zero = append(zero, row)
		}
		amounts = append(amounts, n)
		total += n
	}
	if len(zero) > 0 && f.brk != "zero_written" {
		return nil, &batchRefused{code: "zero_amount", msg: "a ₦0 opening balance posts nothing; remove the row", rows: zero}
	}
	confirmed, err := strconv.ParseInt(string(b.ConfirmedTotalKobo), 10, 64)
	if err != nil || !digitsOnly(string(b.ConfirmedTotalKobo)) {
		return nil, &batchRefused{code: "__bad_request", msg: "confirmedTotalKobo must be a non-negative decimal kobo string"}
	}
	if confirmed != total && f.brk != "total_trusted" {
		return nil, &batchRefused{code: "total_mismatch", msg: fmt.Sprintf("the rows total %d kobo; %d was confirmed", total, confirmed)}
	}
	if b.AsOf != nil && f.brk != "future_ok" {
		var future bool
		q.QueryRow(ctx, `SELECT $1::date > auth_today()`, b.AsOf.Time.Format("2006-01-02")).Scan(&future)
		if future {
			return nil, &batchRefused{code: "as_of_in_future", msg: "the as-at date is after today"}
		}
	}
	seen := map[string]bool{}
	var dup, unknown, has []api.BatchRow
	for _, row := range b.Rows {
		id := row.MemberId.String()
		if seen[id] && f.brk != "duplicate_member_ok" {
			dup = append(dup, row)
		}
		seen[id] = true
		var inSociety, elsewhere bool
		q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2),
		                        EXISTS (SELECT 1 FROM members WHERE id = $1)`, id, society).Scan(&inSociety, &elsewhere)
		if !inSociety && !(f.brk == "foreign_member_ok" && elsewhere) {
			unknown = append(unknown, row)
			if f.brk == "unknown_member_says_why" && elsewhere {
				return nil, &batchRefused{code: "unknown_member", msg: "a member of another society", rows: []api.BatchRow{row}}
			}
			continue
		}
		if f.brk == "balance_check_file_only" {
			continue
		}
		reversal := `AND NOT EXISTS (SELECT 1 FROM ledger_entries r WHERE r.reverses_id = e.id)`
		if f.brk == "reversed_still_counts" {
			reversal = ""
		}
		var carries bool
		q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id
			JOIN accounts a ON a.id = l.account_id
			WHERE e.kind = 'opening_balance' AND e.reverses_id IS NULL AND l.member_id = $1
			  AND a.role = 'member_savings' `+reversal+`)`, id).Scan(&carries)
		if carries {
			has = append(has, row)
		}
	}
	switch {
	case len(dup) > 0:
		return nil, &batchRefused{code: "duplicate_member", msg: "a member appears more than once in this file", rows: dup}
	case len(unknown) > 0:
		return nil, &batchRefused{code: "unknown_member", msg: "no such member in this society", rows: unknown}
	case len(has) > 0:
		return nil, &batchRefused{code: "member_already_has_balance", msg: "these members already carry an opening balance", rows: has}
	}
	return amounts, nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// priorRecord: what the commit under this reference did, READ FROM ITS
// ENTRIES — the as-at date included, since 0002 dropped it from the batch.
func (f *fakeAPI) priorRecord(ctx context.Context, q querier, society, ref string) (api.PostLedgerBatchResponseObject, bool, error) {
	where := `b.society_id = $1 AND b.reference = $2`
	if f.brk == "key_global" {
		where = `$1::uuid IS NOT NULL AND b.reference = $2`
	}
	var (
		postedAt time.Time
		rows     int
		total    int64
		asOf     *time.Time
	)
	err := q.QueryRow(ctx, `SELECT b.posted_at, count(e.id), coalesce(sum(l.amount_kobo), 0), max(e.value_date)
		FROM ledger_batches b
		LEFT JOIN ledger_entries e ON e.batch_id = b.id
		LEFT JOIN ledger_lines l ON l.entry_id = e.id AND l.amount_kobo > 0
		WHERE `+where+` GROUP BY b.id, b.posted_at LIMIT 1`, society, ref).Scan(&postedAt, &rows, &total, &asOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rec := api.PostLedgerBatch200JSONResponse{Reference: ref, Rows: rows, TotalKobo: api.Magnitude(strconv.FormatInt(total, 10)),
		PostedAt: postedAt, AlreadyPosted: true}
	if asOf != nil {
		rec.AsOf = &openapi_types.Date{Time: *asOf}
	} else if f.brk == "asof_null_reads_as_posting_date" {
		rec.AsOf = &openapi_types.Date{Time: postedAt}
	}
	return rec, true, nil
}

// The two "from the request" breaks: a repeat answered with what the REPEAT
// says instead of what the first commit did.
func (f *fakeAPI) fromRequest(rec api.PostLedgerBatchResponseObject, b *api.LedgerBatch) api.PostLedgerBatchResponseObject {
	r := rec.(api.PostLedgerBatch200JSONResponse)
	switch f.brk {
	case "asof_from_request":
		r.AsOf = b.AsOf
	case "record_from_request":
		r.Rows, r.TotalKobo = len(b.Rows), b.ConfirmedTotalKobo
	}
	return r
}

var stepUpRefused = batchRefused{code: "__step_up", msg: "this import needs your PIN: a proof minted for it, in the last five minutes, not yet used"}

// stepUp checks the caller's proof and writes the act that spends it, in x.
// The suite's schema is the 0005 INTERFACE (no rules), so the reference checks
// in Go what the real migration will refuse by name.
func (f *fakeAPI) stepUp(ctx context.Context, x pgx.Tx, caller string, proofID *openapi_types.UUID) (string, error) {
	if proofID == nil {
		switch f.brk {
		case "proof_optional":
			return "", nil
		case "batch_mints_own_proof":
			var id string
			if err := f.db.QueryRow(ctx, `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', 'ledger_batch') RETURNING id::text`,
				caller).Scan(&id); err != nil {
				return "", err
			}
			p := uuidOf(id)
			proofID = &p
		default:
			return "", stepUpRefused
		}
	}
	pid := proofID.String()
	var owner string
	var kind *string
	var fresh, unused bool
	err := x.QueryRow(ctx, `SELECT user_id::text, act_kind, at > auth_now() - interval '5 minutes', consumed_at IS NULL
		FROM auth_proofs WHERE id = $1 AND method = 'pin'`, pid).Scan(&owner, &kind, &fresh, &unused)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", stepUpRefused
	case err != nil:
		return "", err
	case owner != caller && f.brk != "proof_anyones",
		(kind == nil || *kind != "ledger_batch") && f.brk != "proof_any_act",
		!fresh && f.brk != "proof_never_stale",
		!unused && f.brk != "proof_reusable":
		return "", stepUpRefused
	}
	if f.brk == "proof_reusable" || f.brk == "proof_never_stale" {
		return "", nil // checked here, never spent by an act
	}
	actor := caller
	if f.brk == "proof_anyones" {
		actor = owner
	}
	var q querier = x
	if f.brk == "failure_spends_proof" {
		q = f.db // the act commits on its own, before the write lands
	}
	var act string
	if err := q.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('ledger_batch', $1, $2) RETURNING id::text`,
		actor, pid).Scan(&act); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.ConstraintName == "act_needs_pin_proof" {
			return "", stepUpRefused
		}
		return "", err
	}
	if f.brk == "batch_unlinked" {
		return "", nil
	}
	return act, nil
}
