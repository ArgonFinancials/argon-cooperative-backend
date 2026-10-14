//go:build tier1

// THE MUTATION VEHICLE FOR THE MONEY ENDPOINT TESTS — test code, NOT the API.
//
// A reference implementation of POST /societies/{id}/contributions,
// /approvals/{id}/holds, /holds/{id}/releases and /societies/{id}/payouts
// (0011, 0012), breakable one named way per run. The database decides every
// rule it holds; each break is something only the API can get wrong.
//
// Breaks — who:
//
//	money_any_officer_records     any officer of the society records money (the chairman, the auditor)
//	money_any_officer_authorises  any officer authorises a payout (the auditor)
//	money_not_found_says_why      a refusal to a non-officer says "not permitted"
//	money_member_any_society      a contribution to a member of another society is attempted
//	money_post_fixed              the entry records the post as "treasurer", whoever recorded it
//
// Breaks — what is written and answered:
//
//	money_acts_unmintable         a PIN proof for the three money acts is refused (finding 36)
//	money_step_up_as_500          a step-up refusal answers 500
//	money_charge_not_the_servers  a contribution is built with no charge lines, whatever the series say
//	money_member_amount_gross     memberAmountKobo is the gross, charges or not
//	money_undetermined_as_zero    an undetermined charge is answered as zeros, chargeDetermined true
//	money_flags_dropped           the answer carries no flags
//	money_flags_null              no flags is sent as null, not []
//	money_zero_ok                 a zero contribution is attempted
//	money_hold_charges_unread     the hold's answer carries what was inserted, not what the database fixed
//	money_refusal_unexplained     a hold refusal's message is "refused", not the database's words
//	money_withdrawable_as_409     exceeds_withdrawable answers 409
//	money_not_configured_as_500   an unset floor or charge answers 500
//	money_hold_open_as_approved   a second hold reads as not_approved
//	money_release_no_reason       a blank reason is passed on, and its refusal answers 500
//	money_paid_release_as_500     releasing a paid hold answers 500
//	money_released_state_open     a released hold reads as open
//	money_duplicate_unnamed       already_recorded does not name the entry
//	money_duplicate_as_500        the same transfer twice answers 500
//	money_hold_member_as_500      not the hold's member answers 500
//	money_payout_charges_dropped  a held payout is built with no charges, not the hold's
//	money_reference_optional      a blank transfer reference is passed on, and its refusal answers 500
package tier1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

var (
	moneyRecorders   = []string{"treasurer", "secretary"}
	moneyAuthorisers = []string{"chairman", "treasurer", "secretary"}
	postLabels       = map[string]string{"chairman": "Chairman", "treasurer": "Treasurer", "secretary": "Secretary",
		"internal_auditor": "Internal auditor", "branch_officer": "Branch officer", "loan_officer": "Loan officer"}
	moneyStepUpRefusals = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
		"entry_needs_step_up": true, "entry_act_is_actors": true, "entry_act_kind": true, "entry_act_same_transaction": true,
		"hold_needs_step_up": true, "hold_act_is_holders": true, "hold_act_kind": true, "hold_act_same_transaction": true}
	notConfigured = map[string]bool{"account_floor_unset": true, "charge_partner_fee_unset": true, "charge_vat_unset": true,
		"charge_levy_unset": true, "charge_margin_unset": true, "charge_band_missing": true}
)

// moneyPost: the caller's current post in the society among `posts` (in that
// order of preference), "" if none. `any` admits every current post.
func (f *fakeAPI) moneyPost(ctx context.Context, caller, society string, posts []string, any bool) string {
	var post string
	f.db.QueryRow(ctx, `SELECT a.post::text FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)
		  AND ($4 OR a.post::text = ANY($3)) ORDER BY array_position($3, a.post::text) NULLS LAST LIMIT 1`,
		caller, society, posts, any).Scan(&post)
	return post
}

