//go:build tier1

// THE MUTATION VEHICLE FOR THE LOAN READ TESTS — test code, NOT the API.
//
// A reference implementation of GET /loans/{id}, /members/{id}/loans and
// /societies/{id}/loans (LOAN_SHAPE.md §1, §5, §6), mounted through the
// generated router, breakable one named way per run (ARGON_TEST_API_FAKE).
//
// Breaks — who may read:
//
//	loans_borrower_refused         (over-strict) the borrower may not read their own loan
//	loans_auditor_refused          (over-strict) the internal auditor may not read
//	loans_any_member_reads         any member of the society reads any loan
//	loans_officer_ever             a post held at any time counts, not just now
//	loans_officer_any_society      an officer of ANY society reads
//	loans_not_found_says_why       a refusal says "not permitted"
//	loans_book_for_members         a member (no post) reads the society's loan book
//	loans_member_list_any          any member of the society reads another member's loans
//
// Breaks — the figures:
//
//	loans_outstanding_from_schedule  outstanding = principal − due to date (the schedule's story, not the ledger's)
//	loans_posting_basis              as-at compares the POSTING date, not the value date
//	loans_newest_first               repayments allocated to the NEWEST instalment first
//	loans_part_paid_not_missed       a past-due instalment partly paid is not counted missed
//	loans_reason_count_only          the arrears reason is a bare count ("1 missed")
//	loans_paid_ignored               arrears = everything due, payments ignored
//	loans_asof_optional              a missing asOf means today (wrapper, apifake_test.go)
//
// Breaks — the conditions:
//
//	loans_agreed_unchecked           "not yet disbursed" is not checked first: an undisbursed loan (balance zero) reads settled_early
//	loans_settled_reads_early        settled after the schedule ended reads settled_early
//	loans_early_reads_settled        settled before the schedule ended reads settled
//	loans_prepay_terms_ignored       repaid early against the product's terms reads settled_early
//	loans_underpaid_as_arrears       the schedule ended with money owed reads in_arrears, not a disagreement
//	loans_overpaid_clamped           an overpayment clamped to zero, reading settled
//	loans_condition_filter_ignored   ?condition= ignored
//
// Breaks — written off (suite loans_api_wo):
//
//	loans_written_off_reads_settled  a written-off loan (nothing left owing) reads settled
//	loans_write_off_reason_dropped   the sentence leaves out the reason the society gave
//	loans_write_off_ignores_date     a write-off counts whatever its value date
//	loans_recovery_as_repayment      money recovered is counted as a repayment of the loan
//
// Shared with the statement fake: money_as_numbers, nil_slices.
package tier1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

// ---- words: the reason is the server's sentence ---------------------------------

func naira(kobo int64) string {
	if kobo < 0 {
		kobo = -kobo
	}
	whole, frac := kobo/100, kobo%100
	s := strconv.FormatInt(whole, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("₦%s.%02d", b.String(), frac)
}

func longDate(d time.Time) string { return d.Format("2 January 2006") }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---- who may read ----------------------------------------------------------------

func (f *fakeAPI) currentOfficer(ctx context.Context, user, society string) bool {
	term := `AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`
	if f.brk == "loans_officer_ever" {
		term = ""
	}
	soc := `AND a.society_id = $2`
	if f.brk == "loans_officer_any_society" {
		soc = `AND $2::text IS NOT NULL`
	}
	aud := ""
	if f.brk == "loans_auditor_refused" {
		aud = `AND a.post <> 'internal_auditor'`
	}
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members am ON am.id = a.member_id
		WHERE am.user_id = $1 `+soc+` `+term+` `+aud+`)`, user, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) memberOfSociety(ctx context.Context, user, society string) bool {
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE user_id = $1 AND society_id = $2)`, user, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) loansRefusal(what string) api.Problem {
	if f.brk == "loans_not_found_says_why" {
		return api.Problem{Code: "not_permitted", Message: "you may not read this"}
	}
	return api.Problem{Code: "not_found", Message: "no such " + what}
}

// ---- the position ----------------------------------------------------------------

type fakeInst struct {
	n           int
	due         time.Time
	amount, got int64
}

