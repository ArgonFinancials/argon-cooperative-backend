//go:build tier1

// THE MUTATION VEHICLE FOR THE WITHDRAWABLE FIGURE AND THE QUOTE — test code,
// NOT the API.
//
// A reference implementation of GET /members/{id}/withdrawable and
// GET /members/{id}/withdrawal-quote, breakable one named way per run. The
// figure is a READ the API composes (with the reasons only it can word); the
// enforcement is the database's (0012's hold, 0016's raise), and test 07 holds
// the two together: the figure shown is the figure enforced.
//
// Breaks:
//
//	bal_floor_ignored             the floor is neither deducted nor listed
//	bal_guarantee_unexplained     a guarantee is deducted, but says only "held", naming nobody
//	bal_guarantee_ignored         guarantees hold nothing back
//	bal_repaid_listed             a loan repaid in full is listed, holding ₦0
//	bal_floor_unset_zero          no floor: the figure reads 0, as if the member had nothing
//	bal_floor_unset_hides_savings no floor: the whole answer is refused, savings with it
//	bal_holds_ignored             authorised, unpaid payouts are not spoken for
//	bal_negative                  the figure goes below zero
//	bal_quote_ignores_charges     a quote "fits" when the amount alone fits, its charge forgotten
//	bal_anyone_reads              any member of the society reads another's savings
//	bal_officer_refused           (over-strict) an officer of the society may not read a member's figure
//	bal_quote_binding             a quote presents itself as the charge
//	bal_quote_stale               a quote prices on the series in force long ago, not today's
//	bal_final_charge_hidden       the hold's charge is never shown on the request
//	bal_charge_shown_early        a charge is shown on the request before any hold fixes it
package tier1