func (f *fakeAPI) officerHere(ctx context.Context, caller, society string) bool {
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`,
		caller, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) moneyNotFound(what string, exists bool) api.Problem {
	if exists && f.brk == "money_not_found_says_why" {
		return api.Problem{Code: "not_permitted", Message: "you may not do this here"}
	}
	return api.Problem{Code: "not_found", Message: "no such " + what}
}

var moneyNotPermittedRecord = api.Problem{Code: "not_permitted", Message: "recording money is the treasurer's or the secretary's"}
var moneyNotPermittedAuthorise = api.Problem{Code: "not_permitted", Message: "authorising a payout is a signatory's"}

func stepUpProblem(act string) api.Problem {
	return api.Problem{Code: "step_up_required", Message: "a fresh PIN proof for " + act + ", your own"}
}

func magnitude(s api.Magnitude) (int64, bool) {
	n, err := strconv.ParseInt(string(s), 10, 64)
	return n, err == nil && n >= 0
}

type chg struct{ fee, vat, levy, margin int64 }

func (c chg) total() int64 { return c.fee + c.vat + c.levy + c.margin }

func (c chg) wire() *api.Charges {
	m := func(n int64) api.Magnitude { return api.Magnitude(strconv.FormatInt(n, 10)) }
	return &api.Charges{PartnerFeeKobo: m(c.fee), VatKobo: m(c.vat), StatutoryLevyKobo: m(c.levy), ArgonMarginKobo: m(c.margin), TotalKobo: m(c.total())}
}

// quoteOf: the series' charge, or determined=false where the series cannot say.
func (f *fakeAPI) quoteOf(ctx context.Context, society, kind string, amount int64, on string) (c chg, determined bool, err error) {
	err = f.db.QueryRow(ctx, `SELECT * FROM charge_quote($1, $2, $3, $4)`, society, kind, amount, on).Scan(&c.fee, &c.vat, &c.levy, &c.margin)
	if err != nil && pgConstraint(err) != "" && (notConfigured[pgConstraint(err)] || strings.HasPrefix(pgConstraint(err), "charge_")) {
		return chg{}, false, nil
	}
	return c, err == nil, err
}

type mline struct {
	acct, role, member string
	kobo               int64
}

func (f *fakeAPI) accountByRole(ctx context.Context, society, role string) string {
	var id string
	f.db.QueryRow(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND role = $2 ORDER BY code LIMIT 1`, society, role).Scan(&id)
	return id
}

func (f *fakeAPI) chargeLines(ctx context.Context, society string, c chg) []mline {
	var ls []mline
	for _, x := range []struct {
		kobo             int64
		acctRole, charge string
	}{{c.fee, "charges_partner", "partner_charge"}, {c.vat, "charges_vat", "vat"}, {c.levy, "statutory_levy_payable", "statutory_levy"}, {c.margin, "charges_margin", "argon_margin"}} {
		if x.kobo != 0 {
			ls = append(ls, mline{acct: f.accountByRole(ctx, society, x.acctRole), role: x.charge, kobo: -x.kobo})
		}
	}
	return ls
}

type moneyEntry struct {
	society, kind, valueDate, post string
	request, hold, ref             *string
	lines                          []mline
}