func (f *fakeAPI) loanPosition(ctx context.Context, loanID string, asOf time.Time) (api.LoanPosition, error) {
	var (
		p                         api.LoanPosition
		lid, sid, mid, prepayName string
		principal                 int64
		term                      int
		prepay                    bool
		agreed                    time.Time
	)
	err := f.db.QueryRow(ctx, `SELECT l.id::text, l.society_id::text, l.member_id::text, p.code, p.name, l.principal_kobo,
		       l.term_months, l.purpose, l.agreed_at, l.allows_prepayment
		  FROM loans l JOIN loan_products p ON p.id = l.product_id WHERE l.id = $1`, loanID).
		Scan(&lid, &sid, &mid, &p.ProductCode, &p.ProductName, &principal, &term, &p.Purpose, &agreed, &prepay)
	if err != nil {
		return p, err
	}
	prepayName = p.ProductName
	day := asOf.Format("2006-01-02")
	p.LoanId, p.SocietyId, p.MemberId = uuidOf(lid), uuidOf(sid), uuidOf(mid)
	p.PrincipalKobo, p.TermMonths, p.AgreedAt = api.Magnitude(strconv.FormatInt(principal, 10)), term, agreed
	p.AsOf = openapi_types.Date{Time: asOf}

	var st *string
	f.db.QueryRow(ctx, `SELECT approval_status(r.id)::text FROM approval_requests r
		WHERE r.loan_id = $1 AND r.kind = 'loan_disbursement' ORDER BY r.raised_at DESC, r.id DESC LIMIT 1`, lid).Scan(&st)
	if st != nil {
		a := api.LoanPositionApprovalStatus(*st)
		p.ApprovalStatus = &a
	}

	// The ledger: the loan's own lines on member_loan, as at asOf.
	inDate := `e.value_date <= $2::date`
	if f.brk == "loans_posting_basis" {
		inDate = `e.posted_at::date <= $2::date`
	}
	var disbursed *time.Time
	f.db.QueryRow(ctx, `SELECT e.value_date FROM ledger_entries e WHERE e.loan_id = $1 AND e.kind = 'loan_disbursement' AND `+inDate,
		lid, day).Scan(&disbursed)
	p.DisbursedOn = dateOf(disbursed)
	var outstanding, paid int64
	f.db.QueryRow(ctx, `SELECT coalesce(sum(l.amount_kobo), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE e.kind = 'loan_repayment'), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE e.loan_id = $1 AND a.role = 'member_loan' AND `+inDate, lid, day).Scan(&outstanding, &paid)

	// The schedule, allocated oldest first.
	rows, err := f.db.Query(ctx, `SELECT n, due_on, amount_kobo FROM loan_instalments WHERE loan_id = $1 ORDER BY n`, lid)
	if err != nil {
		return p, err
	}
	var insts []fakeInst
	for rows.Next() {
		var in fakeInst
		if err := rows.Scan(&in.n, &in.due, &in.amount); err != nil {
			rows.Close()
			return p, err
		}
		insts = append(insts, in)
	}
	rows.Close()
	order := make([]int, len(insts))
	for i := range insts {
		order[i] = i
		if f.brk == "loans_newest_first" {
			order[i] = len(insts) - 1 - i
		}
	}
	remaining := paid
	for _, i := range order {
		take := min(insts[i].amount, remaining)
		insts[i].got, remaining = take, remaining-take
	}
	var due int64
	missed, notYetDue := []fakeInst{}, 0
	var lastDue time.Time
	p.Instalments = []api.LoanInstalmentPosition{}
	for _, in := range insts {
		state := "due"
		past := !in.due.After(asOf)
		switch {
		case in.got == in.amount:
			state = "paid"
		case past && (in.got == 0 || f.brk != "loans_part_paid_not_missed"):
			state = "missed"
		case past:
			state = "part_paid"
		case in.got > 0:
			state = "part_paid"
		}
		if past {
			due += in.amount
		} else {
			notYetDue++
		}
		if state == "missed" {
			missed = append(missed, in)
		}
		lastDue = in.due
		p.Instalments = append(p.Instalments, api.LoanInstalmentPosition{N: in.n, DueOn: openapi_types.Date{Time: in.due},
			AmountKobo: api.Magnitude(strconv.FormatInt(in.amount, 10)), PaidKobo: api.Magnitude(strconv.FormatInt(in.got, 10)),
			State: api.LoanInstalmentPositionState(state)})
	}
	if f.brk == "loans_outstanding_from_schedule" && disbursed != nil {
		outstanding = principal - due
	}
	arrears := max(due-paid, 0)
	if f.brk == "loans_paid_ignored" {
		arrears = due
	}
	if f.brk == "loans_overpaid_clamped" {
		outstanding = max(outstanding, 0)
	}
	p.OutstandingKobo = api.Kobo(strconv.FormatInt(outstanding, 10))
	p.DueToDateKobo = api.Magnitude(strconv.FormatInt(due, 10))
	p.PaidToDateKobo = api.Magnitude(strconv.FormatInt(paid, 10))
	p.ArrearsKobo = api.Magnitude(strconv.FormatInt(arrears, 10))
	p.MissedInstalments = len(missed)
	if len(missed) > 0 {
		p.DaysInArrears = int(asOf.Sub(missed[0].due).Hours() / 24)
	}

	// A write-off as at asOf (its value date), the society's reason, and money
	// recovered since (to recovery income, never to the loan).
	woDate := `e.value_date <= $2::date`
	if f.brk == "loans_write_off_ignores_date" {
		woDate = `$2::date IS NOT NULL`
	}
	var writtenOn *time.Time
	var writtenOff int64
	f.db.QueryRow(ctx, `SELECT e.value_date, -coalesce((SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		       WHERE l.entry_id = e.id AND a.role = 'member_loan'), 0)
		  FROM ledger_entries e WHERE e.loan_id = $1 AND e.kind::text = 'loan_write_off' AND `+woDate, lid, day).Scan(&writtenOn, &writtenOff)
	var woReason string
	f.db.QueryRow(ctx, `SELECT coalesce(reason, '') FROM approval_requests WHERE loan_id = $1 AND kind::text = 'loan_write_off'
		AND approval_status(id) = 'approved' ORDER BY raised_at DESC LIMIT 1`, lid).Scan(&woReason)
	var recovered int64
	f.db.QueryRow(ctx, `SELECT -coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id
		JOIN accounts a ON a.id = l.account_id
		WHERE e.loan_id = $1 AND e.kind::text = 'loan_recovery' AND a.role = 'loan_recovery_income' AND e.value_date <= $2::date`, lid, day).Scan(&recovered)
	if f.brk == "loans_recovery_as_repayment" {
		paid += recovered
		outstanding -= recovered
		p.PaidToDateKobo = api.Magnitude(strconv.FormatInt(paid, 10))
		p.OutstandingKobo = api.Kobo(strconv.FormatInt(outstanding, 10))
		recovered = 0
	}

	// The condition, in this order, and its sentence.
	ended := !lastDue.After(asOf)
	cond, kind := "", ""
	switch {
	case disbursed == nil && f.brk != "loans_agreed_unchecked":
		cond = "agreed"
		if st == nil {
			p.Reason = fmt.Sprintf("Agreed on %s; not yet disbursed. No disbursement request has been raised.", longDate(agreed))
		} else {
			p.Reason = fmt.Sprintf("Agreed on %s; not yet disbursed. The disbursement request is %s.", longDate(agreed), *st)
		}
	case writtenOn != nil && f.brk != "loans_written_off_reads_settled":
		cond = "written_off"
		p.Reason = fmt.Sprintf("Written off on %s: %s was not recovered.", longDate(*writtenOn), naira(writtenOff))
		if woReason != "" && f.brk != "loans_write_off_reason_dropped" {
			p.Reason += " " + strings.TrimRight(woReason, ".") + "."
		}
		if recovered > 0 {
			p.Reason += fmt.Sprintf(" Since recovered: %s.", naira(recovered))
		}
	case outstanding < 0:
		cond, kind = "disagreement", "overpaid"
		p.Reason = fmt.Sprintf("Disagreement: the ledger shows %s repaid beyond the loan, owed to the member. An officer must refund or correct it; the loan is never edited to agree.", naira(-outstanding))
	case outstanding == 0 && (ended || f.brk == "loans_early_reads_settled") && f.brk != "loans_settled_reads_early":
		cond = "settled"
		p.Reason = "Settled: the schedule is complete and nothing is owed."
	case outstanding == 0 && (prepay || f.brk == "loans_prepay_terms_ignored"):
		cond = "settled_early"
		p.Reason = fmt.Sprintf("Settled early: nothing is owed and %s not yet due.", plural(notYetDue, "instalment was", "instalments were"))
	case outstanding == 0:
		cond, kind = "disagreement", "prepaid_against_terms"
		p.Reason = fmt.Sprintf("Disagreement: nothing is owed, but %s does not allow early repayment and %s not yet due. An officer must establish how it was repaid.",
			prepayName, plural(notYetDue, "instalment was", "instalments were"))
	case ended && f.brk != "loans_underpaid_as_arrears":
		cond, kind = "disagreement", "underpaid"
		p.Reason = fmt.Sprintf("Disagreement: the schedule ended on %s, but the ledger shows %s still owed. An officer must establish why; the loan is never edited to agree.",
			longDate(lastDue), naira(outstanding))
	case arrears > 0:
		cond = "in_arrears"
		if f.brk == "loans_reason_count_only" {
			p.Reason = fmt.Sprintf("%d missed", len(missed))
		} else {
			parts := []string{}
			for _, m := range missed {
				if m.got == 0 {
					parts = append(parts, fmt.Sprintf("%s due %s, unpaid", naira(m.amount), longDate(m.due)))
				} else {
					parts = append(parts, fmt.Sprintf("%s due %s, %s paid", naira(m.amount), longDate(m.due), naira(m.got)))
				}
			}
			p.Reason = fmt.Sprintf("%s: %s.", plural(len(missed), "instalment missed", "instalments missed"), strings.Join(parts, "; "))
		}
	default:
		cond = "active"
		p.Reason = "On schedule."
		for _, in := range insts {
			if in.got < in.amount {
				p.Reason = fmt.Sprintf("On schedule. Next instalment: %s due %s.", naira(in.amount-in.got), longDate(in.due))
				break
			}
		}
	}
	p.Condition = api.LoanCondition(cond)
	if kind != "" {
		p.Disagreement = &api.LoanDisagreement{Kind: api.LoanDisagreementKind(kind)}
	}
	if f.brk == "nil_slices" && len(p.Instalments) == 0 {
		p.Instalments = nil
	}
	return p, nil
}

