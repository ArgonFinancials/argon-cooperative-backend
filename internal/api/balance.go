package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// A MEMBER'S WITHDRAWABLE FIGURE AND THE WITHDRAWAL QUOTE. Written against
// test/tier1/tier1_api_balance_test.go (the balance rewrite, reviewed in four
// pieces and its handlers in four more, 2026-10-26/28; mutation-tested with
// apifake_balance_test.go and apifake_balance_requests_test.go before this file
// existed).
//
// THE DIVISION: GO ARRANGES WHAT THE DATABASE SAYS AND CALCULATES NOTHING
// ABOUT MONEY. Every amount below comes out of a database function or a query
// that sums it: member_position and member_guarantees_held (0018, the one
// arithmetic the raise and the hold are refused by), largest_withdrawal and the
// kept quote (0017), charge_quote. Go puts them in order, words around them,
// and never adds, subtracts or compares a kobo itself: a figure computed here
// would be a second answer to a question the database already answers.

// Reading is tied to the duty (D5): the member; the treasurer and the
// secretary, who raise for a member without a phone; the auditor, whose task
// it is. Not any officer. Anyone else: 404, as for no such member.
var balanceReaders = []string{"treasurer", "secretary", "internal_auditor"}

var (
	balanceNotFound = Problem{Code: "not_found", Message: "no such member"}
	floorUnset      = Problem{Code: "floor_unset", Message: "Your society has not yet set the minimum balance every member keeps, so what you may withdraw cannot be worked out. Your savings are shown; ask your secretary."}
)

const (
	quoteNote     = "This quote is indicative: the charge is fixed when the payout is authorised, from your society's charges that day, and you will be shown it then."
	requestedNote = "Requested, waiting for approval. Not taken from what you can withdraw until it is approved."
)

// balanceReader: the member's society, when the caller may read their figure.
func (s *server) balanceReader(ctx context.Context, member string) (string, bool, error) {
	var society string
	var own bool
	err := s.db.QueryRow(ctx, `SELECT society_id::text, coalesce(user_id = $2, false) FROM members WHERE id = $1`, member, callerOf(ctx)).Scan(&society, &own)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil || own {
		return society, own, err
	}
	post, err := s.dutyPost(ctx, society, balanceReaders)
	return society, post != "", err
}

// chargesOf: four parts and their total, every one as the database summed it.
func chargesOf(fee, vat, levy, margin, total int64) Charges {
	return Charges{PartnerFeeKobo: magnitudeOf(fee), VatKobo: magnitudeOf(vat), StatutoryLevyKobo: magnitudeOf(levy),
		ArgonMarginKobo: magnitudeOf(margin), TotalKobo: magnitudeOf(total)}
}

