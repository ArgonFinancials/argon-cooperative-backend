//go:build tier1

// THE BALANCE REWRITE in the mutation vehicle (reviewed in four pieces,
// 2026-10-26) — test code, NOT the API:
//
//   - the largest amount a member can request: the database's
//     largest_withdrawal (0017), exact at a band boundary;
//   - requests raised and not yet authorised, listed and NOT deducted;
//   - the charge quoted at the raise beside the charge the hold applied, with
//     a sentence only when they differ;
//   - a refused hold says what changed.
//
// Breaks:
//
//	largest_naive                    the largest is the withdrawable less the charge on the withdrawable
//	largest_off_by_one               the largest is one kobo too high
//	largest_when_unavailable         a largest is given while the withdrawable figure is unavailable
//	requested_deducted               an open request is subtracted from the figure
//	requested_hidden                 open requests are not listed
//	requested_kept_after_authorised  a request stays listed after its hold: the money shows twice
//	quote_recomputed                 quotedCharges worked out from today's series, not kept
//	change_unsaid                    no chargeChanged when the charges differ
//	change_note_always               chargeChanged even when they match
//	hold_refusal_bare                a refused hold says only that it does not fit
//	hold_cause_wrong                 every refusal blamed on the withdrawable falling
//	hold_uses_quote                  the hold applies the quoted charge, so the quote binds
package tier1

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/argon/backend/internal/api"
)

const requestedNote = "Requested, waiting for approval. Not taken from what you can withdraw until it is approved."

var (
	quotesOnce sync.Once
	quotesKept bool
)

