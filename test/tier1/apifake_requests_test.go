//go:build tier1

// THE MUTATION VEHICLE FOR RAISING AND LISTING REQUESTS — test code, NOT the API.
//
// A reference implementation of POST /societies/{id}/withdrawal-requests,
// GET /members/{id}/withdrawal-requests and GET /societies/{id}/approval-requests
// (0016), breakable one named way per run. 0016's rules are the database's
// (the suite's base carries them); this fake maps their refusals, as the real
// handler will, and checks only what is the API's own: who may raise, the
// key, the projections.
//
// Breaks — raising:
//
//	req_anyone_raises            the database's refusal of a raiser who is neither the member nor an officer is not mapped (500)
//	req_any_officer_raises       any officer raises (the auditor, the chairman): the duty is the API's own rule
//	req_officer_member_view      an officer who raised it is answered in the member projection (no raiser shown)
//	req_not_found_says_why       a refusal to an outsider says "not permitted"
//	req_key_ignored              the Idempotency-Key is not looked up: a retry is raised again
//	req_retry_needs_step_up      a repeat is judged on its step-up before the key is looked up
//	req_key_optional             a request with no Idempotency-Key is given one
//	req_step_up_as_500           a step-up refusal answers 500
//	req_withdrawable_unexplained exceeds_withdrawable says "refused", not the figures
//	req_resubmission_as_500      a refused resubmission answers 500
//	req_raiser_shown_to_member   the member projection names who raised it
//	req_raiser_sign_as_500       the raiser signing answers 500, not conflict_of_interest
//	req_unreachable_as_500       a request no quorum can approve answers 500, not quorum_unreachable
//
// Breaks — the lists:
//
//	req_member_list_anyone       any member of the society reads another's list
//	req_member_list_officer_view the member's list is in the officer projection (who signed, the fraud path)
//	req_list_oldest_first        lists oldest first
//	req_list_null                an empty list is null
//	req_status_filter_ignored    ?status= is ignored
//	req_society_list_members     a member with no post reads the society's list
//	req_society_list_member_view the society's list is in the member projection (who raised, who signed: gone)
//	req_auditor_refused          (over-strict) the internal auditor may not read the society's list
package tier1

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
)

var reqStepUpRefusals = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
	"withdrawal_request_needs_step_up": true, "withdrawal_request_act_is_raisers": true, "withdrawal_request_act_kind": true,
	"withdrawal_request_act_same_transaction": true}

