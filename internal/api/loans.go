package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// THE LOAN READ SIDE (LOAN_SHAPE.md §1, §5, §6). Written against the approved
// acceptance tests, test/tier1/tier1_api_loans_test.go.
//
//   - What is OWED comes from the LEDGER: the loan's own lines on member_loan,
//     by VALUE DATE, as at asOf. Nothing is stored on the loan.
//   - Repayments are allocated to the OLDEST instalment first.
//   - The reason is the server's sentence; clients render it as given.
//   - Disagreement is a condition, not an error: the loan is never edited to
//     agree with the ledger.
//   - Readers: the borrower, or an officer of the loan's society holding a
//     current post (the auditor included). Anyone else: 404, identical to
//     nothing being there.

func naira(kobo int64) string {
	if kobo < 0 {
		kobo = -kobo
	}
	s := strconv.FormatInt(kobo/100, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("₦%s.%02d", b.String(), kobo%100)
}

func longDate(d time.Time) string { return d.Format("2 January 2006") }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func magnitudeOf(v int64) Magnitude { return Magnitude(strconv.FormatInt(v, 10)) }

type instalment struct {
	n           int
	due         time.Time
	amount, got int64
}

// loanPosition derives one loan's position as at asOf.
func (s *server) loanPosition(ctx context.Context, loanID string, asOf time.Time) (LoanPosition, error) {
	var (
		p             LoanPosition
		lid, sid, mid string
		principal     int64
		term          int
		prepay        bool
	)
	if err := s.db.QueryRow(ctx, `SELECT l.id::text, l.society_id::text, l.member_id::text, p.code, p.name, l.principal_kobo,
		       l.term_months, l.purpose, l.agreed_at, l.allows_prepayment
		  FROM loans l JOIN loan_products p ON p.id = l.product_id WHERE l.id = $1`, loanID).
		Scan(&lid, &sid, &mid, &p.ProductCode, &p.ProductName, &principal, &term, &p.Purpose, &p.AgreedAt, &prepay); err != nil {
		return p, err
	}
	day := asOf.Format("2006-01-02")
	p.LoanId, p.SocietyId, p.MemberId = uuidOf(lid), uuidOf(sid), uuidOf(mid)
	p.PrincipalKobo, p.TermMonths, p.AsOf = magnitudeOf(principal), term, openapi_types.Date{Time: asOf}

	// The disbursement request's approval status now (an approval is not value-dated).
	var status *string
	if err := s.db.QueryRow(ctx, `SELECT approval_status(r.id)::text FROM approval_requests r
		WHERE r.loan_id = $1 AND r.kind = 'loan_disbursement' ORDER BY r.raised_at DESC, r.id DESC LIMIT 1`, lid).
		Scan(&status); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	if status != nil {
		a := LoanPositionApprovalStatus(*status)
		p.ApprovalStatus = &a
	}

	// The ledger, as at asOf by value date.
	var disbursed *time.Time
	if err := s.db.QueryRow(ctx, `SELECT value_date FROM ledger_entries
		WHERE loan_id = $1 AND kind = 'loan_disbursement' AND value_date <= $2::date`, lid, day).
		Scan(&disbursed); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	p.DisbursedOn = dateOf(disbursed)
	var outstanding, paid int64
	if err := s.db.QueryRow(ctx, `SELECT coalesce(sum(l.amount_kobo), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE e.kind = 'loan_repayment'), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE e.loan_id = $1 AND a.role = 'member_loan' AND e.value_date <= $2::date`, lid, day).
		Scan(&outstanding, &paid); err != nil {
		return p, err
	}

	// The schedule, repayments allocated oldest first.
	rows, err := s.db.Query(ctx, `SELECT n, due_on, amount_kobo FROM loan_instalments WHERE loan_id = $1 ORDER BY n`, lid)
	if err != nil {
		return p, err
	}
	var insts []instalment
	for rows.Next() {
		var in instalment
		if err := rows.Scan(&in.n, &in.due, &in.amount); err != nil {
			rows.Close()
			return p, err
		}
		insts = append(insts, in)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return p, err
	}
	remaining := paid
	for i := range insts {
		take := min(insts[i].amount, remaining)
		insts[i].got, remaining = take, remaining-take
	}

	var due int64
	var missed []instalment
	notYetDue := 0
	var lastDue time.Time
	p.Instalments = []LoanInstalmentPosition{}
	for _, in := range insts {
		past := !in.due.After(asOf)
		state := "due"
		switch {
		case in.got == in.amount:
			state = "paid"
		case past:
			state = "missed"
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
		p.Instalments = append(p.Instalments, LoanInstalmentPosition{N: in.n, DueOn: openapi_types.Date{Time: in.due},
			AmountKobo: magnitudeOf(in.amount), PaidKobo: magnitudeOf(in.got), State: LoanInstalmentPositionState(state)})
	}
	arrears := max(due-paid, 0)
	p.OutstandingKobo = Kobo(strconv.FormatInt(outstanding, 10))
	p.DueToDateKobo, p.PaidToDateKobo, p.ArrearsKobo = magnitudeOf(due), magnitudeOf(paid), magnitudeOf(arrears)
	p.MissedInstalments = len(missed)
	if len(missed) > 0 {
		p.DaysInArrears = int(asOf.Sub(missed[0].due).Hours() / 24)
	}

	// The condition, in this order: "not yet disbursed" first, because an
	// undisbursed loan also has a zero balance.
	ended := !lastDue.After(asOf)
	cond, kind := "", ""
	switch {
	case disbursed == nil:
		cond = "agreed"
		if status == nil {
			p.Reason = fmt.Sprintf("Agreed on %s; not yet disbursed. No disbursement request has been raised.", longDate(p.AgreedAt))
		} else {
			p.Reason = fmt.Sprintf("Agreed on %s; not yet disbursed. The disbursement request is %s.", longDate(p.AgreedAt), *status)
		}
	case outstanding < 0:
		cond, kind = "disagreement", "overpaid"
		p.Reason = fmt.Sprintf("Disagreement: the ledger shows %s repaid beyond the loan, owed to the member. An officer must refund or correct it; the loan is never edited to agree.", naira(outstanding))
	case outstanding == 0 && ended:
		cond = "settled"
		p.Reason = "Settled: the schedule is complete and nothing is owed."
	case outstanding == 0 && prepay:
		cond = "settled_early"
		p.Reason = fmt.Sprintf("Settled early: nothing is owed and %s not yet due.", plural(notYetDue, "instalment was", "instalments were"))
	case outstanding == 0:
		cond, kind = "disagreement", "prepaid_against_terms"
		p.Reason = fmt.Sprintf("Disagreement: nothing is owed, but %s does not allow early repayment and %s not yet due. An officer must establish how it was repaid.",
			p.ProductName, plural(notYetDue, "instalment was", "instalments were"))
	case ended:
		cond, kind = "disagreement", "underpaid"
		p.Reason = fmt.Sprintf("Disagreement: the schedule ended on %s, but the ledger shows %s still owed. An officer must establish why; the loan is never edited to agree.",
			longDate(lastDue), naira(outstanding))
	case arrears > 0:
		cond = "in_arrears"
		parts := []string{}
		for _, m := range missed {
			if m.got == 0 {
				parts = append(parts, fmt.Sprintf("%s due %s, unpaid", naira(m.amount), longDate(m.due)))
			} else {
				parts = append(parts, fmt.Sprintf("%s due %s, %s paid", naira(m.amount), longDate(m.due), naira(m.got)))
			}
		}
		p.Reason = fmt.Sprintf("%s: %s.", plural(len(missed), "instalment missed", "instalments missed"), strings.Join(parts, "; "))
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
	p.Condition = LoanCondition(cond)
	if kind != "" {
		p.Disagreement = &LoanDisagreement{Kind: LoanDisagreementKind(kind)}
	}
	return p, nil
}

// loanBook: the positions of the loans `where` selects, in order of agreement.
func (s *server) loanBook(ctx context.Context, where, arg string, asOf time.Time, cond *LoanCondition) (LoanBook, error) {
	book := LoanBook{AsOf: openapi_types.Date{Time: asOf}, Loans: []LoanPosition{}}
	rows, err := s.db.Query(ctx, `SELECT id::text FROM loans WHERE `+where+` ORDER BY agreed_at, id`, arg)
	if err != nil {
		return book, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return book, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return book, err
	}
	for _, id := range ids {
		p, err := s.loanPosition(ctx, id, asOf)
		if err != nil {
			return book, err
		}
		if cond != nil && p.Condition != *cond {
			continue
		}
		book.Loans = append(book.Loans, p)
	}
	return book, nil
}

func (s *server) GetLoan(ctx context.Context, req GetLoanRequestObject) (GetLoanResponseObject, error) {
	notFound := GetLoan404JSONResponse(Problem{Code: "not_found", Message: "no such loan"})
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT m.user_id = $2 OR person_holds_post($2, l.society_id)
		FROM loans l JOIN members m ON m.id = l.member_id WHERE l.id = $1`, req.LoanId.String(), callerOf(ctx)).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ok) {
		return notFound, nil
	}
	if err != nil {
		return nil, err
	}
	p, err := s.loanPosition(ctx, req.LoanId.String(), req.Params.AsOf.Time)
	if err != nil {
		return nil, err
	}
	return GetLoan200JSONResponse(p), nil
}

func (s *server) GetMemberLoans(ctx context.Context, req GetMemberLoansRequestObject) (GetMemberLoansResponseObject, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT coalesce(user_id = $2, false) OR person_holds_post($2, society_id) FROM members WHERE id = $1`,
		req.MemberId.String(), callerOf(ctx)).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ok) {
		return GetMemberLoans404JSONResponse(Problem{Code: "not_found", Message: "no such member"}), nil
	}
	if err != nil {
		return nil, err
	}
	book, err := s.loanBook(ctx, `member_id = $1`, req.MemberId.String(), req.Params.AsOf.Time, nil)
	if err != nil {
		return nil, err
	}
	return GetMemberLoans200JSONResponse(book), nil
}

func (s *server) GetSocietyLoans(ctx context.Context, req GetSocietyLoansRequestObject) (GetSocietyLoansResponseObject, error) {
	if ok, err := s.mayReadBooks(ctx, req.SocietyId.String()); err != nil || !ok {
		return GetSocietyLoans404JSONResponse(booksNotFound), err
	}
	book, err := s.loanBook(ctx, `society_id = $1`, req.SocietyId.String(), req.Params.AsOf.Time, req.Params.Condition)
	if err != nil {
		return nil, err
	}
	return GetSocietyLoans200JSONResponse(book), nil
}