import (
	"context"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

const quoteNote = "This quote is indicative: the charge is fixed when the payout is authorised, from your society's charges that day, and you will be shown it then."

func magOf(n int64) api.Magnitude { return api.Magnitude(strconv.FormatInt(n, 10)) }


// balanceReader: may the caller read this member's figure? The member, or an
// officer of the member's society. Returns the member's society.
func (f *fakeAPI) balanceReader(ctx context.Context, caller, member string) (string, bool) {
	var society string
	f.db.QueryRow(ctx, `SELECT society_id::text FROM members WHERE id = $1`, member).Scan(&society)
	if society == "" {
		return "", false
	}
	if f.beneficiaryOf(ctx, caller, member) {
		return society, true
	}
	if f.officerHere(ctx, caller, society) {
		return society, f.brk != "bal_officer_refused"
	}
	return society, f.brk == "bal_anyone_reads" && f.memberOfSociety(ctx, caller, society)
}

type position struct {
	savings    int64
	floor      *int64
	deductions []api.Deduction
	total      int64
}

func (f *fakeAPI) positionOf(ctx context.Context, society, member string) (position, error) {
	var p position
	if err := f.db.QueryRow(ctx, `SELECT coalesce(-sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		WHERE a.role = 'member_savings' AND l.member_id = $1`, member).Scan(&p.savings); err != nil {
		return p, err
	}
	if err := f.db.QueryRow(ctx, `SELECT account_floor_kobo FROM societies WHERE id = $1`, society).Scan(&p.floor); err != nil {
		return p, err
	}
	add := func(d api.Deduction, n int64) { p.deductions = append(p.deductions, d); p.total += n }
	if p.floor != nil && f.brk != "bal_floor_ignored" {
		add(api.Deduction{Kind: "floor", AmountKobo: magOf(*p.floor),
			Reason: "Your society's minimum balance: every member keeps " + naira(*p.floor) + " saved."}, *p.floor)
	}
	if f.brk != "bal_guarantee_ignored" {
		rows, err := f.db.Query(ctx, `SELECT g.loan_id::text, u.full_name, greatest(loan_owed(g.loan_id), 0)
			FROM loan_guarantees g JOIN loans lo ON lo.id = g.loan_id JOIN members bm ON bm.id = lo.member_id JOIN users u ON u.id = bm.user_id
			WHERE (g.guarantor_member_id = $1 OR g.guarantor_user_id = (SELECT user_id FROM members WHERE id = $1))
			  AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals gw WHERE gw.guarantee_id = g.id)
			ORDER BY g.loan_id`, member)
		if err != nil {
			return p, err
		}
		type g struct {
			loan, borrower string
			owed           int64
		}
		var gs []g
		for rows.Next() {
			var x g
			if err := rows.Scan(&x.loan, &x.borrower, &x.owed); err != nil {
				rows.Close()
				return p, err
			}
			gs = append(gs, x)
		}
		rows.Close()
		for _, x := range gs {
			if x.owed == 0 && f.brk != "bal_repaid_listed" {
				continue
			}
			loan, who := uuidOf(x.loan), x.borrower
			reason := "Held while you guarantee " + x.borrower + "'s loan: the " + naira(x.owed) + " still owed on it is held in full, until your society decides how much a guarantor answers for."
			if f.brk == "bal_guarantee_unexplained" {
				reason, who = "Held.", ""
			}
			d := api.Deduction{Kind: "guarantee", AmountKobo: magOf(x.owed), Reason: reason, LoanId: &loan}
			if who != "" {
				d.BorrowerName = &who
			}
			add(d, x.owed)
		}
	}
	if f.brk != "bal_holds_ignored" {
		var open int64
		if err := f.db.QueryRow(ctx, `SELECT coalesce(sum(h.amount_kobo + h.partner_fee_kobo + h.vat_kobo + h.statutory_levy_kobo + h.argon_margin_kobo), 0)
			FROM withdrawal_holds h WHERE h.member_id = $1
			  AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
			  AND NOT EXISTS (SELECT 1 FROM ledger_entries le WHERE le.hold_id = h.id)`, member).Scan(&open); err != nil {
			return p, err
		}
		if open > 0 {
			add(api.Deduction{Kind: "authorised_payout", AmountKobo: magOf(open),
				Reason: "Payouts already authorised and not yet paid, their charges included: " + naira(open) + "."}, open)
		}
	}
	if p.deductions == nil {
		p.deductions = []api.Deduction{}
	}
	return p, nil
}

// withdrawableOf: the figure, nil when it cannot be worked out.
func (f *fakeAPI) withdrawableOf(p position) *int64 {
	if p.floor == nil {
		if f.brk == "bal_floor_unset_zero" {
			z := int64(0)
			return &z
		}
		return nil
	}
	n := p.savings - p.total
	if n < 0 && f.brk != "bal_negative" {
		n = 0
	}
	return &n
}

var floorUnset = api.Problem{Code: "floor_unset", Message: "Your society has not yet set the minimum balance every member keeps, so what you may withdraw cannot be worked out. Your savings are shown; ask your secretary."}

func (f *fakeAPI) GetMemberWithdrawable(ctx context.Context, req api.GetMemberWithdrawableRequestObject) (api.GetMemberWithdrawableResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	society, ok := f.balanceReader(ctx, caller, member)
	if !ok {
		return api.GetMemberWithdrawable404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	p, err := f.positionOf(ctx, society, member)
	if err != nil {
		return nil, err
	}
	out := api.Withdrawable{MemberId: req.MemberId, AsOf: time.Now(), SavingsBalanceKobo: api.SavingsBalance(strconv.FormatInt(p.savings, 10)), Deductions: p.deductions}
	if w := f.withdrawableOf(p); w != nil {
		m := magOf(*w)
		out.WithdrawableKobo = &m
	} else {
		if f.brk == "bal_floor_unset_hides_savings" {
			return api.GetMemberWithdrawable404JSONResponse{Code: "not_configured", Message: "this society has not set its account floor"}, nil
		}
		u := floorUnset
		out.Unavailable = &u
	}
	return api.GetMemberWithdrawable200JSONResponse(out), nil
}

func (f *fakeAPI) GetWithdrawalQuote(ctx context.Context, req api.GetWithdrawalQuoteRequestObject) (api.GetWithdrawalQuoteResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	society, ok := f.balanceReader(ctx, caller, member)
	if !ok {
		return api.GetWithdrawalQuote404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	amount, okA := magnitude(req.Params.AmountKobo)
	if !okA {
		return api.GetWithdrawalQuote400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	today := time.Now()
	on := today.Format("2006-01-02")
	if f.brk == "bal_quote_stale" {
		on = "2020-01-01"
	}
	c, determined, err := f.quoteOf(ctx, society, "withdrawal_out", amount, on)
	if err != nil {
		return nil, err
	}
	if !determined {
		return api.GetWithdrawalQuote422JSONResponse{Code: "not_configured", Message: "this society's charges for a withdrawal of this amount are not set"}, nil
	}
	p, err := f.positionOf(ctx, society, member)
	if err != nil {
		return nil, err
	}
	q := api.WithdrawalQuote{MemberId: req.MemberId, AmountKobo: magOf(amount), Charges: *c.wire(), TotalOutKobo: magOf(amount + c.total()),
		Indicative: true, QuotedOn: openapi_types.Date{Time: today}, Note: quoteNote}
	if f.brk == "bal_quote_binding" {
		q.Indicative, q.Note = false, "This is the charge for this withdrawal."
	}
	if w := f.withdrawableOf(p); w != nil {
		out := amount + c.total()
		if f.brk == "bal_quote_ignores_charges" {
			out = amount
		}
		fits := out <= *w
		q.WithinWithdrawable = &fits
	}
	return api.GetWithdrawalQuote200JSONResponse(q), nil
}

// authorisedChargesOf: the charge fixed by the request's live hold (open or
// paid, never released); nil before any.
func (f *fakeAPI) authorisedChargesOf(ctx context.Context, request string, amount int64, society string) *api.Charges {
	if f.brk == "bal_final_charge_hidden" {
		return nil
	}
	var c chg
	err := f.db.QueryRow(ctx, `SELECT h.partner_fee_kobo, h.vat_kobo, h.statutory_levy_kobo, h.argon_margin_kobo FROM withdrawal_holds h
		WHERE h.request_id = $1 AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
		ORDER BY h.held_at DESC LIMIT 1`, request).Scan(&c.fee, &c.vat, &c.levy, &c.margin)
	if err != nil {
		if f.brk == "bal_charge_shown_early" {
			if q, ok, qerr := f.quoteOf(ctx, society, "withdrawal_out", amount, time.Now().Format("2006-01-02")); qerr == nil && ok {
				return q.wire()
			}
		}
		return nil
	}
	return c.wire()
}
