//go:build tier1

// The reference for the reports (stage 2), DERIVED from internal/api/reports.go
// and breakable by name (ARGON_TEST_API_FAKE=<break>), for tier1_api_reports_test.go:
//
//	rep_period_ignored          the income statement counts entries after the period's end
//	rep_zero_rows_dropped       an income or expense account with nothing in the period left out
//	rep_income_negative         income read as debits (earned money shown negative)
//	rep_cash_opening_inclusive  the period's first day counted in the opening cash
//	rep_cash_out_as_in          money out shown as money in
//	rep_loans_at_from           loans outstanding at the period's start, not its end
//	rep_charges_swapped         the partner's charge and Argon's margin swapped
//	rep_any_reader              anyone signed in reads a society's reports
package tier1

import (
	"context"
	"log/slog"
	"strconv"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

func fb(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func frKobo(v int64) api.Kobo { return api.Kobo(strconv.FormatInt(v, 10)) }

var frNotFound = api.Problem{Code: "not_found", Message: "no such society"}

func fqLog(ctx context.Context, msg string, args ...any) { slog.Default().ErrorContext(ctx, msg, args...) }

func (f *fakeAPI) frMayRead(ctx context.Context, society string) (bool, error) {
	if f.brk == "rep_any_reader" {
		return true, nil
	}
	var ok bool
	err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $2) AND person_holds_post($1, $2)`,
		ctx.Value(ctxUser{}).(string), society).Scan(&ok)
	return ok, err
}

var frBadPeriod = api.Problem{Code: "bad_period", Message: "a period's first day is on or before its last"}

func frPeriodOf(from, to openapi_types.Date) (string, string, bool) {
	f, t := from.Time.Format("2006-01-02"), to.Time.Format("2006-01-02")
	return f, t, !from.Time.After(to.Time)
}

func (f *fakeAPI) GetIncomeStatement(ctx context.Context, req api.GetIncomeStatementRequestObject) (api.GetIncomeStatementResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.frMayRead(ctx, society); err != nil || !ok {
		return api.GetIncomeStatement404JSONResponse(frNotFound), err
	}
	from, to, ok := frPeriodOf(req.Params.From, req.Params.To)
	if !ok {
		return api.GetIncomeStatement400JSONResponse(frBadPeriod), nil
	}
	rows, err := f.db.Query(ctx, `
		SELECT a.code, a.name, a.kind::text, coalesce(sum(l.amount_kobo), 0)
		  FROM accounts a
		  LEFT JOIN (ledger_lines l JOIN ledger_entries e
		               ON e.id = l.entry_id AND e.value_date >= $2::date AND ($3::date IS NOT NULL OR true) AND (e.value_date <= $3::date OR `+fb(f.brk == "rep_period_ignored")+`))
		         ON l.account_id = a.id
		 WHERE a.society_id = $1 AND a.kind IN ('income', 'expense')
		 GROUP BY a.id, a.code, a.name, a.kind
		 ORDER BY a.code`, society, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.IncomeStatement{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, Income: []api.ReportLine{}, Expenses: []api.ReportLine{}}
	var income, expenses int64
	for rows.Next() {
		var code, name, kind string
		var sum int64
		if err := rows.Scan(&code, &name, &kind, &sum); err != nil {
			return nil, err
		}
		if sum == 0 && f.brk == "rep_zero_rows_dropped" {
			continue
		}
		if kind == "income" && f.brk == "rep_income_negative" {
			sum = -sum
		}
		if kind == "income" { // earned: credits, read positive
			income -= sum
			out.Income = append(out.Income, api.ReportLine{Code: code, Name: name, AmountKobo: frKobo(-sum)})
		} else { // spent: debits, positive
			expenses += sum
			out.Expenses = append(out.Expenses, api.ReportLine{Code: code, Name: name, AmountKobo: frKobo(sum)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.TotalIncomeKobo, out.TotalExpensesKobo, out.SurplusKobo = frKobo(income), frKobo(expenses), frKobo(income-expenses)
	return api.GetIncomeStatement200JSONResponse(out), nil
}

func (f *fakeAPI) GetCashFlow(ctx context.Context, req api.GetCashFlowRequestObject) (api.GetCashFlowResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.frMayRead(ctx, society); err != nil || !ok {
		return api.GetCashFlow404JSONResponse(frNotFound), err
	}
	from, to, ok := frPeriodOf(req.Params.From, req.Params.To)
	if !ok {
		return api.GetCashFlow400JSONResponse(frBadPeriod), nil
	}
	var opening, closing int64
	if err := f.db.QueryRow(ctx, `
		SELECT coalesce(sum(l.amount_kobo) FILTER (WHERE e.value_date IS NULL OR e.value_date < $2::date OR (`+fb(f.brk == "rep_cash_opening_inclusive")+` AND e.value_date = $2::date)), 0),
		       coalesce(sum(l.amount_kobo) FILTER (WHERE e.value_date IS NULL OR e.value_date <= $3::date), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE a.society_id = $1 AND a.role = 'bank'`, society, from, to).Scan(&opening, &closing); err != nil {
		return nil, err
	}
	// Each entry's net movement of cash, then by kind and direction.
	rows, err := f.db.Query(ctx, `
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
	out := api.CashFlow{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, Inflows: []api.CashFlowLine{}, Outflows: []api.CashFlowLine{}}
	var in, outflow int64
	for rows.Next() {
		var kind string
		var isIn bool
		var n int
		var amount int64
		if err := rows.Scan(&kind, &isIn, &n, &amount); err != nil {
			return nil, err
		}
		line := api.CashFlowLine{Kind: kind, Entries: n, AmountKobo: frKobo(amount)}
		if isIn || f.brk == "rep_cash_out_as_in" {
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
	if opening+in-outflow != closing && f.brk != "rep_cash_out_as_in" && f.brk != "rep_cash_opening_inclusive" {
		fqLog(ctx, "cash flow does not reconcile", "society", society, "from", from, "to", to,
			"opening", opening, "in", in, "out", outflow, "closing", closing)
		return api.GetCashFlow500JSONResponse(api.Problem{Code: "cash_does_not_reconcile", Message: "the society's cash does not reconcile: a defect, not a variance"}), nil
	}
	out.OpeningCashKobo, out.TotalInKobo, out.TotalOutKobo, out.ClosingCashKobo = frKobo(opening), frKobo(in), frKobo(outflow), frKobo(closing)
	return api.GetCashFlow200JSONResponse(out), nil
}

func (f *fakeAPI) GetPeriodReport(ctx context.Context, req api.GetPeriodReportRequestObject) (api.GetPeriodReportResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.frMayRead(ctx, society); err != nil || !ok {
		return api.GetPeriodReport404JSONResponse(frNotFound), err
	}
	from, to, ok := frPeriodOf(req.Params.From, req.Params.To)
	if !ok {
		return api.GetPeriodReport400JSONResponse(frBadPeriod), nil
	}
	out := api.PeriodReport{SocietyId: req.SocietyId, From: req.Params.From, To: req.Params.To, ContributionsByBranch: []api.BranchContributions{}}
	// Contributions to members' savings, by the member's branch; a reversal
	// is a contribution_in too, and nets.
	rows, err := f.db.Query(ctx, `
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
		var c api.BranchContributions
		var amount int64
		if err := rows.Scan(&c.BranchCode, &c.BranchName, &amount); err != nil {
			rows.Close()
			return nil, err
		}
		total += amount
		c.AmountKobo = frKobo(amount)
		out.ContributionsByBranch = append(out.ContributionsByBranch, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.TotalContributionsKobo = frKobo(total)
	var loans, partner, margin, vat, levy int64
	if err := f.db.QueryRow(ctx, `
		SELECT coalesce(sum(l.amount_kobo) FILTER (WHERE a.role = 'member_loan' AND (e.value_date IS NULL OR e.value_date <= (CASE WHEN `+fb(f.brk == "rep_loans_at_from")+` THEN $2::date ELSE $3::date END))), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'partner_charge' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'argon_margin' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'vat' AND e.value_date BETWEEN $2::date AND $3::date), 0),
		       coalesce(-sum(l.amount_kobo) FILTER (WHERE l.charge_role = 'statutory_levy' AND e.value_date BETWEEN $2::date AND $3::date), 0)
		  FROM ledger_lines l JOIN ledger_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		 WHERE a.society_id = $1`, society, from, to).Scan(&loans, &partner, &margin, &vat, &levy); err != nil {
		return nil, err
	}
	out.LoansOutstandingKobo = frKobo(loans)
	if f.brk == "rep_charges_swapped" {
		partner, margin = margin, partner
	}
	out.Charges = api.ChargesCollected{PartnerKobo: frKobo(partner), MarginKobo: frKobo(margin), VatKobo: frKobo(vat), LevyKobo: frKobo(levy),
		TotalKobo: frKobo(partner + margin + vat + levy)}
	return api.GetPeriodReport200JSONResponse(out), nil
}