func (f *fakeAPI) loanBook(ctx context.Context, where string, arg string, asOf time.Time, cond *api.LoanCondition) (api.LoanBook, error) {
	book := api.LoanBook{AsOf: openapi_types.Date{Time: asOf}, Loans: []api.LoanPosition{}}
	if f.brk == "nil_slices" {
		book.Loans = nil
	}
	rows, err := f.db.Query(ctx, `SELECT id::text FROM loans WHERE `+where+` ORDER BY agreed_at, id`, arg)
	if err != nil {
		return book, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		p, err := f.loanPosition(ctx, id, asOf)
		if err != nil {
			return book, err
		}
		if cond != nil && f.brk != "loans_condition_filter_ignored" && p.Condition != *cond {
			continue
		}
		book.Loans = append(book.Loans, p)
	}
	return book, nil
}

func (f *fakeAPI) GetLoan(ctx context.Context, req api.GetLoanRequestObject) (api.GetLoanResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	var society string
	var borrower *string
	err := f.db.QueryRow(ctx, `SELECT l.society_id::text, m.user_id::text FROM loans l JOIN members m ON m.id = l.member_id WHERE l.id = $1`,
		req.LoanId.String()).Scan(&society, &borrower)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.GetLoan404JSONResponse(api.Problem{Code: "not_found", Message: "no such loan"}), nil
	}
	if err != nil {
		return nil, err
	}
	ok := (borrower != nil && *borrower == caller && f.brk != "loans_borrower_refused") || f.currentOfficer(ctx, caller, society) ||
		(f.brk == "loans_any_member_reads" && f.memberOfSociety(ctx, caller, society))
	if !ok {
		return api.GetLoan404JSONResponse(f.loansRefusal("loan")), nil
	}
	p, err := f.loanPosition(ctx, req.LoanId.String(), req.Params.AsOf.Time)
	if err != nil {
		return nil, err
	}
	return api.GetLoan200JSONResponse(p), nil
}

