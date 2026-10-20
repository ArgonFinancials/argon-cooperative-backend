package api

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// RAISING AND LISTING WITHDRAWAL REQUESTS (0016). Written against the approved
// acceptance tests, test/tier1/tier1_api_requests_test.go (approved
// 2026-10-20 on their mutation evidence, without being read: finding 46).
//
//   - Raised by the member it pays, on their own step-up, or by an officer on
//     theirs for a member without a phone. The raiser is recorded and never
//     signs it (§10, "raises"; SignApproval maps the refusal).
//   - TWO LAYERS, TWO RULES, DELIBERATELY: the database allows ANY officer of
//     the society to raise for a member (raiser_is_member_or_officer); THIS
//     handler narrows that to the officers who record money, the treasurer and
//     the secretary. Remove the check below and the database will not catch
//     it: a branch officer or the chairman raising for a member will simply
//     start working (TestApiRequests05 is all that stands in the way).
//   - Everything else is the DATABASE's (0016): the raiser, the step-up, the
//     expiry (the society's window; nothing a client sends), within
//     withdrawable, a quorum that can approve it, a resubmission of the same
//     member's. Each refusal is mapped to what the person can do, carrying the
//     database's words where they explain: the figures, the arithmetic and
//     the remedy are the database's, passed through, never rewritten.
//   - The Idempotency-Key is the request's reference: a repeat answers 200
//     with the FIRST request, writes nothing, and needs no step-up.
//   - The member's list: progress and outcome, never who signed or raised it.
//     The officers' list: who raised, who signed, filterable by status.

var requestStepUpRefused = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
	"withdrawal_request_needs_step_up": true, "withdrawal_request_act_is_raisers": true, "withdrawal_request_act_kind": true,
	"withdrawal_request_act_same_transaction": true}

// requestByReference: the request already raised under this key in the
// society, in `projection`; nil if none.
func (s *server) requestByReference(ctx context.Context, society, ref, projection string) (*ApprovalView, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT id::text FROM approval_requests WHERE society_id = $1 AND reference = $2`, society, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := s.loadApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.approvalView(ctx, r, projection)
	return &v, err
}

// ownsMember: is the caller the person behind the member record?
func (s *server) ownsMember(ctx context.Context, member string) (bool, error) {
	var mine bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND user_id = $2)`, member, callerOf(ctx)).Scan(&mine)
	return mine, err
}