// postMoney: the caller's act and the entry, in one transaction.
func (f *fakeAPI) postMoney(ctx context.Context, caller, act, proof string, e moneyEntry) (string, error) {
	var id string
	label := postLabels[e.post]
	if f.brk == "money_post_fixed" {
		e.post, label = "treasurer", "Treasurer"
	}
	err := f.actThen(ctx, caller, act, proof, false, func(x pgx.Tx, actID *string) error {
		if err := x.QueryRow(ctx, `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label,
			  act_id, request_id, hold_id, payout_reference) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id::text`,
			e.society, e.kind, e.valueDate, caller, e.post, label, actID, e.request, e.hold, e.ref).Scan(&id); err != nil {
			return err
		}
		for _, l := range e.lines {
			var member *string
			if l.member != "" {
				member = &l.member
			}
			role := l.role
			if role == "" {
				role = "principal"
			}
			if _, err := x.Exec(ctx, `INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($1, $2, $3, $4, $5)`,
				id, l.acct, l.kobo, role, member); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func (f *fakeAPI) flagsOf(ctx context.Context, entry string) []api.Discrepancy {
	if f.brk == "money_flags_null" {
		return nil
	}
	out := []api.Discrepancy{}
	if f.brk == "money_flags_dropped" {
		return out
	}
	rows, err := f.db.Query(ctx, `SELECT kind, expected, observed FROM ledger_discrepancies WHERE entry_id = $1 ORDER BY kind`, entry)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d api.Discrepancy
		var kind string
		if rows.Scan(&kind, &d.Expected, &d.Observed) == nil {
			d.Kind = api.DiscrepancyKind(kind)
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeAPI) movement(ctx context.Context, caller, post, entry, kind, society, member string, amount int64, date openapi_types.Date,
	c chg, determined bool, hold, ref *string) api.MoneyMovement {
	m := api.MoneyMovement{EntryId: uuidOf(entry), Kind: api.MoneyMovementKind(kind), SocietyId: uuidOf(society), MemberId: uuidOf(member),
		AmountKobo: api.Magnitude(strconv.FormatInt(amount, 10)), ValueDate: date, ChargeDetermined: determined,
		Flags: f.flagsOf(ctx, entry), PayoutReference: ref}
	if hold != nil {
		h := uuidOf(*hold)
		m.HoldId = &h
	}
	if f.brk == "money_post_fixed" {
		post = "treasurer"
	}
	p := api.PostedByPost(post)
	m.PostedBy = api.PostedBy{UserId: uuidOf(caller), Post: &p, PostLabel: postLabels[post]}
	if determined {
		m.Charges = c.wire()
	} else if f.brk == "money_undetermined_as_zero" {
		m.ChargeDetermined, m.Charges = true, chg{}.wire()
	}
	moved := amount + c.total()
	if kind == "contribution_in" {
		moved = amount - c.total()
	}
	if f.brk == "money_member_amount_gross" {
		moved = amount
	}
	m.MemberAmountKobo = api.Magnitude(strconv.FormatInt(moved, 10))
	return m
}

// =============================================================================
// CONTRIBUTIONS
// =============================================================================

func (f *fakeAPI) RecordContribution(ctx context.Context, req api.RecordContributionRequestObject) (api.RecordContributionResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if !f.officerHere(ctx, caller, society) {
		var exists bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&exists)
		return api.RecordContribution404JSONResponse(f.moneyNotFound("society", exists)), nil
	}
	post := f.moneyPost(ctx, caller, society, moneyRecorders, f.brk == "money_any_officer_records")
	if post == "" {
		return api.RecordContribution403JSONResponse(moneyNotPermittedRecord), nil
	}
	b := req.Body
	if b == nil {
		return api.RecordContribution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	amount, ok := magnitude(b.AmountKobo)
	if !ok {
		return api.RecordContribution400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	if amount == 0 && f.brk != "money_zero_ok" {
		return api.RecordContribution422JSONResponse{Code: "zero_amount", Message: "a contribution of nothing is not recorded"}, nil
	}
	member := b.MemberId.String()
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !here && f.brk != "money_member_any_society" {
		return api.RecordContribution422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	date := b.ValueDate.Format("2006-01-02")
	c, determined, err := f.quoteOf(ctx, society, "contribution_in", amount, date)
	if err != nil {
		return nil, err
	}
	if f.brk == "money_charge_not_the_servers" {
		c, determined = chg{}, true
	}
	lines := []mline{{acct: f.accountByRole(ctx, society, "bank"), kobo: amount},
		{acct: f.accountByRole(ctx, society, "member_savings"), kobo: -(amount - c.total()), member: member}}
	lines = append(lines, f.chargeLines(ctx, society, c)...)
	entry, err := f.postMoney(ctx, caller, "contribution_record", b.PinProofId.String(),
		moneyEntry{society: society, kind: "contribution_in", valueDate: date, post: post, lines: lines})
	if err != nil {
		if moneyStepUpRefusals[pgConstraint(err)] && f.brk != "money_step_up_as_500" {
			return api.RecordContribution403JSONResponse(stepUpProblem("contribution_record")), nil
		}
		return nil, err
	}
	return api.RecordContribution201JSONResponse(f.movement(ctx, caller, post, entry, "contribution_in", society, member, amount, b.ValueDate, c, determined, nil, nil)), nil
}

// =============================================================================
// HOLDS
// =============================================================================

func (f *fakeAPI) holdView(ctx context.Context, id string) (api.PayoutHold, error) {
	var h api.PayoutHold
	var hid, req, member, by string
	var amount int64
	var c chg
	var paid, released bool
	var reason *string
	err := f.db.QueryRow(ctx, `SELECT h.id::text, h.request_id::text, h.member_id::text, h.amount_kobo, h.partner_fee_kobo, h.vat_kobo,
		  h.statutory_levy_kobo, h.argon_margin_kobo, h.actor_user_id::text, h.held_at,
		  EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id), r.hold_id IS NOT NULL, r.reason
		FROM withdrawal_holds h LEFT JOIN withdrawal_hold_releases r ON r.hold_id = h.id WHERE h.id = $1`, id).
		Scan(&hid, &req, &member, &amount, &c.fee, &c.vat, &c.levy, &c.margin, &by, &h.AuthorisedAt, &paid, &released, &reason)
	if err != nil {
		return h, err
	}
	h.HoldId, h.RequestId, h.MemberId, h.AuthorisedBy = uuidOf(hid), uuidOf(req), uuidOf(member), uuidOf(by)
	h.AmountKobo = api.Magnitude(strconv.FormatInt(amount, 10))
	h.Charges = *c.wire()
	h.TotalKobo = api.Magnitude(strconv.FormatInt(amount+c.total(), 10))
	h.State = "open"
	switch {
	case paid:
		h.State = "paid"
	case released && f.brk != "money_released_state_open":
		h.State = "released"
	}
	h.ReleaseReason = reason
	return h, nil
}

func (f *fakeAPI) AuthorisePayout(ctx context.Context, req api.AuthorisePayoutRequestObject) (api.AuthorisePayoutResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	r, err := f.apprLoad(ctx, req.RequestId.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return api.AuthorisePayout404JSONResponse(f.moneyNotFound("request", false)), nil
	}
	if err != nil {
		return nil, err
	}
	if !f.officerHere(ctx, caller, r.society) {
		return api.AuthorisePayout404JSONResponse(f.moneyNotFound("request", true)), nil
	}
	if f.moneyPost(ctx, caller, r.society, moneyAuthorisers, f.brk == "money_any_officer_authorises") == "" {
		return api.AuthorisePayout403JSONResponse(moneyNotPermittedAuthorise), nil
	}
	if req.Body == nil {
		return api.AuthorisePayout400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var hold string
	err = f.actThen(ctx, caller, "withdrawal_authorisation", req.Body.PinProofId.String(), false, func(x pgx.Tx, act *string) error {
		// The charges are the database's to fix (0012); these zeros are overwritten.
		return x.QueryRow(ctx, `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, partner_fee_kobo, vat_kobo,
			  statutory_levy_kobo, argon_margin_kobo, actor_user_id, act_id) VALUES ($1, $2, $3, $4, 0, 0, 0, 0, $5, $6) RETURNING id::text`,
			r.id, r.society, r.member, r.amount, caller, act).Scan(&hold)
	})
	if err != nil {
		msg := dbMessage(err)
		if f.brk == "money_refusal_unexplained" {
			msg = "refused"
		}
		switch c := pgConstraint(err); {
		case moneyStepUpRefusals[c] && f.brk != "money_step_up_as_500":
			return api.AuthorisePayout403JSONResponse(stepUpProblem("withdrawal_authorisation")), nil
		case c == "hold_request_approved":
			return api.AuthorisePayout409JSONResponse{Code: "not_approved", Message: msg}, nil
		case c == "hold_once_open":
			code := "hold_open"
			if f.brk == "money_hold_open_as_approved" {
				code = "not_approved"
			}
			return api.AuthorisePayout409JSONResponse{Code: code, Message: msg}, nil
		case c == "withdrawal_within_withdrawable":
			if f.brk == "money_withdrawable_as_409" {
				return api.AuthorisePayout409JSONResponse{Code: "exceeds_withdrawable", Message: msg}, nil
			}
			return api.AuthorisePayout422JSONResponse{Code: "exceeds_withdrawable", Message: msg}, nil
		case notConfigured[c] && f.brk != "money_not_configured_as_500":
			return api.AuthorisePayout422JSONResponse{Code: "not_configured", Message: msg}, nil
		}
		return nil, err
	}
	h, err := f.holdView(ctx, hold)
	if err != nil {
		return nil, err
	}
	if f.brk == "money_hold_charges_unread" {
		h.Charges = *chg{}.wire()
		h.TotalKobo = h.AmountKobo
	}
	return api.AuthorisePayout201JSONResponse(h), nil
}

// dbMessage: the database's own words for a refusal.
func dbMessage(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Message
	}
	return err.Error()
}

func (f *fakeAPI) ReleaseHold(ctx context.Context, req api.ReleaseHoldRequestObject) (api.ReleaseHoldResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	var society string
	err := f.db.QueryRow(ctx, `SELECT society_id::text FROM withdrawal_holds WHERE id = $1`, req.HoldId.String()).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ReleaseHold404JSONResponse(f.moneyNotFound("hold", false)), nil
	}
	if err != nil {
		return nil, err
	}
	if !f.officerHere(ctx, caller, society) {
		return api.ReleaseHold404JSONResponse(f.moneyNotFound("hold", true)), nil
	}
	if f.moneyPost(ctx, caller, society, moneyAuthorisers, f.brk == "money_any_officer_authorises") == "" {
		return api.ReleaseHold403JSONResponse(moneyNotPermittedAuthorise), nil
	}
	if req.Body == nil {
		return api.ReleaseHold400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if strings.TrimSpace(req.Body.Reason) == "" && f.brk != "money_release_no_reason" {
		return api.ReleaseHold422JSONResponse{Code: "reason_required", Message: "say why the transfer will not happen"}, nil
	}
	_, err = f.db.Exec(ctx, `INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason) VALUES ($1, $2, $3)`,
		req.HoldId.String(), caller, req.Body.Reason)
	if err != nil {
		switch c := pgConstraint(err); {
		case c == "hold_release_unpaid" && f.brk != "money_paid_release_as_500":
			return api.ReleaseHold409JSONResponse{Code: "hold_paid", Message: dbMessage(err)}, nil
		case c == "withdrawal_hold_releases_pkey":
			return api.ReleaseHold409JSONResponse{Code: "hold_released", Message: "this hold was already released"}, nil
		}
		return nil, err
	}
	h, err := f.holdView(ctx, req.HoldId.String())
	if err != nil {
		return nil, err
	}
	return api.ReleaseHold201JSONResponse(h), nil
}

// =============================================================================
// PAYOUTS
// =============================================================================

func (f *fakeAPI) RecordPayout(ctx context.Context, req api.RecordPayoutRequestObject) (api.RecordPayoutResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if !f.officerHere(ctx, caller, society) {
		var exists bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&exists)
		return api.RecordPayout404JSONResponse(f.moneyNotFound("society", exists)), nil
	}
	post := f.moneyPost(ctx, caller, society, moneyRecorders, f.brk == "money_any_officer_records")
	if post == "" {
		return api.RecordPayout403JSONResponse(moneyNotPermittedRecord), nil
	}
	b := req.Body
	if b == nil {
		return api.RecordPayout400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	amount, ok := magnitude(b.AmountKobo)
	if !ok {
		return api.RecordPayout400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	if strings.TrimSpace(b.PayoutReference) == "" && f.brk != "money_reference_optional" {
		return api.RecordPayout400JSONResponse{Code: "bad_request", Message: "a payout names its transfer reference"}, nil
	}
	if amount == 0 {
		return api.RecordPayout422JSONResponse{Code: "zero_amount", Message: "a payout of nothing is not recorded"}, nil
	}
	member := b.MemberId.String()
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !here {
		return api.RecordPayout422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	date := b.ValueDate.Format("2006-01-02")
	var c chg
	determined := true
	var hold, request *string
	if b.HoldId != nil {
		id := b.HoldId.String()
		var rq string
		err := f.db.QueryRow(ctx, `SELECT request_id::text, partner_fee_kobo, vat_kobo, statutory_levy_kobo, argon_margin_kobo
			FROM withdrawal_holds WHERE id = $1 AND society_id = $2`, id, society).Scan(&rq, &c.fee, &c.vat, &c.levy, &c.margin)
		if errors.Is(err, pgx.ErrNoRows) {
			return api.RecordPayout422JSONResponse{Code: "unknown_hold", Message: "not a hold of this society"}, nil
		}
		if err != nil {
			return nil, err
		}
		hold, request = &id, &rq
		if f.brk == "money_payout_charges_dropped" {
			c = chg{}
		}
	} else {
		var err error
		if c, determined, err = f.quoteOf(ctx, society, "withdrawal_out", amount, date); err != nil {
			return nil, err
		}
	}
	lines := []mline{{acct: f.accountByRole(ctx, society, "member_savings"), kobo: amount + c.total(), member: member},
		{acct: f.accountByRole(ctx, society, "bank"), kobo: -amount}}
	lines = append(lines, f.chargeLines(ctx, society, c)...)
	ref := b.PayoutReference
	entry, err := f.postMoney(ctx, caller, "withdrawal_payout", b.PinProofId.String(),
		moneyEntry{society: society, kind: "withdrawal_out", valueDate: date, post: post, request: request, hold: hold, ref: &ref, lines: lines})
	if err != nil {
		switch c := pgConstraint(err); {
		case moneyStepUpRefusals[c] && f.brk != "money_step_up_as_500":
			return api.RecordPayout403JSONResponse(stepUpProblem("withdrawal_payout")), nil
		case c == "payout_recorded_once" && f.brk != "money_duplicate_as_500":
			var first string
			f.db.QueryRow(ctx, `SELECT id::text FROM ledger_entries WHERE payout_reference = $1`, ref).Scan(&first)
			msg := fmt.Sprintf("transfer %s is already recorded, as entry %s", ref, first)
			if f.brk == "money_duplicate_unnamed" {
				msg = "this transfer is already recorded"
			}
			return api.RecordPayout409JSONResponse{Code: "already_recorded", Message: msg}, nil
		case c == "payout_from_hold_member" && f.brk != "money_hold_member_as_500":
			return api.RecordPayout422JSONResponse{Code: "not_the_holds_member", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	m := f.movement(ctx, caller, post, entry, "withdrawal_out", society, member, amount, b.ValueDate, c, determined, hold, &ref)
	return api.RecordPayout201JSONResponse(m), nil
}
