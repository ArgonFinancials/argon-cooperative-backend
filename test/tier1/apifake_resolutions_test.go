//go:build tier1

// THE MUTATION VEHICLE FOR THE OFFICERS' PAYMENT ENDPOINTS — test code, NOT the
// API. A reference implementation of the wallet exceptions, the unmatched
// payments and the payment resolutions (2026-11-11), breakable one named way
// per run (ARGON_TEST_API_FAKE=<break>):
//
//	resolve_any_reader        anyone signed in reads a society's exceptions, payments and resolutions
//	resolve_auditor_blind     the internal auditor (who reads everything) is refused the exceptions
//	unmatched_claims_hidden   an unmatched payment is listed without the claims on its session
//	unmatched_credited_listed a payment already credited is listed as unmatched
//	propose_key_ignored       a repeated Idempotency-Key reaches the database (500), not the first proposal
//	open_unnamed              409 open_resolution does not name the proposal waiting
//	refusal_as_500            a proposal the database refuses (a payment already credited) answers 500
//	decided_as_500            deciding a decided proposal answers 500, not 409 not_open
//	entry_hidden              a confirmed credit answers without the contribution it posted
//	state_filter_ignored      ?state= is ignored
package tier1

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/argon/backend/internal/api"
)

func (f *fakeAPI) officerKind(ctx context.Context, caller, society string) (officer, acting bool) {
	f.db.QueryRow(ctx, `SELECT count(*) > 0, count(*) FILTER (WHERE a.post <> 'internal_auditor') > 0
		FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`,
		caller, society).Scan(&officer, &acting)
	if f.brk == "resolve_any_reader" {
		officer = true
	}
	return
}

func refusalOf(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.ConstraintName
	}
	return ""
}

var fakeNotPermitted = api.Problem{Code: "not_permitted", Message: "two officers, neither the member credited nor the claimant; the auditor does not act"}

