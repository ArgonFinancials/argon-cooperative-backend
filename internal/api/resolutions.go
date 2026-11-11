package api

// A PAYMENT NOBODY COULD MATCH, RESOLVED BY TWO OFFICERS (0033; LEDGER_CONTRACT
// §12, 2026-11-11). One current officer proposes, a different one confirms,
// each on their own PIN (act payment_resolution); neither is the member
// credited or the claimant declined. A credit is at VFD's confirmed amount.
// The database keeps every rule and posts the credit in the confirmation's
// transaction; this file asks, answers, and names what refused.
//
// Reading (the exceptions, the unmatched payments, the resolutions): any
// current officer, the internal auditor included. Acting: any current
// officer but the auditor.

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// actingOfficer: whether the caller holds a current post in the society, and
// whether it is one that may act (not the internal auditor).
func (s *server) actingOfficer(ctx context.Context, society string) (officer, acting bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT count(*) > 0, count(*) FILTER (WHERE a.post <> 'internal_auditor') > 0
		FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`,
		callerOf(ctx), society).Scan(&officer, &acting)
	return
}

var notPermittedResolve = Problem{Code: "not_permitted",
	Message: "a payment is resolved by two of the society's officers, neither the member it credits nor the claimant it declines; the internal auditor reads and does not act, and the proposer does not confirm their own proposal"}

func reasonOk(r string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(r))
	return n >= 10 && n <= 500
}

const badReason = "a reason is 10 to 500 characters"

// ---- reading -------------------------------------------------------------------

func (s *server) GetWalletExceptions(ctx context.Context, req GetWalletExceptionsRequestObject) (GetWalletExceptionsResponseObject, error) {
	society := req.SocietyId.String()
	if officer, _, err := s.actingOfficer(ctx, society); err != nil || !officer {
		if err != nil {
			return nil, err
		}
		return GetWalletExceptions404JSONResponse(notFound("society")), nil
	}
	rows, err := s.db.Query(ctx, `SELECT kind, subject_id::text, since, detail FROM wallet_exceptions WHERE society_id = $1 ORDER BY since, subject_id`, society)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := WalletExceptionList{Exceptions: []WalletException{}}
	for rows.Next() {
		var e WalletException
		var kind, subject string
		if err := rows.Scan(&kind, &subject, &e.Since, &e.Detail); err != nil {
			return nil, err
		}
		e.Kind, e.SubjectId = WalletExceptionKind(kind), uuidOf(subject)
		out.Exceptions = append(out.Exceptions, e)
	}
	return GetWalletExceptions200JSONResponse(out), rows.Err()
}

func (s *server) GetUnmatchedPayments(ctx context.Context, req GetUnmatchedPaymentsRequestObject) (GetUnmatchedPaymentsResponseObject, error) {
	society := req.SocietyId.String()
	if officer, _, err := s.actingOfficer(ctx, society); err != nil || !officer {
		if err != nil {
			return nil, err
		}
		return GetUnmatchedPayments404JSONResponse(notFound("society")), nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := s.db.Query(ctx, `SELECT i.id::text, i.amount_kobo, i.confirmed_at, i.session_id, i.originator_name, i.originator_bank, i.narration
		FROM vfd_inflows i WHERE i.society_id = $1 AND NOT EXISTS (SELECT 1 FROM vfd_inflow_postings p WHERE p.inflow_id = i.id)
		ORDER BY i.confirmed_at, i.id LIMIT $2`, society, limit)
	if err != nil {
		return nil, err
	}
	out := UnmatchedPaymentList{Payments: []UnmatchedPayment{}}
	for rows.Next() {
		var p UnmatchedPayment
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount, &p.ConfirmedAt, &p.SessionId, &p.SenderName, &p.SenderBank, &p.Narration); err != nil {
			rows.Close()
			return nil, err
		}
		p.InflowId, p.AmountKobo, p.Claims = uuidOf(id), magnitudeOf(amount), []UnmatchedClaim{}
		out.Payments = append(out.Payments, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k := range out.Payments {
		p := &out.Payments[k]
		if p.SessionId == nil {
			continue
		}
		cs, err := s.db.Query(ctx, `SELECT s.claim_id::text, s.member_id::text, m.full_name, m.member_number, s.amount_kobo, s.claimed_at, s.state
			FROM contribution_claim_states s JOIN members m ON m.id = s.member_id
			WHERE s.society_id = $1 AND s.session_id = $2 ORDER BY s.claimed_at, s.claim_id`, society, *p.SessionId)
		if err != nil {
			return nil, err
		}
		for cs.Next() {
			var c UnmatchedClaim
			var id, member, state string
			var amount int64
			if err := cs.Scan(&id, &member, &c.MemberName, &c.MemberNumber, &amount, &c.ClaimedAt, &state); err != nil {
				cs.Close()
				return nil, err
			}
			c.ClaimId, c.MemberId, c.AmountKobo, c.State = uuidOf(id), uuidOf(member), magnitudeOf(amount), UnmatchedClaimState(state)
			p.Claims = append(p.Claims, c)
		}
		cs.Close()
		if err := cs.Err(); err != nil {
			return nil, err
		}
	}
	return GetUnmatchedPayments200JSONResponse(out), nil
}

const resolutionColumns = `r.id::text, r.society_id::text, r.decision, r.inflow_id::text, r.claim_id::text, r.member_id::text,
	(SELECT full_name FROM members WHERE id = r.member_id), (SELECT amount_kobo FROM vfd_inflows WHERE id = r.inflow_id),
	r.reason, r.proposed_by::text, (SELECT full_name FROM users WHERE id = r.proposed_by),
	(SELECT a.post::text FROM appointments a JOIN members m ON m.id = a.member_id WHERE m.user_id = r.proposed_by AND a.society_id = r.society_id
	   AND a.held_from <= r.proposed_at::date AND (a.held_until IS NULL OR a.held_until >= r.proposed_at::date) ORDER BY a.held_from DESC LIMIT 1),
	r.proposed_at, d.confirmed, d.decided_by::text, (SELECT full_name FROM users WHERE id = d.decided_by),
	(SELECT a.post::text FROM appointments a JOIN members m ON m.id = a.member_id WHERE m.user_id = d.decided_by AND a.society_id = r.society_id
	   AND a.held_from <= d.decided_at::date AND (a.held_until IS NULL OR a.held_until >= d.decided_at::date) ORDER BY a.held_from DESC LIMIT 1),
	d.decided_at, d.reason,
	CASE WHEN d.confirmed AND r.decision = 'credit' THEN (SELECT e.id::text FROM ledger_entries e WHERE e.vfd_inflow_id = r.inflow_id) END`

const resolutionFrom = ` FROM payment_resolutions r LEFT JOIN payment_resolution_decisions d ON d.resolution_id = r.id`

func scanResolution(row pgx.Row) (PaymentResolution, string, error) {
	var p PaymentResolution
	var id, society, decision, proposer string
	var inflow, claim, member, memberName, proposerName, proposerPost, decider, deciderName, deciderPost, decReason, entry *string
	var amount *int64
	var confirmed *bool
	var decidedAt *time.Time
	if err := row.Scan(&id, &society, &decision, &inflow, &claim, &member, &memberName, &amount, &p.Reason, &proposer, &proposerName, &proposerPost,
		&p.ProposedAt, &confirmed, &decider, &deciderName, &deciderPost, &decidedAt, &decReason, &entry); err != nil {
		return p, "", err
	}
	p.ResolutionId, p.Decision = uuidOf(id), PaymentResolutionDecision(decision)
	p.InflowId, p.ClaimId, p.MemberId, p.MemberName = uuidPtr(inflow), uuidPtr(claim), uuidPtr(member), memberName
	if amount != nil && decision == "credit" {
		v := string(magnitudeOf(*amount))
		p.AmountKobo = &v
	}
	p.ProposedBy = ResolutionOfficer{UserId: uuidOf(proposer), Name: deref(proposerName), Post: proposerPost}
	p.State = PaymentResolutionState("open")
	if confirmed != nil {
		p.State = PaymentResolutionState("rejected")
		if *confirmed {
			p.State = PaymentResolutionState("confirmed")
		} else {
			p.RejectionReason = decReason
		}
		p.DecidedBy = &ResolutionOfficer{UserId: uuidOf(deref(decider)), Name: deref(deciderName), Post: deciderPost}
		p.DecidedAt = decidedAt
	}
	p.EntryId = uuidPtr(entry)
	return p, society, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *server) resolutionOf(ctx context.Context, id string) (PaymentResolution, string, bool, error) {
	p, society, err := scanResolution(s.db.QueryRow(ctx, `SELECT `+resolutionColumns+resolutionFrom+` WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, "", false, nil
	}
	return p, society, err == nil, err
}