func (f *fakeAPI) reqByReference(ctx context.Context, society, ref, projection string) (*api.ApprovalView, error) {
	var id string
	err := f.db.QueryRow(ctx, `SELECT id::text FROM approval_requests WHERE society_id = $1 AND reference = $2`, society, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := f.apprLoad(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := f.apprView(ctx, r, projection)
	return &v, err
}

func (f *fakeAPI) RaiseWithdrawalRequest(ctx context.Context, req api.RaiseWithdrawalRequestRequestObject) (api.RaiseWithdrawalRequestResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	officer := f.officerHere(ctx, caller, society)
	if !officer && !f.memberOfSociety(ctx, caller, society) {
		var exists bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM societies WHERE id = $1)`, society).Scan(&exists)
		if exists && f.brk == "req_not_found_says_why" {
			return api.RaiseWithdrawalRequest404JSONResponse{Code: "not_permitted", Message: "you may not raise requests here"}, nil
		}
		return api.RaiseWithdrawalRequest404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	b := req.Body
	if b == nil {
		return api.RaiseWithdrawalRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	member := b.MemberId.String()
	// Who may raise. The member it pays, or an officer: the DATABASE's rule
	// (raiser_is_member_or_officer), mapped below, not checked twice here.
	// Which officers: the treasurer and the secretary, who record money. That
	// duty is the API's own rule, so it is checked here.
	projection := "member"
	if !f.beneficiaryOf(ctx, caller, member) && officer {
		if f.moneyPost(ctx, caller, society, moneyRecorders, f.brk == "req_any_officer_raises") == "" {
			return api.RaiseWithdrawalRequest403JSONResponse{Code: "not_permitted", Message: "a withdrawal is raised by the member it pays, or by the treasurer or secretary for them"}, nil
		}
		projection = "officer"
		if f.brk == "req_officer_member_view" {
			projection = "member"
		}
	}
	key := req.Params.IdempotencyKey
	if f.brk == "req_key_ignored" {
		key += "-" + digest()[:8]
	}
	if f.brk == "req_retry_needs_step_up" {
		var fresh bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM auth_proofs WHERE id::text = $1 AND user_id = $2 AND consumed_at IS NULL)`,
			b.PinProofId.String(), caller).Scan(&fresh)
		if !fresh {
			return api.RaiseWithdrawalRequest403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for withdrawal_request, your own"}, nil
		}
	}
	if v, err := f.reqByReference(ctx, society, key, projection); err != nil || v != nil {
		if err != nil {
			return nil, err
		}
		return api.RaiseWithdrawalRequest200JSONResponse(*v), nil
	}
	amount, ok := magnitude(b.AmountKobo)
	if !ok {
		return api.RaiseWithdrawalRequest400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	if amount == 0 {
		return api.RaiseWithdrawalRequest422JSONResponse{Code: "zero_amount", Message: "a withdrawal of nothing is not raised"}, nil
	}
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !here {
		return api.RaiseWithdrawalRequest422JSONResponse{Code: "unknown_member", Message: "not a member of this society"}, nil
	}
	var quorum *int
	f.db.QueryRow(ctx, `SELECT withdrawal_signatures_required FROM societies WHERE id = $1`, society).Scan(&quorum)
	if quorum == nil {
		return api.RaiseWithdrawalRequest422JSONResponse{Code: "not_configured", Message: "this society has not decided how many signatures a withdrawal needs"}, nil
	}
	var resub *string
	if b.ResubmitsId != nil {
		r := b.ResubmitsId.String()
		resub = &r
	}
	var id string
	err := f.actThen(ctx, caller, "withdrawal_request", b.PinProofId.String(), false, func(x pgx.Tx, act *string) error {
		// The expiry is the database's to set (0016); this one is overwritten.
		return x.QueryRow(ctx, `INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at,
			  raised_by_user_id, act_id, resubmits_id) VALUES ($1, $2, $3, 'withdrawal_out', $4, $5, now() + interval '1 day', $6, $7, $8) RETURNING id::text`,
			society, key, member, amount, *quorum, caller, act, resub).Scan(&id)
	})
	if err != nil {
		msg := dbMessage(err)
		switch c := pgConstraint(err); {
		case reqStepUpRefusals[c] && f.brk != "req_step_up_as_500":
			return api.RaiseWithdrawalRequest403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for withdrawal_request, your own"}, nil
		case c == "raiser_is_member_or_officer" && f.brk != "req_anyone_raises":
			return api.RaiseWithdrawalRequest403JSONResponse{Code: "not_permitted", Message: msg}, nil
		case c == "withdrawal_request_within_withdrawable":
			if f.brk == "req_withdrawable_unexplained" {
				msg = "refused"
			}
			return api.RaiseWithdrawalRequest422JSONResponse{Code: "exceeds_withdrawable", Message: msg}, nil
		case c == "request_cannot_reach_quorum" && f.brk != "req_unreachable_as_500":
			return api.RaiseWithdrawalRequest422JSONResponse{Code: "quorum_unreachable", Message: msg}, nil
		case notConfigured[c] || c == "withdrawal_quorum_unset":
			return api.RaiseWithdrawalRequest422JSONResponse{Code: "not_configured", Message: msg}, nil
		case (c == "resubmits_a_rejected_request" || c == "resubmission_same_member") && f.brk != "req_resubmission_as_500":
			return api.RaiseWithdrawalRequest409JSONResponse{Code: "not_resubmittable", Message: msg}, nil
		case c == "approval_requests_society_id_reference_key": // a retry that raced the first
			if v, verr := f.reqByReference(ctx, society, key, projection); verr == nil && v != nil {
				return api.RaiseWithdrawalRequest200JSONResponse(*v), nil
			}
		}
		return nil, err
	}
	r, err := f.apprLoad(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := f.apprView(ctx, r, projection)
	if err != nil {
		return nil, err
	}
	return api.RaiseWithdrawalRequest201JSONResponse(v), nil
}

// beneficiaryOf: does `user` own the member record `member`? (per PERSON)
func (f *fakeAPI) beneficiaryOf(ctx context.Context, user, member string) bool {
	var mine bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND user_id = $2)`, member, user).Scan(&mine)
	return mine
}

// listRequests: the requests matching `where` (args from $1), newest first,
// each in `projection`.
func (f *fakeAPI) listRequests(ctx context.Context, where string, limit int, projection string, args ...any) ([]api.ApprovalView, error) {
	order := "raised_at DESC, id DESC"
	if f.brk == "req_list_oldest_first" {
		order = "raised_at, id"
	}
	rows, err := f.db.Query(ctx, `SELECT id::text FROM approval_requests WHERE `+where+` ORDER BY `+order, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []api.ApprovalView{}
	if f.brk == "req_list_null" {
		out = nil
	}
	for _, id := range ids {
		r, err := f.apprLoad(ctx, id)
		if err != nil {
			return nil, err
		}
		v, err := f.apprView(ctx, r, projection)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func listLimit(l *int) int {
	if l == nil || *l < 1 || *l > 200 {
		return 50
	}
	return *l
}

func (f *fakeAPI) GetMemberWithdrawalRequests(ctx context.Context, req api.GetMemberWithdrawalRequestsRequestObject) (api.GetMemberWithdrawalRequestsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	mine := f.beneficiaryOf(ctx, caller, member)
	if !mine && f.brk == "req_member_list_anyone" {
		var society string
		f.db.QueryRow(ctx, `SELECT society_id::text FROM members WHERE id = $1`, member).Scan(&society)
		mine = society != "" && f.memberOfSociety(ctx, caller, society)
	}
	if !mine {
		return api.GetMemberWithdrawalRequests404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	projection := "member"
	if f.brk == "req_member_list_officer_view" {
		projection = "officer"
	}
	items, err := f.listRequests(ctx, `member_id = $1 AND kind = 'withdrawal_out'`, listLimit(req.Params.Limit), projection, member)
	if err != nil {
		return nil, err
	}
	return api.GetMemberWithdrawalRequests200JSONResponse{Items: items}, nil
}

func (f *fakeAPI) GetSocietyApprovalRequests(ctx context.Context, req api.GetSocietyApprovalRequestsRequestObject) (api.GetSocietyApprovalRequestsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := req.SocietyId.String()
	officer := f.officerHere(ctx, caller, society) && !(f.brk == "req_auditor_refused" && f.holdsAuditor(ctx, caller, society))
	if !officer && !(f.brk == "req_society_list_members" && f.memberOfSociety(ctx, caller, society)) {
		return api.GetSocietyApprovalRequests404JSONResponse{Code: "not_found", Message: "no such society"}, nil
	}
	where, args := `society_id = $1`, []any{society}
	if req.Params.Status != nil && f.brk != "req_status_filter_ignored" {
		where += ` AND approval_status(id)::text = $2`
		args = append(args, string(*req.Params.Status))
	}
	projection := "officer"
	if f.brk == "req_society_list_member_view" {
		projection = "member"
	}
	items, err := f.listRequests(ctx, where, listLimit(req.Params.Limit), projection, args...)
	if err != nil {
		return nil, err
	}
	return api.GetSocietyApprovalRequests200JSONResponse{Items: items}, nil
}
