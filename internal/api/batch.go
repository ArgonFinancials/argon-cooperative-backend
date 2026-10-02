package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// postAll — LEDGER_CONTRACT §1. Written against test/tier1/tier1_api_batch_test.go
// (reviewed; mutation-tested with apifake_batch_test.go before this existed).
//
// One transaction, in this order:
//
//	who may import (outside: reads only)  → 404 / 403
//	lock the society's imports            → two files naming one member cannot both post
//	the reference already committed?      → 200, the FIRST commit's record, whatever the body says
//	is the body sound?                    → 400 / 422
//	the accounts, by role                 → 409
//	the step-up: an act spends the proof  → 403 step_up_required (0005)
//	write the batch, its entries, lines   → 201, or roll back: 500
//
// The reference is written in the same transaction as the entries, never
// before, so a refused or failed call — a timeout included — claims nothing
// and a retry with the same key can succeed.

var batchNotFound = PostLedgerBatch404JSONResponse{Code: "not_found", Message: "no such society"}

// The one post that imports (frontend permissions.ts: import_members). The
// console is a surface, not a role: whoever operates it does what their post allows.
const importerPost, importerLabel = "secretary", "Secretary"

type batchRefusal struct {
	status int // 400 or 422
	code   string
	msg    string
	rows   []BatchRow
}

func (r *batchRefusal) Error() string { return r.code }

var errStepUp = errors.New("step-up required")

// The refusals that mean "no usable proof": 0003's (missing, not the actor's,
// stale, spent) and 0005's (minted for another act, or in this transaction).
var stepUpRefusals = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true}

var errChart = errors.New("the society's chart cannot take this batch: it needs exactly one migration_control and one member_savings account")

func (s *server) PostLedgerBatch(ctx context.Context, req PostLedgerBatchRequestObject) (PostLedgerBatchResponseObject, error) {
	caller, society, ref, b := callerOf(ctx), req.SocietyId.String(), req.Params.IdempotencyKey, req.Body

	var importer, officer bool
	if err := s.db.QueryRow(ctx, `SELECT
		  EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		           WHERE m.user_id = $1 AND a.society_id = $2 AND a.post = $3
		             AND a.held_from <= auth_today() AND (a.held_until IS NULL OR a.held_until >= auth_today())),
		  person_holds_post($1, $2)`, caller, society, importerPost).Scan(&importer, &officer); err != nil {
		return nil, err
	}
	if !importer {
		if officer { // an officer here already knows the society exists
			return PostLedgerBatch403JSONResponse{Code: "not_permitted", Message: "importing opening balances is the secretary's"}, nil
		}
		return batchNotFound, nil // byte-identical to a society that does not exist
	}
	if b == nil {
		return PostLedgerBatch400JSONResponse{Code: "bad_request", Message: "a batch body is required"}, nil
	}

	var out PostLedgerBatchResponseObject
	err := pgx.BeginFunc(ctx, s.db, func(x pgx.Tx) error {
		if _, err := x.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('argon.batch:' || $1))`, society); err != nil {
			return err
		}
		if rec, ok, err := postedRecord(ctx, x, society, ref); err != nil || ok {
			out = rec
			return err
		}
		amounts, total, err := judgeBatch(ctx, x, society, b)
		if err != nil {
			return err
		}
		migration, savings, err := batchAccounts(ctx, x, society)
		if err != nil {
			return err
		}
		// The step-up (0005), only now that something is about to be
		// written, and inside this transaction: a repeat needs no proof, a
		// refusal never touches it, a failure rolls its spending back. The act
		// spends the proof; 0003 and 0005 refuse, by name, one that is missing,
		// someone else's, stale, spent, minted for another act, or minted here.
		if b.PinProofId == nil {
			return errStepUp
		}
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('ledger_batch', $1, $2) RETURNING id::text`,
			caller, b.PinProofId.String()).Scan(&act); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && stepUpRefusals[pg.ConstraintName] {
				return errStepUp
			}
			return err
		}
		var batch string
		if err := x.QueryRow(ctx, `INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`, society, ref, caller, len(b.Rows), total, act).Scan(&batch); err != nil {
			return err
		}
		var asOf any // NULL stays NULL: "accumulated before migration", never the import date
		if b.AsOf != nil {
			asOf = b.AsOf.Time.Format("2006-01-02")
		}
		for i, row := range b.Rows {
			var entry string
			if err := x.QueryRow(ctx, `INSERT INTO ledger_entries
				  (society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label, source_line)
				VALUES ($1, $2, 'opening_balance', $3, $4, $5, $6, $7) RETURNING id::text`,
				society, batch, asOf, caller, importerPost, importerLabel, row.Line).Scan(&entry); err != nil {
				return err
			}
			// D-01: Dr migration control, Cr the member's savings.
			if _, err := x.Exec(ctx, `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id)
				VALUES ($1, $2, $3, 'principal', NULL), ($1, $4, $5, 'principal', $6)`,
				entry, migration, amounts[i], savings, -amounts[i], row.MemberId.String()); err != nil {
				return err
			}
		}
		rec, _, err := postedRecord(ctx, x, society, ref)
		if err != nil {
			return err
		}
		created := rec.(PostLedgerBatch200JSONResponse)
		created.AlreadyPosted = false
		out = PostLedgerBatch201JSONResponse(created)
		return nil
	})

	var refusal *batchRefusal
	var pg *pgconn.PgError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &refusal) && refusal.status == 400:
		return PostLedgerBatch400JSONResponse{Code: "bad_request", Message: refusal.msg}, nil
	case errors.As(err, &refusal):
		return refusal.response(), nil
	case errors.Is(err, errStepUp):
		return PostLedgerBatch403JSONResponse{Code: "step_up_required",
			Message: "This import needs your PIN: a proof minted for it, in the last five minutes, not yet used. Nothing was posted."}, nil
	case errors.Is(err, errChart):
		return PostLedgerBatch409JSONResponse{Code: "chart_incomplete", Message: errChart.Error()}, nil
	case errors.As(err, &pg) && pg.ConstraintName == "entry_not_in_closed_period":
		return (&batchRefusal{code: "period_closed", msg: "the as-at date falls in a closed period. Nothing was posted; " +
			"a correction to a closed period is an adjusting entry in the current period"}).response(), nil
	case errors.As(err, &pg) && pg.ConstraintName == "ledger_batches_society_id_reference_key":
		// Only reachable if the lock above were bypassed. Answer with what committed.
		if rec, ok, rerr := postedRecord(ctx, s.db, society, ref); rerr == nil && ok {
			return rec, nil
		}
	}
	s.log.Error("postAll failed; nothing was written", "society", society, "reference", ref, "err", err)
	return PostLedgerBatch500JSONResponse{Code: "post_failed",
		Message: "Nothing was posted. Retrying with the same reference is safe: it has not been used."}, nil
}