func (s *server) GetPaymentResolutions(ctx context.Context, req GetPaymentResolutionsRequestObject) (GetPaymentResolutionsResponseObject, error) {
	society := req.SocietyId.String()
	if officer, _, err := s.actingOfficer(ctx, society); err != nil || !officer {
		if err != nil {
			return nil, err
		}
		return GetPaymentResolutions404JSONResponse(notFound("society")), nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	state := ""
	if req.Params.State != nil {
		state = string(*req.Params.State)
	}
	rows, err := s.db.Query(ctx, `SELECT `+resolutionColumns+resolutionFrom+` WHERE r.society_id = $1
		AND ($2 = '' OR ($2 = 'open' AND d.id IS NULL) OR ($2 = 'confirmed' AND d.confirmed) OR ($2 = 'rejected' AND NOT d.confirmed))
		ORDER BY r.proposed_at DESC, r.id LIMIT $3`, society, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := PaymentResolutionList{Resolutions: []PaymentResolution{}}
	for rows.Next() {
		p, _, err := scanResolution(rows)
		if err != nil {
			return nil, err
		}
		out.Resolutions = append(out.Resolutions, p)
	}
	return GetPaymentResolutions200JSONResponse(out), rows.Err()
}

// ---- acting --------------------------------------------------------------------

func (s *server) resolutionByKey(ctx context.Context, society, key string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT id::text FROM payment_resolutions WHERE society_id = $1 AND client_reference = $2`, society, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// openOn: the open proposal on this payment or claim, if any.
func (s *server) openOn(ctx context.Context, inflow, claim *openapi_types.UUID) *openapi_types.UUID {
	var id string
	var in, cl any
	if inflow != nil {
		in = inflow.String()
	}
	if claim != nil {
		cl = claim.String()
	}
	if err := s.db.QueryRow(ctx, `SELECT resolution_id::text FROM payment_resolutions_open WHERE inflow_id = $1 OR claim_id = $2 LIMIT 1`, in, cl).Scan(&id); err != nil {
		return nil
	}
	u := uuidOf(id)
	return &u
}

var resolutionStepUpRefused = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true, "resolution_act": true}

func (s *server) ProposePaymentResolution(ctx context.Context, req ProposePaymentResolutionRequestObject) (ProposePaymentResolutionResponseObject, error) {
	society := req.SocietyId.String()
	officer, acting, err := s.actingOfficer(ctx, society)
	if err != nil {
		return nil, err
	}
	if !officer {
		return ProposePaymentResolution404JSONResponse(notFound("society")), nil
	}
	key := req.Params.IdempotencyKey
	if first, err := s.resolutionByKey(ctx, society, key); err != nil || first != "" {
		if err != nil {
			return nil, err
		}
		p, _, _, err := s.resolutionOf(ctx, first)
		if err != nil {
			return nil, err
		}
		return ProposePaymentResolution200JSONResponse(p), nil
	}
	if !acting {
		return ProposePaymentResolution403JSONResponse(notPermittedResolve), nil
	}
	b := req.Body
	if b == nil {
		return ProposePaymentResolution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var inflow, member, claim any
	switch b.Decision {
	case "credit":
		if b.InflowId == nil || b.MemberId == nil {
			return ProposePaymentResolution400JSONResponse{Code: "bad_request", Message: "a credit names the payment (inflowId) and the member (memberId)"}, nil
		}
		inflow, member = b.InflowId.String(), b.MemberId.String()
		if b.ClaimId != nil {
			claim = b.ClaimId.String()
		}
	case "decline":
		if b.ClaimId == nil || b.InflowId != nil || b.MemberId != nil {
			return ProposePaymentResolution400JSONResponse{Code: "bad_request", Message: "a decline names one claim (claimId) and nothing else"}, nil
		}
		claim = b.ClaimId.String()
	default:
		return ProposePaymentResolution400JSONResponse{Code: "bad_request", Message: "decision is credit or decline"}, nil
	}
	if !reasonOk(b.Reason) {
		return ProposePaymentResolution422JSONResponse{Code: "bad_reason", Message: badReason}, nil
	}
	var id string
	err = s.onStepUp(ctx, "payment_resolution", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO payment_resolutions (society_id, decision, inflow_id, claim_id, member_id, reason, proposed_by, proposed_act, client_reference)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id::text`,
			society, string(b.Decision), inflow, claim, member, strings.TrimSpace(b.Reason), callerOf(ctx), act, key).Scan(&id)
	})
	if err != nil {
		switch c := refusedBy(err); {
		case resolutionStepUpRefused[c]:
			return ProposePaymentResolution403JSONResponse(stepUpProblem("payment_resolution")), nil
		case c == "resolution_not_beneficiary" || c == "resolution_by_an_officer":
			return ProposePaymentResolution403JSONResponse(notPermittedResolve), nil
		case c == "resolution_subject":
			return ProposePaymentResolution422JSONResponse{Code: "not_this_society", Message: dbMessage(err)}, nil
		case c == "resolution_says_why":
			return ProposePaymentResolution422JSONResponse{Code: "bad_reason", Message: badReason}, nil
		case c == "resolution_payment_uncredited":
			return ProposePaymentResolution409JSONResponse{Code: "already_credited", Message: dbMessage(err)}, nil
		case c == "resolution_claim_open":
			return ProposePaymentResolution409JSONResponse{Code: "claim_closed", Message: dbMessage(err)}, nil
		case c == "resolution_open_once_per_payment" || c == "resolution_open_once_per_claim":
			return ProposePaymentResolution409JSONResponse{Code: "open_resolution",
				Message: "a proposal on this is waiting for its second officer: confirm or reject that one", ResolutionId: s.openOn(ctx, b.InflowId, b.ClaimId)}, nil
		case c == "resolution_recorded_once": // a retry that raced the first
			if first, ferr := s.resolutionByKey(ctx, society, key); ferr == nil && first != "" {
				if p, _, ok, perr := s.resolutionOf(ctx, first); perr == nil && ok {
					return ProposePaymentResolution200JSONResponse(p), nil
				}
			}
		}
		return nil, err
	}
	p, _, _, err := s.resolutionOf(ctx, id)
	if err != nil {
		return nil, err
	}
	return ProposePaymentResolution201JSONResponse(p), nil
}