func (f *fakeAPI) GetWalletExceptions(ctx context.Context, req api.GetWalletExceptionsRequestObject) (api.GetWalletExceptionsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	officer, acting := f.officerKind(ctx, caller, society)
	if !officer || (f.brk == "resolve_auditor_blind" && !acting) {
		return api.GetWalletExceptions404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	rows, err := f.db.Query(ctx, `SELECT kind, subject_id::text, since, detail FROM wallet_exceptions WHERE society_id = $1 ORDER BY since, subject_id`, society)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.WalletExceptionList{Exceptions: []api.WalletException{}}
	for rows.Next() {
		var e api.WalletException
		var kind, subject string
		if err := rows.Scan(&kind, &subject, &e.Since, &e.Detail); err != nil {
			return nil, err
		}
		e.Kind, e.SubjectId = api.WalletExceptionKind(kind), uuidOf(subject)
		out.Exceptions = append(out.Exceptions, e)
	}
	return api.GetWalletExceptions200JSONResponse(out), rows.Err()
}

func (f *fakeAPI) GetUnmatchedPayments(ctx context.Context, req api.GetUnmatchedPaymentsRequestObject) (api.GetUnmatchedPaymentsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if officer, _ := f.officerKind(ctx, caller, society); !officer {
		return api.GetUnmatchedPayments404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	uncredited := `AND NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)`
	if f.brk == "unmatched_credited_listed" {
		uncredited = ""
	}
	rows, err := f.db.Query(ctx, `SELECT i.id::text, i.amount_kobo, i.confirmed_at, i.session_id, i.originator_name, i.originator_bank, i.narration
		FROM vfd_inflows i WHERE i.society_id = $1 `+uncredited+` ORDER BY i.confirmed_at, i.id LIMIT $2`, society, limit)
	if err != nil {
		return nil, err
	}
	out := api.UnmatchedPaymentList{Payments: []api.UnmatchedPayment{}}
	for rows.Next() {
		var p api.UnmatchedPayment
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount, &p.ConfirmedAt, &p.SessionId, &p.SenderName, &p.SenderBank, &p.Narration); err != nil {
			rows.Close()
			return nil, err
		}
		p.InflowId, p.AmountKobo, p.Claims = uuidOf(id), api.Magnitude(strconv.FormatInt(amount, 10)), []api.UnmatchedClaim{}
		out.Payments = append(out.Payments, p)
	}
	rows.Close()
	if f.brk == "unmatched_claims_hidden" {
		return api.GetUnmatchedPayments200JSONResponse(out), nil
	}
	for k := range out.Payments {
		p := &out.Payments[k]
		if p.SessionId == nil {
			continue
		}
		cs, err := f.db.Query(ctx, `SELECT s.claim_id::text, s.member_id::text, m.full_name, m.member_number, s.amount_kobo, s.claimed_at, s.state
			FROM contribution_claim_states s JOIN members m ON m.id = s.member_id WHERE s.society_id = $1 AND s.session_id = $2 ORDER BY s.claimed_at, s.claim_id`, society, *p.SessionId)
		if err != nil {
			return nil, err
		}
		for cs.Next() {
			var c api.UnmatchedClaim
			var id, member, state string
			var amount int64
			if err := cs.Scan(&id, &member, &c.MemberName, &c.MemberNumber, &amount, &c.ClaimedAt, &state); err != nil {
				cs.Close()
				return nil, err
			}
			c.ClaimId, c.MemberId, c.AmountKobo, c.State = uuidOf(id), uuidOf(member), api.Magnitude(strconv.FormatInt(amount, 10)), api.UnmatchedClaimState(state)
			p.Claims = append(p.Claims, c)
		}
		cs.Close()
	}
	return api.GetUnmatchedPayments200JSONResponse(out), nil
}

const fakeResCols = `r.id::text, r.society_id::text, r.decision, r.inflow_id::text, r.claim_id::text, r.member_id::text,
	(SELECT full_name FROM members WHERE id = r.member_id), (SELECT amount_kobo FROM vfd_inflows WHERE id = r.inflow_id),
	r.reason, r.proposed_by::text, (SELECT full_name FROM users WHERE id = r.proposed_by), r.proposed_at,
	d.confirmed, d.decided_by::text, (SELECT full_name FROM users WHERE id = d.decided_by), d.decided_at, d.reason,
	CASE WHEN d.confirmed AND r.decision = 'credit' THEN (SELECT e.id::text FROM ledger_entries e WHERE e.vfd_inflow_id = r.inflow_id) END
	FROM payment_resolutions r LEFT JOIN payment_resolution_decisions d ON d.resolution_id = r.id`

func (f *fakeAPI) scanRes(row pgx.Row) (api.PaymentResolution, string, error) {
	var p api.PaymentResolution
	var id, society, decision, proposer string
	var inflow, claim, member, memberName, proposerName, decider, deciderName, decReason, entry *string
	var amount *int64
	var confirmed *bool
	var decidedAt *time.Time
	if err := row.Scan(&id, &society, &decision, &inflow, &claim, &member, &memberName, &amount, &p.Reason, &proposer, &proposerName,
		&p.ProposedAt, &confirmed, &decider, &deciderName, &decidedAt, &decReason, &entry); err != nil {
		return p, "", err
	}
	p.ResolutionId, p.Decision = uuidOf(id), api.PaymentResolutionDecision(decision)
	if inflow != nil {
		u := uuidOf(*inflow)
		p.InflowId = &u
	}
	if claim != nil {
		u := uuidOf(*claim)
		p.ClaimId = &u
	}
	if member != nil {
		u := uuidOf(*member)
		p.MemberId = &u
	}
	p.MemberName = memberName
	if amount != nil && decision == "credit" {
		v := strconv.FormatInt(*amount, 10)
		p.AmountKobo = &v
	}
	p.ProposedBy = api.ResolutionOfficer{UserId: uuidOf(proposer), Name: strings.TrimSpace(deref(proposerName))}
	p.State = api.PaymentResolutionState("open")
	if confirmed != nil {
		p.State = api.PaymentResolutionState("rejected")
		if *confirmed {
			p.State = api.PaymentResolutionState("confirmed")
		} else {
			p.RejectionReason = decReason
		}
		p.DecidedBy = &api.ResolutionOfficer{UserId: uuidOf(deref(decider)), Name: deref(deciderName)}
		p.DecidedAt = decidedAt
	}
	if entry != nil && f.brk != "entry_hidden" {
		u := uuidOf(*entry)
		p.EntryId = &u
	}
	return p, society, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *fakeAPI) resOf(ctx context.Context, id string) (api.PaymentResolution, string, bool, error) {
	p, society, err := f.scanRes(f.db.QueryRow(ctx, `SELECT `+fakeResCols+` WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, "", false, nil
	}
	return p, society, err == nil, err
}

func (f *fakeAPI) GetPaymentResolutions(ctx context.Context, req api.GetPaymentResolutionsRequestObject) (api.GetPaymentResolutionsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	if officer, _ := f.officerKind(ctx, caller, society); !officer {
		return api.GetPaymentResolutions404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	state := ""
	if req.Params.State != nil && f.brk != "state_filter_ignored" {
		state = string(*req.Params.State)
	}
	rows, err := f.db.Query(ctx, `SELECT `+fakeResCols+` WHERE r.society_id = $1
		AND ($2 = '' OR ($2 = 'open' AND d.id IS NULL) OR ($2 = 'confirmed' AND d.confirmed) OR ($2 = 'rejected' AND NOT d.confirmed))
		ORDER BY r.proposed_at DESC, r.id LIMIT $3`, society, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.PaymentResolutionList{Resolutions: []api.PaymentResolution{}}
	for rows.Next() {
		p, _, err := f.scanRes(rows)
		if err != nil {
			return nil, err
		}
		out.Resolutions = append(out.Resolutions, p)
	}
	return api.GetPaymentResolutions200JSONResponse(out), rows.Err()
}

// actOn: the act and the write, in one transaction, as the caller.
func (f *fakeAPI) actOn(ctx context.Context, caller, proof string, write func(pgx.Tx, string) error) error {
	x, err := f.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer x.Rollback(ctx)
	var act string
	if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('payment_resolution', $1, $2) RETURNING id`, caller, proof).Scan(&act); err != nil {
		return err
	}
	if err := write(x, act); err != nil {
		return err
	}
	return x.Commit(ctx)
}

func (f *fakeAPI) ProposePaymentResolution(ctx context.Context, req api.ProposePaymentResolutionRequestObject) (api.ProposePaymentResolutionResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	officer, acting := f.officerKind(ctx, caller, society)
	if !officer {
		return api.ProposePaymentResolution404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	key := req.Params.IdempotencyKey
	if f.brk != "propose_key_ignored" {
		var first string
		if f.db.QueryRow(ctx, `SELECT id::text FROM payment_resolutions WHERE society_id = $1 AND client_reference = $2`, society, key).Scan(&first) == nil {
			p, _, _, err := f.resOf(ctx, first)
			if err != nil {
				return nil, err
			}
			return api.ProposePaymentResolution200JSONResponse(p), nil
		}
	}
	if !acting {
		return api.ProposePaymentResolution403JSONResponse(fakeNotPermitted), nil
	}
	b := req.Body
	if b == nil {
		return api.ProposePaymentResolution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var inflow, member, claim any
	if b.InflowId != nil {
		inflow = b.InflowId.String()
	}
	if b.MemberId != nil {
		member = b.MemberId.String()
	}
	if b.ClaimId != nil {
		claim = b.ClaimId.String()
	}
	if n := len([]rune(strings.TrimSpace(b.Reason))); n < 10 || n > 500 {
		return api.ProposePaymentResolution422JSONResponse{Code: "bad_reason", Message: "a reason is 10 to 500 characters"}, nil
	}
	var id string
	err := f.actOn(ctx, caller, b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO payment_resolutions (society_id, decision, inflow_id, claim_id, member_id, reason, proposed_by, proposed_act, client_reference)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id::text`, society, string(b.Decision), inflow, claim, member, strings.TrimSpace(b.Reason), caller, act, key).Scan(&id)
	})
	if err != nil {
		if f.brk == "refusal_as_500" {
			return nil, err
		}
		switch c := refusalOf(err); c {
		case "resolution_not_beneficiary", "resolution_by_an_officer":
			return api.ProposePaymentResolution403JSONResponse(fakeNotPermitted), nil
		case "resolution_subject":
			return api.ProposePaymentResolution422JSONResponse{Code: "not_this_society", Message: err.Error()}, nil
		case "resolution_payment_uncredited":
			return api.ProposePaymentResolution409JSONResponse{Code: "already_credited", Message: err.Error()}, nil
		case "resolution_claim_open":
			return api.ProposePaymentResolution409JSONResponse{Code: "claim_closed", Message: err.Error()}, nil
		case "resolution_open_once_per_payment", "resolution_open_once_per_claim":
			p := api.Problem{Code: "open_resolution", Message: "a proposal is waiting for its second officer"}
			if f.brk != "open_unnamed" {
				var open string
				if f.db.QueryRow(ctx, `SELECT resolution_id::text FROM payment_resolutions_open WHERE inflow_id = $1 OR claim_id = $2 LIMIT 1`, inflow, claim).Scan(&open) == nil {
					u := uuidOf(open)
					p.ResolutionId = &u
				}
			}
			return api.ProposePaymentResolution409JSONResponse(p), nil
		}
		return nil, err
	}
	p, _, _, err := f.resOf(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.ProposePaymentResolution201JSONResponse(p), nil
}

func (f *fakeAPI) decideRes(ctx context.Context, id, proof string, confirmed bool, reason string) (api.PaymentResolution, int, api.Problem, error) {
	caller := ctx.Value(ctxUser{}).(string)
	p, society, found, err := f.resOf(ctx, id)
	if err != nil || !found {
		return p, 404, api.Problem{Code: "not_found", Message: "no such resolution"}, err
	}
	officer, acting := f.officerKind(ctx, caller, society)
	if !officer {
		return p, 404, api.Problem{Code: "not_found", Message: "no such resolution"}, nil
	}
	if !acting {
		return p, 403, fakeNotPermitted, nil
	}
	if p.State != "open" && f.brk != "decided_as_500" {
		return p, 409, api.Problem{Code: "not_open", Message: "decided already"}, nil
	}
	var why any
	if !confirmed {
		if n := len([]rune(strings.TrimSpace(reason))); n < 10 || n > 500 {
			return p, 422, api.Problem{Code: "bad_reason", Message: "a reason is 10 to 500 characters"}, nil
		}
		why = strings.TrimSpace(reason)
	}
	err = f.actOn(ctx, caller, proof, func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO payment_resolution_decisions (resolution_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, confirmed, caller, act, why)
		return err
	})
	if err != nil {
		switch c := refusalOf(err); c {
		case "resolution_not_beneficiary", "resolution_by_an_officer", "resolution_two_officers":
			return p, 403, fakeNotPermitted, nil
		case "resolution_decided_once":
			if f.brk == "decided_as_500" {
				return p, 0, api.Problem{}, err
			}
			return p, 409, api.Problem{Code: "not_open", Message: "decided already"}, nil
		case "resolution_payment_uncredited":
			return p, 409, api.Problem{Code: "already_credited", Message: err.Error()}, nil
		case "resolution_claim_open":
			return p, 409, api.Problem{Code: "claim_closed", Message: err.Error()}, nil
		case "resolution_posted":
			return p, 409, api.Problem{Code: "not_postable", Message: err.Error()}, nil
		}
		return p, 0, api.Problem{}, err
	}
	p, _, _, err = f.resOf(ctx, id)
	return p, 200, api.Problem{}, err
}

func (f *fakeAPI) ConfirmPaymentResolution(ctx context.Context, req api.ConfirmPaymentResolutionRequestObject) (api.ConfirmPaymentResolutionResponseObject, error) {
	p, st, prob, err := f.decideRes(ctx, req.ResolutionId.String(), req.Body.PinProofId.String(), true, "")
	switch {
	case err != nil:
		return nil, err
	case st == 403:
		return api.ConfirmPaymentResolution403JSONResponse(prob), nil
	case st == 404:
		return api.ConfirmPaymentResolution404JSONResponse(prob), nil
	case st == 409:
		return api.ConfirmPaymentResolution409JSONResponse(prob), nil
	}
	return api.ConfirmPaymentResolution200JSONResponse(p), nil
}

func (f *fakeAPI) RejectPaymentResolution(ctx context.Context, req api.RejectPaymentResolutionRequestObject) (api.RejectPaymentResolutionResponseObject, error) {
	p, st, prob, err := f.decideRes(ctx, req.ResolutionId.String(), req.Body.PinProofId.String(), false, req.Body.Reason)
	switch {
	case err != nil:
		return nil, err
	case st == 403:
		return api.RejectPaymentResolution403JSONResponse(prob), nil
	case st == 404:
		return api.RejectPaymentResolution404JSONResponse(prob), nil
	case st == 409:
		return api.RejectPaymentResolution409JSONResponse(prob), nil
	case st == 422:
		return api.RejectPaymentResolution422JSONResponse(prob), nil
	}
	return api.RejectPaymentResolution200JSONResponse(p), nil
}
