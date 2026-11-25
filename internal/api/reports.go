package api

// REPORTS (stage 2, 2026-11-25): the income statement, the cash flow (the
// direct method), and the period report LEDGER_CONTRACT §4 names
// (contributions by branch, loans outstanding, charges collected). Every one
// is a read of the entries by VALUE DATE (§4): an undated entry is before
// every date, so it is in no period and in every opening figure. Readable by
// whoever reads the books (mayReadBooks). A report that would not reconcile
// is refused as a defect (500), never shown with a variance.

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

var badPeriod = Problem{Code: "bad_period", Message: "a period's first day is on or before its last"}

func periodOf(from, to openapi_types.Date) (string, string, bool) {
	f, t := from.Time.Format("2006-01-02"), to.Time.Format("2006-01-02")
	return f, t, !from.Time.After(to.Time)
}

func (s *server) GetIncomeStatement(ctx context.Context, req GetIncomeStatementRequestObject) (GetIncomeStatementResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.mayReadBooks(ctx, society); err != nil || !ok {
		return GetIncomeStatement404JSONResponse(booksNotFound), err
	}
	from, to, ok := periodOf(req.Params.From, req.Params.To)
	if !ok {
		return GetIncomeStatement400JSONResponse(badPeriod), nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.code, a.name, a.kind::text, coalesce(sum(l.amount_kobo), 0)
		  FROM accounts a
		  LEFT JOIN (ledger_lines l JOIN ledger_entries e
		               ON e.id = l.entry_id AND e.value_date BETWEEN $2::date AND $3::date)
		         ON l.account_id = a.id
		 WHERE a.society_id = $1 AND a.kind IN ('income', 'expense')
		 GROUP BY a.id, a.code, a.name, a.kind
		 ORDER BY a.code`, society, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := IncomeStatement{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, Income: []ReportLine{}, Expenses: []ReportLine{}}
	var income, expenses int64
	for rows.Next() {
		var code, name, kind string
		var sum int64
		if err := rows.Scan(&code, &name, &kind, &sum); err != nil {
			return nil, err
		}
		if kind == "income" { // earned: credits, read positive
			income -= sum
			out.Income = append(out.Income, ReportLine{Code: code, Name: name, AmountKobo: koboOf(-sum)})
		} else { // spent: debits, positive
			expenses += sum
			out.Expenses = append(out.Expenses, ReportLine{Code: code, Name: name, AmountKobo: koboOf(sum)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.TotalIncomeKobo, out.TotalExpensesKobo, out.SurplusKobo = koboOf(income), koboOf(expenses), koboOf(income-expenses)
	return GetIncomeStatement200JSONResponse(out), nil
}

func (s *server) GetCashFlow(ctx context.Context, req GetCashFlowRequestObject) (GetCashFlowResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.mayReadBooks(ctx, society); err != nil || !ok {
		return GetCashFlow404JSONResponse(booksNotFound), err
	}
	from, to, ok := periodOf(req.Params.From, req.Params.To)
	if !ok {
		return GetCashFlow400JSONResponse(badPeriod), nil
	}
	var opening, closing int64
	if err := s.db.QueryRow(ctx, `
		SELECT coalesce(sum(l.amount_kobo) FILTER (WHERE e.value_date IS NULL OR e.value_date < $2::date), 0),
		       coalesce(sum(l.amount_kobo) FILTER (WHERE e.value_date IS NULL OR e.value_date <= $3::date), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE a.society_id = $1 AND a.role = 'bank'`, society, from, to).Scan(&opening, &closing); err != nil {
		return nil, err
	}
	// Each entry's net movement of cash, then by kind and direction.
	rows, err := s.db.Query(ctx, `
		SELECT kind, net > 0, count(*), sum(abs(net))
		  FROM (SELECT e.kind::text AS kind, sum(l.amount_kobo) AS net
		          FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		         WHERE a.society_id = $1 AND a.role = 'bank' AND e.value_date BETWEEN $2::date AND $3::date
		         GROUP BY e.id, e.kind) m
		 WHERE net <> 0
		 GROUP BY kind, net > 0
		 ORDER BY kind`, society, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := CashFlow{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, Inflows: []CashFlowLine{}, Outflows: []CashFlowLine{}}
	var in, outflow int64
	for rows.Next() {
		var kind string
		var isIn bool
		var n int
		var amount int64
		if err := rows.Scan(&kind, &isIn, &n, &amount); err != nil {
			return nil, err
		}
		line := CashFlowLine{Kind: kind, Entries: n, AmountKobo: koboOf(amount)}
		if isIn {
			in += amount
			out.Inflows = append(out.Inflows, line)
		} else {
			outflow += amount
			out.Outflows = append(out.Outflows, line)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if opening+in-outflow != closing {
		s.log.ErrorContext(ctx, "cash flow does not reconcile", "society", society, "from", from, "to", to,
			"opening", opening, "in", in, "out", outflow, "closing", closing)
		return GetCashFlow500JSONResponse(Problem{Code: "cash_does_not_reconcile", Message: "the society's cash does not reconcile: a defect, not a variance"}), nil
	}
	out.OpeningCashKobo, out.TotalInKobo, out.TotalOutKobo, out.ClosingCashKobo = koboOf(opening), koboOf(in), koboOf(outflow), koboOf(closing)
	return GetCashFlow200JSONResponse(out), nil
}

func (s *server) GetPeriodReport(ctx context.Context, req GetPeriodReportRequestObject) (GetPeriodReportResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.mayReadBooks(ctx, society); err != nil || !ok {
		return GetPeriodReport404JSONResponse(booksNotFound), err
	}
	from, to, ok := periodOf(req.Params.From, req.Params.To)
	if !ok {
		return GetPeriodReport400JSONResponse(badPeriod), nil
	}
	out := PeriodReport{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, ContributionsByBranch: []BranchContributions{}}
	// Contributions to members' savings, by the member's branch; a reversal
	// is a contribution_in too, and nets.
	rows, err := s.db.Query(ctx, `
		SELECT b.code, b.name, coalesce(-sum(l.amount_kobo), 0)
		  FROM branches b
		  LEFT JOIN members m ON m.branch_id = b.id
		  LEFT JOIN (ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id AND e.kind = 'contribution_in'
		               AND e.value_date BETWEEN $2::date AND $3::date
		             JOIN accounts a ON a.id = l.account_id AND a.role = 'member_savings')
		         ON l.member_id = m.id
		 WHERE b.society_id = $1
		 GROUP BY b.id, b.code, b.name
		 ORDER BY b.code`, society, from, to)
	if err != nil {
		return nil, err
	}
	var total int64
	for rows.Next() {
		var c BranchContributions
		var amount int64
		if err := rows.Scan(&c.BranchCode, &c.BranchName, &amount); err != nil {
			rows.Close()
			return nil, err
		}
		total += amount
		c.AmountKobo = koboOf(amount)
		out.ContributionsByBranch = append(out.ContributionsByBranch, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.TotalContributionsKobo = koboOf(total)
	var loans, partner, margin, vat, levy int64
	if err := s.db.QueryRow(ctx, `
		SELECT coalesce(sum(l.amount_kobo) FILTER (WHERE a.role = 'member_loan' AND (e.value_date IS NULL OR e.value_date <= $3::date)), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'partner_charge' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'argon_margin' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'vat' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'statutory_levy' AND e.value_date BETWEEN $2::date AND $3::date), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE a.society_id = $1`, society, from, to).Scan(&loans, &partner, &margin, &vat, &levy); err != nil {
		return nil, err
	}
	out.LoansOutstandingKobo = koboOf(loans)
	out.Charges = ChargesCollected{PartnerKobo: koboOf(partner), MarginKobo: koboOf(margin), VatKobo: koboOf(vat), LevyKobo: koboOf(levy),
		TotalKobo: koboOf(partner + margin + vat + levy)}
	return GetPeriodReport200JSONResponse(out), nil
}

var _ = time.Time{}