// decide: a confirmation (reason "") or a rejection, on the caller's own act.
// It answers the resolution, or the problem's status and body.
func (s *server) decide(ctx context.Context, id, proof string, confirmed bool, reason string) (PaymentResolution, int, Problem, error) {
	p, society, found, err := s.resolutionOf(ctx, id)
	if err != nil {
		return p, 0, Problem{}, err
	}
	if !found {
		return p, 404, notFound("resolution"), nil
	}
	officer, acting, err := s.actingOfficer(ctx, society)
	if err != nil {
		return p, 0, Problem{}, err
	}
	if !officer {
		return p, 404, notFound("resolution"), nil
	}
	if !acting {
		return p, 403, notPermittedResolve, nil
	}
	if p.State != "open" {
		return p, 409, Problem{Code: "not_open", Message: "this proposal was " + string(p.State) + " already"}, nil
	}
	if !confirmed && !reasonOk(reason) {
		return p, 422, Problem{Code: "bad_reason", Message: badReason}, nil
	}
	var why any
	if !confirmed {
		why = strings.TrimSpace(reason)
	}
	err = s.onStepUp(ctx, "payment_resolution", proof, func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO payment_resolution_decisions (resolution_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, confirmed, callerOf(ctx), act, why)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case resolutionStepUpRefused[c]:
			return p, 403, stepUpProblem("payment_resolution"), nil
		case c == "resolution_not_beneficiary" || c == "resolution_by_an_officer" || c == "resolution_two_officers":
			return p, 403, notPermittedResolve, nil
		case c == "resolution_decided_once":
			return p, 409, Problem{Code: "not_open", Message: "this proposal was decided already"}, nil
		case c == "resolution_payment_uncredited":
			return p, 409, Problem{Code: "already_credited", Message: dbMessage(err)}, nil
		case c == "resolution_claim_open":
			return p, 409, Problem{Code: "claim_closed", Message: dbMessage(err) + "; reject this proposal to clear it"}, nil
		case c == "resolution_posted":
			return p, 409, Problem{Code: "not_postable", Message: dbMessage(err)}, nil
		case c == "resolution_says_why":
			return p, 422, Problem{Code: "bad_reason", Message: badReason}, nil
		}
		return p, 0, Problem{}, err
	}
	p, _, _, err = s.resolutionOf(ctx, id)
	return p, 200, Problem{}, err
}