// schemaKeepsQuotes: does a request keep the charge it was quoted (0017)?
func schemaKeepsQuotes() bool {
	quotesOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'approval_requests' AND column_name = 'quoted_on')`).Scan(&quotesKept)
	})
	return quotesKept
}

// largestOf: the largest amount the member can request, nil when the figure
// is unavailable.
func (f *fakeAPI) largestOf(ctx context.Context, society string, withdrawable *int64, p position) (*int64, error) {
	if withdrawable == nil {
		if f.brk != "largest_when_unavailable" {
			return nil, nil
		}
		n := p.savings
		return &n, nil
	}
	var n int64
	if f.brk == "largest_naive" {
		c, ok, err := f.quoteOf(ctx, society, "withdrawal_out", *withdrawable, time.Now().Format("2006-01-02"))
		if err != nil {
			return nil, err
		}
		n = *withdrawable
		if ok {
			n -= c.total()
		}
	} else if err := f.db.QueryRow(ctx, `SELECT coalesce(largest_withdrawal($1, 'withdrawal_out', $2, current_date), 0)`,
		society, *withdrawable).Scan(&n); err != nil {
		return nil, err
	}
	if f.brk == "largest_off_by_one" {
		n++
	}
	if n < 0 {
		n = 0
	}
	return &n, nil
}

// requestedOf: the member's withdrawal requests raised and not yet authorised
// (awaiting or approved, no hold), with the charge each was quoted.
func (f *fakeAPI) requestedOf(ctx context.Context, member string) ([]api.RequestedWithdrawal, int64, error) {
	out := []api.RequestedWithdrawal{}
	if f.brk == "requested_hidden" || !schemaKeepsQuotes() {
		return out, 0, nil
	}
	held := `AND NOT EXISTS (SELECT 1 FROM withdrawal_holds h WHERE h.request_id = r.id
		AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id))`
	if f.brk == "requested_kept_after_authorised" {
		held = ""
	}
	rows, err := f.db.Query(ctx, `SELECT r.id::text, r.amount_kobo, coalesce(r.quoted_partner_fee_kobo, 0), coalesce(r.quoted_vat_kobo, 0),
		coalesce(r.quoted_statutory_levy_kobo, 0), coalesce(r.quoted_argon_margin_kobo, 0), r.raised_at
		FROM approval_requests r WHERE r.member_id = $1 AND r.kind = 'withdrawal_out'
		  AND approval_status(r.id) IN ('awaiting', 'approved') `+held+` ORDER BY r.raised_at, r.id`, member)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var sum int64
	for rows.Next() {
		var id string
		var amount int64
		var c chg
		var at time.Time
		if err := rows.Scan(&id, &amount, &c.fee, &c.vat, &c.levy, &c.margin, &at); err != nil {
			return nil, 0, err
		}
		out = append(out, api.RequestedWithdrawal{RequestId: uuidOf(id), AmountKobo: magOf(amount), QuotedCharges: *c.wire(),
			TotalKobo: magOf(amount + c.total()), RaisedAt: at, Note: requestedNote})
		sum += amount + c.total()
	}
	return out, sum, rows.Err()
}

// quotedChargesOf: the charge a request was quoted when raised, as kept (0017);
// nil when it carries none.
func (f *fakeAPI) quotedChargesOf(ctx context.Context, request string, amount int64, society string) *api.Charges {
	if !schemaKeepsQuotes() {
		return nil
	}
	if f.brk == "quote_recomputed" {
		if q, ok, err := f.quoteOf(ctx, society, "withdrawal_out", amount, time.Now().Format("2006-01-02")); err == nil && ok {
			return q.wire()
		}
		return nil
	}
	var c chg
	var on *time.Time
	if err := f.db.QueryRow(ctx, `SELECT coalesce(quoted_partner_fee_kobo, 0), coalesce(quoted_vat_kobo, 0), coalesce(quoted_statutory_levy_kobo, 0),
		coalesce(quoted_argon_margin_kobo, 0), quoted_on FROM approval_requests WHERE id = $1`, request).Scan(&c.fee, &c.vat, &c.levy, &c.margin, &on); err != nil || on == nil {
		return nil
	}
	return c.wire()
}

func kobos(c *api.Charges) int64 {
	n, _ := strconv.ParseInt(string(c.TotalKobo), 10, 64)
	return n
}

// chargeChangedOf: the sentence, only when the quoted and the authorised
// charges both exist and differ; it names the date the society's charges
// changed (the series in force at the hold).
func (f *fakeAPI) chargeChangedOf(ctx context.Context, request, society string, quoted, authorised *api.Charges) *string {
	if quoted == nil || authorised == nil || f.brk == "change_unsaid" {
		return nil
	}
	if kobos(quoted) == kobos(authorised) && f.brk != "change_note_always" {
		return nil
	}
	var changed time.Time
	f.db.QueryRow(ctx, `SELECT max(cs.effective_from) FROM charge_schedules cs JOIN withdrawal_holds h ON h.request_id = $1
		WHERE cs.society_id = $2 AND cs.kind = 'withdrawal_out' AND cs.effective_from <= h.held_at::date`, request, society).Scan(&changed)
	s := "The charge changed between your request and its approval: you were quoted " + naira(kobos(quoted)) +
		" and were charged " + naira(kobos(authorised)) + ", because the society's charges changed on " + changed.Format("2 January 2006") + "."
	return &s
}

// holdRefusal: what a refused hold says, for exceeds_withdrawable.
func (f *fakeAPI) holdRefusal(ctx context.Context, request, society, member string, amount int64) (api.HoldRefusal, bool, error) {
	out := api.HoldRefusal{Code: "exceeds_withdrawable", Message: "This request no longer fits."}
	quoted := f.quotedChargesOf(ctx, request, amount, society)
	now, ok, err := f.quoteOf(ctx, society, "withdrawal_out", amount, time.Now().Format("2006-01-02"))
	if err != nil || !ok || quoted == nil {
		return out, false, err
	}
	p, err := f.positionOf(ctx, society, member)
	if err != nil {
		return out, false, err
	}
	w := f.withdrawableOf(p)
	if w == nil {
		return out, false, nil
	}
	q := kobos(quoted)
	changed := now.total() != q
	fell := amount+q > *w
	if f.brk == "hold_uses_quote" && !fell {
		return out, true, nil // the quoted charge fits: the hold "goes through" at the quote
	}
	if f.brk == "hold_refusal_bare" {
		return out, false, nil
	}
	var causes []api.HoldRefusalCauses
	var why []string
	if changed && f.brk != "hold_cause_wrong" {
		causes = append(causes, api.ChargeChanged)
		var on time.Time
		f.db.QueryRow(ctx, `SELECT max(effective_from) FROM charge_schedules WHERE society_id = $1 AND kind = 'withdrawal_out' AND effective_from <= current_date`, society).Scan(&on)
		why = append(why, "The society's charges changed on "+on.Format("2 January 2006")+": the quoted "+naira(q)+" is now "+naira(now.total())+".")
	}
	if fell || f.brk == "hold_cause_wrong" {
		causes = append(causes, api.WithdrawableFell)
		why = append(why, "What the member can withdraw has fallen since the request was raised (another payout authorised, a guarantee, or the floor).")
	}
	needed := amount + now.total()
	a, qk, nk, nd, wk := magOf(amount), magOf(q), magOf(now.total()), magOf(needed), magOf(*w)
	out.AmountKobo, out.QuotedChargeKobo, out.ChargeNowKobo, out.NeededKobo, out.WithdrawableKobo, out.Causes = &a, &qk, &nk, &nd, &wk, &causes
	out.Message = "This request no longer fits. " + strings.Join(why, " ") + " " + naira(needed) + " is needed and " + naira(*w) + " can be withdrawn."
	return out, false, nil
}