func (f *fakeAPI) GetMemberLoans(ctx context.Context, req api.GetMemberLoansRequestObject) (api.GetMemberLoansResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	var society string
	var user *string
	err := f.db.QueryRow(ctx, `SELECT society_id::text, user_id::text FROM members WHERE id = $1`, req.MemberId.String()).Scan(&society, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.GetMemberLoans404JSONResponse(api.Problem{Code: "not_found", Message: "no such member"}), nil
	}
	if err != nil {
		return nil, err
	}
	ok := (user != nil && *user == caller) || f.currentOfficer(ctx, caller, society) ||
		(f.brk == "loans_member_list_any" && f.memberOfSociety(ctx, caller, society))
	if !ok {
		return api.GetMemberLoans404JSONResponse(f.loansRefusal("member")), nil
	}
	book, err := f.loanBook(ctx, `member_id = $1`, req.MemberId.String(), req.Params.AsOf.Time, nil)
	if err != nil {
		return nil, err
	}
	return api.GetMemberLoans200JSONResponse(book), nil
}

func (f *fakeAPI) GetSocietyLoans(ctx context.Context, req api.GetSocietyLoansRequestObject) (api.GetSocietyLoansResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	var exists bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&exists)
	ok := exists && (f.currentOfficer(ctx, caller, society) || (f.brk == "loans_book_for_members" && f.memberOfSociety(ctx, caller, society)))
	if !ok {
		if exists {
			return api.GetSocietyLoans404JSONResponse(f.loansRefusal("society")), nil
		}
		return api.GetSocietyLoans404JSONResponse(api.Problem{Code: "not_found", Message: "no such society"}), nil
	}
	book, err := f.loanBook(ctx, `society_id = $1`, society, req.Params.AsOf.Time, req.Params.Condition)
	if err != nil {
		return nil, err
	}
	return api.GetSocietyLoans200JSONResponse(book), nil
}