func (s *server) RaiseWithdrawalRequest(ctx context.Context, req RaiseWithdrawalRequestRequestObject) (RaiseWithdrawalRequestResponseObject, error) {
	society := req.SocietyId.String()
	var officer, member bool
	if err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2), EXISTS (SELECT 1 FROM members WHERE user_id = $1 AND society_id = $2)`,
		callerOf(ctx), society).Scan(&officer, &member); err != nil {
		return nil, err
	}
	if !officer && !member {
		return RaiseWithdrawalRequest404JSONResponse(notFound("society")), nil
	}
	b := req.Body
	if b == nil {
		return RaiseWithdrawalRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	forMember := b.MemberId.String()
	mine, err := s.ownsMember(ctx, forMember)
	if err != nil {
		return nil, err
	}
	// Raising for someone else: which officers may is the API's own rule (the
	// header). Whether the raiser may raise at all is the database's, mapped
	// below, not checked twice here.
	projection := "member"
	if !mine && officer {
		post, err := s.dutyPost(ctx, society, moneyRecorders)
		if err != nil {
			return nil, err
		}
		if post == "" {
			return RaiseWithdrawalRequest403JSONResponse{Code: "not_permitted", Message: "a withdrawal is raised by the member it pays, or by the treasurer or secretary for them"}, nil
		}
		projection = "officer"
	}
	// Already raised under this key: nothing is written and no step-up is
	// needed, because nothing attributable happens.
	key := req.Params.IdempotencyKey
	if v, err := s.requestByReference(ctx, society, key, projection); err != nil || v != nil {
		if err != nil {
			return nil, err
		}
		return RaiseWithdrawalRequest200JSONResponse(*v), nil
	}
	amount, ok := magnitude(string(b.AmountKobo))
	if !ok {
		return RaiseWithdrawalRequest400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	if amount == 0 {
		return RaiseWithdrawalRequest422JSONResponse{Code: "zero_amount", Message: "a withdrawal of nothing is not raised"}, nil
	}
	here, err := s.memberHere(ctx, forMember, society)
	if err != nil {
		return nil, err
	}
	if !here {
		return RaiseWithdrawalRequest422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	// The quorum is the society's, copied into the request as it is raised
	// (0011 refuses any other): the request keeps it if the society's changes.
	var quorum *int
	if err := s.db.QueryRow(ctx, `SELECT withdrawal_signatures_required FROM societies WHERE id = $1`, society).Scan(&quorum); err != nil {
		return nil, err
	}
	if quorum == nil {
		return RaiseWithdrawalRequest422JSONResponse{Code: "not_configured", Message: "this society has not decided how many signatures a withdrawal needs"}, nil
	}
	var resubmits *string
	if b.ResubmitsId != nil {
		r := b.ResubmitsId.String()
		resubmits = &r
	}
	var id string
	err = s.onStepUp(ctx, "withdrawal_request", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		// raised_at and expires_at are the database's to set (0016): none is sent.
		return x.QueryRow(ctx, `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at,
			  raised_by_user_id, act_id, resubmits_id) VALUES ($1, $2, $3, 'withdrawal_out', $4, $5, now(), $6, $7, $8) RETURNING id::text`,
			society, key, forMember, amount, *quorum, callerOf(ctx), act, resubmits).Scan(&id)
	})
	if err != nil {
		msg := dbMessage(err)
		switch c := refusedBy(err); {
		case requestStepUpRefused[c]:
			return RaiseWithdrawalRequest403JSONResponse(stepUpProblem("withdrawal_request")), nil
		case c == "raiser_is_member_or_officer":
			return RaiseWithdrawalRequest403JSONResponse{Code: "not_permitted", Message: msg}, nil
		case c == "withdrawal_request_within_withdrawable":
			return RaiseWithdrawalRequest422JSONResponse{Code: "exceeds_withdrawable", Message: msg}, nil
		case c == "request_cannot_reach_quorum":
			return RaiseWithdrawalRequest422JSONResponse{Code: "quorum_unreachable", Message: msg}, nil
		case notConfigured[c] || c == "withdrawal_quorum_unset":
			return RaiseWithdrawalRequest422JSONResponse{Code: "not_configured", Message: msg}, nil
		case c == "resubmits_a_rejected_request" || c == "resubmission_same_member":
			return RaiseWithdrawalRequest409JSONResponse{Code: "not_resubmittable", Message: msg}, nil
		case c == "approval_requests_society_id_reference_key": // a retry that raced the first
			if v, verr := s.requestByReference(ctx, society, key, projection); verr == nil && v != nil {
				return RaiseWithdrawalRequest200JSONResponse(*v), nil
			}
		}
		return nil, err
	}
	r, err := s.loadApproval(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.approvalView(ctx, r, projection)
	if err != nil {
		return nil, err
	}
	return RaiseWithdrawalRequest201JSONResponse(v), nil
}

func requestLimit(l *int) int {
	if l == nil || *l < 1 || *l > 200 {
		return 50
	}
	return *l
}

// listRequests: the requests matching `where` (args from $1), newest first, at
// most `limit`, each in `projection`. Empty is [], never null.
func (s *server) listRequests(ctx context.Context, where string, limit int, projection string, args ...any) ([]ApprovalView, error) {
	args = append(args, limit)
	rows, err := s.db.Query(ctx, `SELECT id::text FROM approval_requests WHERE `+where+
		` ORDER BY raised_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []ApprovalView{}
	for _, id := range ids {
		r, err := s.loadApproval(ctx, id)
		if err != nil {
			return nil, err
		}
		v, err := s.approvalView(ctx, r, projection)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetMemberWithdrawalRequests: the member's own, in the member projection.
// Anyone else: 404, as for a member that does not exist.
func (s *server) GetMemberWithdrawalRequests(ctx context.Context, req GetMemberWithdrawalRequestsRequestObject) (GetMemberWithdrawalRequestsResponseObject, error) {
	member := req.MemberId.String()
	mine, err := s.ownsMember(ctx, member)
	if err != nil {
		return nil, err
	}
	if !mine {
		return GetMemberWithdrawalRequests404JSONResponse(notFound("member")), nil
	}
	items, err := s.listRequests(ctx, `member_id = $1 AND kind = 'withdrawal_out'`, requestLimit(req.Params.Limit), "member", member)
	if err != nil {
		return nil, err
	}
	return GetMemberWithdrawalRequests200JSONResponse{Items: items}, nil
}

// GetSocietyApprovalRequests: the society's, in the officer projection, to a
// current officer (the auditor included: the auditor reads everything).
// Anyone else: 404, as for a society that does not exist.
func (s *server) GetSocietyApprovalRequests(ctx context.Context, req GetSocietyApprovalRequestsRequestObject) (GetSocietyApprovalRequestsResponseObject, error) {
	society := req.SocietyId.String()
	officer, err := s.officerHere(ctx, society)
	if err != nil {
		return nil, err
	}
	if !officer {
		return GetSocietyApprovalRequests404JSONResponse(notFound("society")), nil
	}
	where, args := `society_id = $1`, []any{society}
	if req.Params.Status != nil {
		where += ` AND approval_status(id)::text = $2`
		args = append(args, string(*req.Params.Status))
	}
	items, err := s.listRequests(ctx, where, requestLimit(req.Params.Limit), "officer", args...)
	if err != nil {
		return nil, err
	}
	return GetSocietyApprovalRequests200JSONResponse{Items: items}, nil
}