func (r *batchRefusal) response() PostLedgerBatch422JSONResponse {
	out := PostLedgerBatch422JSONResponse{Code: BatchRefusalCode(r.code), Message: r.msg}
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

// magnitude parses a §0 "how much": a non-negative decimal kobo string.
// A sign, a point, an exponent or an empty string is refused, never coerced.
func magnitude(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// judgeBatch: every refusal, before anything is written. Inside the
// transaction and under the society's lock, so what it reads stays true.
func judgeBatch(ctx context.Context, x pgx.Tx, society string, b *LedgerBatch) ([]int64, int64, error) {
	if len(b.Rows) == 0 {
		return nil, 0, &batchRefusal{code: "nothing_to_post", msg: "No row in this batch to post."}
	}
	amounts := make([]int64, len(b.Rows))
	var total int64
	var zero []BatchRow
	for i, row := range b.Rows {
		n, ok := magnitude(string(row.AmountKobo))
		if !ok {
			// A negative is a member who OWES: a loan, not savings. A migration
			// must not create debt from a spreadsheet column.
			return nil, 0, &batchRefusal{status: 400, msg: fmt.Sprintf("line %d: amountKobo must be a non-negative decimal kobo string, got %q", row.Line, row.AmountKobo)}
		}
		if n == 0 { // means nothing; more likely a parsing artifact than an intention
			zero = append(zero, row)
		}
		amounts[i] = n
		total += n
	}
	if len(zero) > 0 {
		return nil, 0, &batchRefusal{code: "zero_amount", msg: "A ₦0 opening balance posts nothing. Remove the row, or correct its amount.", rows: zero}
	}
	confirmed, ok := magnitude(string(b.ConfirmedTotalKobo))
	if !ok {
		return nil, 0, &batchRefusal{status: 400, msg: fmt.Sprintf("confirmedTotalKobo must be a non-negative decimal kobo string, got %q", b.ConfirmedTotalKobo)}
	}
	if confirmed != total { // otherwise the secretary confirms one figure and another posts
		return nil, 0, &batchRefusal{code: "total_mismatch",
			msg: fmt.Sprintf("The rows total %d kobo but %d was confirmed. Nothing was posted. Preview the import again.", total, confirmed)}
	}
	if b.AsOf != nil {
		var future bool
		if err := x.QueryRow(ctx, `SELECT $1::date > auth_today()`, b.AsOf.Time.Format("2006-01-02")).Scan(&future); err != nil {
			return nil, 0, err
		}
		if future {
			return nil, 0, &batchRefusal{code: "as_of_in_future", msg: "The as-at date is after today. Balances cannot be accumulated to a date that has not happened."}
		}
	}

	seen := map[string]bool{}
	var dup, unknown, has []BatchRow
	for _, row := range b.Rows {
		id := row.MemberId.String()
		if seen[id] {
			dup = append(dup, row)
			continue
		}
		seen[id] = true
		var inSociety, carries bool
		if err := x.QueryRow(ctx, `SELECT
			  EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2),
			  EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id JOIN accounts a ON a.id = l.account_id
			           WHERE e.kind = 'opening_balance' AND e.reverses_id IS NULL AND l.member_id = $1 AND a.role = 'member_savings'
			             AND NOT EXISTS (SELECT 1 FROM ledger_entries r WHERE r.reverses_id = e.id))`,
			id, society).Scan(&inSociety, &carries); err != nil {
			return nil, 0, err
		}
		switch {
		case !inSociety: // one answer for another society's member and for none at all
			unknown = append(unknown, row)
		case carries:
			has = append(has, row)
		}
	}
	switch {
	case len(dup) > 0:
		return nil, 0, &batchRefusal{code: "duplicate_member", msg: "A member appears more than once in this file. Nothing was posted.", rows: dup}
	case len(unknown) > 0:
		return nil, 0, &batchRefusal{code: "unknown_member", msg: "No such member in this society. Nothing was posted.", rows: unknown}
	case len(has) > 0:
		return nil, 0, &batchRefusal{code: "member_already_has_balance",
			msg: "These members already carry an opening balance. Nothing was posted: skipping them would be a partial migration.", rows: has}
	}
	return amounts, total, nil
}

// batchAccounts: by ROLE (0004), and exactly one of each. Guessing which of
// two savings accounts to credit would be inventing where the money goes.
func batchAccounts(ctx context.Context, x pgx.Tx, society string) (migration, savings string, err error) {
	one := func(role string) (string, error) {
		rows, err := x.Query(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND role = $2`, society, role)
		if err != nil {
			return "", err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return "", err
		}
		if len(ids) != 1 {
			return "", errChart
		}
		return ids[0], nil
	}
	if migration, err = one("migration_control"); err != nil {
		return "", "", err
	}
	savings, err = one("member_savings")
	return migration, savings, err
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// postedRecord: what the commit under this reference did, READ FROM ITS
// ENTRIES. The as-at date included: 0002 dropped it from the batch, so the
// entries are the only record of it.
func postedRecord(ctx context.Context, q rowQuerier, society, ref string) (PostLedgerBatchResponseObject, bool, error) {
	var (
		postedAt    time.Time
		rows, dates int
		total       int64
		asOf        *time.Time
	)
	err := q.QueryRow(ctx, `SELECT b.posted_at, count(e.id), count(DISTINCT coalesce(e.value_date::text, 'null')),
		       coalesce(sum(l.amount_kobo), 0), max(e.value_date)
		  FROM ledger_batches b
		  LEFT JOIN ledger_entries e ON e.batch_id = b.id
		  LEFT JOIN ledger_lines l ON l.entry_id = e.id AND l.amount_kobo > 0
		 WHERE b.society_id = $1 AND b.reference = $2
		 GROUP BY b.id, b.posted_at`, society, ref).Scan(&postedAt, &rows, &dates, &total, &asOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if dates > 1 { // one batch, one as-at date: anything else is not this endpoint's write
		return nil, false, fmt.Errorf("batch %s carries %d different as-at dates", ref, dates)
	}
	rec := PostLedgerBatch200JSONResponse{Reference: ref, Rows: rows, TotalKobo: Magnitude(strconv.FormatInt(total, 10)),
		PostedAt: postedAt, AlreadyPosted: true}
	if asOf != nil {
		rec.AsOf = &openapi_types.Date{Time: *asOf}
	}
	return rec, true, nil
}