func (s *server) GetMemberWithdrawable(ctx context.Context, req GetMemberWithdrawableRequestObject) (GetMemberWithdrawableResponseObject, error) {
	member := req.MemberId.String()
	society, ok, err := s.balanceReader(ctx, member)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetMemberWithdrawable404JSONResponse(balanceNotFound), nil
	}

	// The parts and the figure: the one arithmetic (0018).
	var savings, held, open int64
	var floor, figure *int64
	if err := s.db.QueryRow(ctx, `SELECT savings_kobo, held_kobo, floor_kobo, open_holds_kobo, withdrawable_kobo FROM member_position($1)`, member).
		Scan(&savings, &held, &floor, &open, &figure); err != nil {
		return nil, err
	}
	out := Withdrawable{MemberId: req.MemberId, AsOf: time.Now(), SavingsBalanceKobo: SavingsBalance(koboOf(savings)),
		Deductions: []Deduction{}, Requested: []RequestedWithdrawal{}}

	// Deductions in a FIXED order (floor, guarantees by loan, open holds), so
	// two readings have the same shape; each says why.
	if floor != nil {
		out.Deductions = append(out.Deductions, Deduction{Kind: "floor", AmountKobo: magnitudeOf(*floor),
			Reason: "Your society's minimum balance: every member keeps " + naira(*floor) + " saved."})
	}
	rows, err := s.db.Query(ctx, `SELECT loan_id::text, borrower_name, owed_kobo FROM member_guarantees_held($1)`, member)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var loan, borrower string
		var owed int64
		if err := rows.Scan(&loan, &borrower, &owed); err != nil {
			rows.Close()
			return nil, err
		}
		l, b := uuidOf(loan), borrower
		out.Deductions = append(out.Deductions, Deduction{Kind: "guarantee", AmountKobo: magnitudeOf(owed), LoanId: &l, BorrowerName: &b,
			Reason: "Held while you guarantee " + borrower + "'s loan: the " + naira(owed) + " still owed on it is held in full, until your society decides how much a guarantor answers for."})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if open > 0 {
		out.Deductions = append(out.Deductions, Deduction{Kind: "authorised_payout", AmountKobo: magnitudeOf(open),
			Reason: "Payouts already authorised and not yet paid, their charges included: " + naira(open) + "."})
	}

	// The figure, and the largest amount that can be asked for against it:
	// NULL together when no floor is set; 0 when nothing fits.
	if figure == nil {
		u := floorUnset
		out.Unavailable = &u
	} else {
		w := magnitudeOf(*figure)
		out.WithdrawableKobo = &w
		var largest int64
		err := s.db.QueryRow(ctx, `SELECT coalesce(largest_withdrawal($1, 'withdrawal_out', $2, current_date), 0)`, society, *figure).Scan(&largest)
		if err != nil && !strings.HasPrefix(refusedBy(err), "charge_") {
			return nil, err
		}
		// Charges not set: no request can be raised, so the largest is 0.
		l := magnitudeOf(largest)
		out.LargestRequestableKobo = &l
	}

	// Requested and not yet authorised: awaiting or approved, with no LIVE
	// hold (a released hold puts the request back). Listed, never deducted.
	// Each total is summed here, in the query.
	rq, err := s.db.Query(ctx, `SELECT r.id::text, r.amount_kobo,
		       coalesce(r.quoted_partner_fee_kobo, 0), coalesce(r.quoted_vat_kobo, 0), coalesce(r.quoted_statutory_levy_kobo, 0), coalesce(r.quoted_argon_margin_kobo, 0),
		       coalesce(r.quoted_partner_fee_kobo, 0) + coalesce(r.quoted_vat_kobo, 0) + coalesce(r.quoted_statutory_levy_kobo, 0) + coalesce(r.quoted_argon_margin_kobo, 0),
		       r.amount_kobo + coalesce(r.quoted_partner_fee_kobo, 0) + coalesce(r.quoted_vat_kobo, 0) + coalesce(r.quoted_statutory_levy_kobo, 0) + coalesce(r.quoted_argon_margin_kobo, 0),
		       r.raised_at
		  FROM approval_requests r
		 WHERE r.member_id = $1 AND r.kind = 'withdrawal_out' AND approval_status(r.id) IN ('awaiting', 'approved')
		   AND NOT EXISTS (SELECT 1 FROM withdrawal_holds h WHERE h.request_id = r.id
		                    AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id))
		 ORDER BY r.raised_at, r.id`, member)
	if err != nil {
		return nil, err
	}
	defer rq.Close()
	for rq.Next() {
		var id string
		var amount, fee, vat, levy, margin, charge, total int64
		var at time.Time
		if err := rq.Scan(&id, &amount, &fee, &vat, &levy, &margin, &charge, &total, &at); err != nil {
			return nil, err
		}
		out.Requested = append(out.Requested, RequestedWithdrawal{RequestId: uuidOf(id), AmountKobo: magnitudeOf(amount),
			QuotedCharges: chargesOf(fee, vat, levy, margin, charge), TotalKobo: magnitudeOf(total), RaisedAt: at, Note: requestedNote})
	}
	if err := rq.Err(); err != nil {
		return nil, err
	}
	return GetMemberWithdrawable200JSONResponse(out), nil
}

func (s *server) GetWithdrawalQuote(ctx context.Context, req GetWithdrawalQuoteRequestObject) (GetWithdrawalQuoteResponseObject, error) {
	member := req.MemberId.String()
	society, ok, err := s.balanceReader(ctx, member)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetWithdrawalQuote404JSONResponse(balanceNotFound), nil
	}
	amount, okA := magnitude(req.Params.AmountKobo)
	if !okA {
		return GetWithdrawalQuote400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	// The charge today, the total out, and whether it fits: one query, the
	// raise rule's own comparison (the amount and its charge at most the
	// figure). NULL, not false, when the figure cannot be worked out.
	var fee, vat, levy, margin, charge, total int64
	var fits *bool
	err = s.db.QueryRow(ctx, `SELECT q.partner_fee_kobo, q.vat_kobo, q.statutory_levy_kobo, q.argon_margin_kobo,
		       q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo,
		       $3::bigint + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo,
		       CASE WHEN p.withdrawable_kobo IS NULL THEN NULL
		            ELSE $3::bigint + q.partner_fee_kobo + q.vat_kobo + q.statutory_levy_kobo + q.argon_margin_kobo <= p.withdrawable_kobo END
		  FROM charge_quote($1, 'withdrawal_out', $3, current_date) q, member_position($2) p`, society, member, amount).
		Scan(&fee, &vat, &levy, &margin, &charge, &total, &fits)
	if err != nil {
		if strings.HasPrefix(refusedBy(err), "charge_") {
			return GetWithdrawalQuote422JSONResponse{Code: "not_configured", Message: "this society's charges for a withdrawal of this amount are not set: " + dbMessage(err)}, nil
		}
		return nil, err
	}
	q := WithdrawalQuote{MemberId: req.MemberId, AmountKobo: magnitudeOf(amount), Charges: chargesOf(fee, vat, levy, margin, charge),
		TotalOutKobo: magnitudeOf(total), WithinWithdrawable: fits, Indicative: true,
		QuotedOn: openapi_types.Date{Time: time.Now()}, Note: quoteNote}
	if fits == nil {
		u := floorUnset
		q.WithdrawableUnavailable = &u
	}
	return GetWithdrawalQuote200JSONResponse(q), nil
}