func (s *server) ConfirmPaymentResolution(ctx context.Context, req ConfirmPaymentResolutionRequestObject) (ConfirmPaymentResolutionResponseObject, error) {
	if req.Body == nil {
		return ConfirmPaymentResolution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	p, status, prob, err := s.decide(ctx, req.ResolutionId.String(), req.Body.PinProofId.String(), true, "")
	switch {
	case err != nil:
		return nil, err
	case status == 403:
		return ConfirmPaymentResolution403JSONResponse(prob), nil
	case status == 404:
		return ConfirmPaymentResolution404JSONResponse(prob), nil
	case status == 409:
		return ConfirmPaymentResolution409JSONResponse(prob), nil
	}
	return ConfirmPaymentResolution200JSONResponse(p), nil
}

func (s *server) RejectPaymentResolution(ctx context.Context, req RejectPaymentResolutionRequestObject) (RejectPaymentResolutionResponseObject, error) {
	if req.Body == nil {
		return RejectPaymentResolution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	p, status, prob, err := s.decide(ctx, req.ResolutionId.String(), req.Body.PinProofId.String(), false, req.Body.Reason)
	switch {
	case err != nil:
		return nil, err
	case status == 403:
		return RejectPaymentResolution403JSONResponse(prob), nil
	case status == 404:
		return RejectPaymentResolution404JSONResponse(prob), nil
	case status == 409:
		return RejectPaymentResolution409JSONResponse(prob), nil
	case status == 422:
		return RejectPaymentResolution422JSONResponse(prob), nil
	}
	return RejectPaymentResolution200JSONResponse(p), nil
}
