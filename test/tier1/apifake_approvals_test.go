//go:build tier1

// THE MUTATION VEHICLE FOR THE APPROVAL ENDPOINT TESTS — test code, NOT the API.
//
// A reference implementation of GET /approvals/{id}, POST …/signatures and
// POST …/rejections (PRD 8.4; 0007, 0010), breakable one named way per run.
//
// Breaks — who sees what:
//
//	appr_officer_unnamed          the officer projection leaves out who signed
//	appr_auditor_refused          (over-strict) the internal auditor may not read
//	appr_member_sees_names        the member projection lists who signed
//	appr_member_sees_rejecter     the member projection names who rejected
//	appr_fraud_path_shown         the member is told a request was rejected as looks_wrong
//	appr_any_member_reads         any member of the society reads any request
//	appr_officer_any_society      an officer of ANY society reads
//	appr_not_found_says_why       a refusal says "not permitted"
//
// Breaks — signing and rejecting:
//
//	appr_sign_without_step_up     no proof: signs anyway, with no act
//	appr_failed_step_up_signs     a proof the database refuses (spent): signs anyway, with no act
//	appr_status_stale             the answer to a signature carries the status from before it
//	appr_not_signatory_as_404     a refused non-signatory is told "no such request"
//	appr_closed_as_403            signing a closed request reads as a step-up problem
//	appr_already_signed_as_closed signing twice reads as "closed"
//	appr_reject_without_step_up   no proof: rejects anyway, with no act
//	appr_rejection_note_dropped   the officer projection drops the rejection's note
//	appr_note_refusal_as_500      looks_wrong with no note answers 500, not 422 note_required
//	appr_signed_reject_as_closed  a signer rejecting reads as "closed", not signed_cannot_reject
//
// Shared: money_as_numbers.
package tier1

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/argon/backend/internal/api"
)

type apprRow struct {
	id, society          string
	kind                 string
	amount               int64
	member, loan, reason *string
	required             int
	raised, expires      time.Time
}

func (f *fakeAPI) apprLoad(ctx context.Context, id string) (apprRow, error) {
	var r apprRow
	err := f.db.QueryRow(ctx, `SELECT id::text, society_id::text, kind::text, amount_kobo, member_id::text, loan_id::text, reason,
		       required, raised_at, expires_at FROM approval_requests WHERE id = $1`, id).
		Scan(&r.id, &r.society, &r.kind, &r.amount, &r.member, &r.loan, &r.reason, &r.required, &r.raised, &r.expires)
	return r, err
}

// apprProjection: "officer" (a current post in the society), "member" (the
// request's member), or "" (neither).
func (f *fakeAPI) apprProjection(ctx context.Context, caller string, r apprRow) string {
	if f.currentOfficer(ctx, caller, r.society) && !(f.brk == "appr_auditor_refused" && f.holdsAuditor(ctx, caller, r.society)) {
		return "officer"
	}
	if f.brk == "appr_officer_any_society" {
		var any bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
			WHERE m.user_id = $1 AND (a.held_until IS NULL OR a.held_until >= current_date))`, caller).Scan(&any)
		if any {
			return "officer"
		}
	}
	if r.member != nil {
		var mine bool
		f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND user_id = $2)`, *r.member, caller).Scan(&mine)
		if mine {
			return "member"
		}
	}
	if f.brk == "appr_any_member_reads" && f.memberOfSociety(ctx, caller, r.society) {
		return "member"
	}
	return ""
}

func (f *fakeAPI) holdsAuditor(ctx context.Context, user, society string) bool {
	var ok bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.post = 'internal_auditor' AND (a.held_until IS NULL OR a.held_until >= current_date))`,
		user, society).Scan(&ok)
	return ok
}

func (f *fakeAPI) apprNotFound(exists bool) api.Problem {
	if exists && f.brk == "appr_not_found_says_why" {
		return api.Problem{Code: "not_permitted", Message: "you may not read this request"}
	}
	return api.Problem{Code: "not_found", Message: "no such request"}
}

func (f *fakeAPI) apprView(ctx context.Context, r apprRow, projection string) (api.ApprovalView, error) {
	v := api.ApprovalView{RequestId: uuidOf(r.id), SocietyId: uuidOf(r.society), Kind: api.MovementKind(r.kind),
		AmountKobo: api.Magnitude(itoa(r.amount)), MemberId: uuidPtr(r.member), LoanId: uuidPtr(r.loan), Reason: r.reason,
		Required: r.required, RaisedAt: r.raised, ExpiresAt: r.expires, Projection: api.ApprovalViewProjection(projection)}
	var st string
	if err := f.db.QueryRow(ctx, `SELECT approval_status($1)::text`, r.id).Scan(&st); err != nil {
		return v, err
	}
	v.Status = api.ApprovalStatus(st)
	f.db.QueryRow(ctx, `SELECT count(DISTINCT user_id) FROM signatory_approvals WHERE request_id = $1`, r.id).Scan(&v.SignedCount)
	if projection == "officer" && f.brk != "appr_officer_unnamed" || projection == "member" && f.brk == "appr_member_sees_names" {
		rows, err := f.db.Query(ctx, `SELECT user_id::text, member_id::text, post_at_signing::text, signed_at
			FROM signatory_approvals WHERE request_id = $1 ORDER BY signed_at, id`, r.id)
		if err != nil {
			return v, err
		}
		sigs := []api.ApprovalSignature{}
		for rows.Next() {
			var s api.ApprovalSignature
			var u, m, post string
			if err := rows.Scan(&u, &m, &post, &s.SignedAt); err != nil {
				rows.Close()
				return v, err
			}
			s.UserId, s.MemberId, s.Post = uuidOf(u), uuidOf(m), api.OfficerPost(post)
			sigs = append(sigs, s)
		}
		rows.Close()
		v.Signatures = &sigs
	}
	var rj api.ApprovalRejection
	var reason, by, post string
	err := f.db.QueryRow(ctx, `SELECT reason, note, rejected_at, user_id::text, post_at_rejection::text
		FROM approval_rejections WHERE request_id = $1 LIMIT 1`, r.id).Scan(&reason, &rj.Note, &rj.RejectedAt, &by, &post)
	if err == nil {
		rj.Reason = api.RejectionReason(reason)
		fraud := reason == "looks_wrong"
		switch {
		case projection == "officer":
			u, p := uuidOf(by), api.OfficerPost(post)
			rj.ByUserId, rj.ByPost = &u, &p
			if f.brk == "appr_rejection_note_dropped" {
				rj.Note = nil
			}
			v.Rejection, v.Escalated = &rj, fraud
		case fraud && f.brk != "appr_fraud_path_shown":
			v.Status, v.Rejection = "referred", nil // the fraud path does not go back to the requester
		default:
			if f.brk == "appr_member_sees_rejecter" {
				u, p := uuidOf(by), api.OfficerPost(post)
				rj.ByUserId, rj.ByPost = &u, &p
			}
			v.Rejection = &rj
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	return v, nil
}

func itoa(v int64) string { return string(koboOf(v)) }

func (f *fakeAPI) GetApproval(ctx context.Context, req api.GetApprovalRequestObject) (api.GetApprovalResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	r, err := f.apprLoad(ctx, req.RequestId.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return api.GetApproval404JSONResponse(f.apprNotFound(false)), nil
	}
	if err != nil {
		return nil, err
	}
	proj := f.apprProjection(ctx, caller, r)
	if proj == "" {
		return api.GetApproval404JSONResponse(f.apprNotFound(true)), nil
	}
	v, err := f.apprView(ctx, r, proj)
	if err != nil {
		return nil, err
	}
	return api.GetApproval200JSONResponse(v), nil
}

func pgConstraint(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.ConstraintName != "" {
			return pe.ConstraintName
		}
		return pe.Code
	}
	return ""
}

var stepUpRefusals = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
	"signature_needs_step_up": true, "signature_act_is_signers": true, "signature_act_kind": true,
	"rejection_needs_step_up": true, "rejection_act_is_rejecters": true, "rejection_act_kind": true}

// actThen: in ONE transaction, the caller's step-up act of `kind` spending
// `proof`, then `write` with the act's id. `bypass` skips the act (breaks).
func (f *fakeAPI) actThen(ctx context.Context, caller, kind, proof string, bypass bool, write func(pgx.Tx, *string) error) error {
	x, err := f.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer x.Rollback(ctx)
	var act *string
	if !bypass {
		var id string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			kind, caller, proof).Scan(&id); err != nil {
			return err
		}
		act = &id
	}
	if err := write(x, act); err != nil {
		return err
	}
	return x.Commit(ctx)
}

func (f *fakeAPI) SignApproval(ctx context.Context, req api.SignApprovalRequestObject) (api.SignApprovalResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	r, err := f.apprLoad(ctx, req.RequestId.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return api.SignApproval404JSONResponse(f.apprNotFound(false)), nil
	}
	if err != nil {
		return nil, err
	}
	if f.apprProjection(ctx, caller, r) != "officer" {
		return api.SignApproval404JSONResponse(f.apprNotFound(true)), nil
	}
	var before api.ApprovalView
	if f.brk == "appr_status_stale" {
		before, _ = f.apprView(ctx, r, "officer")
	}
	proof := req.Body.PinProofId.String()
	// The signer's member record in this society that holds a current post.
	var member, post string
	f.db.QueryRow(ctx, `SELECT m.id::text, a.post::text FROM members m JOIN appointments a ON a.member_id = m.id
		WHERE m.user_id = $1 AND a.society_id = $2 AND (a.held_until IS NULL OR a.held_until >= current_date)
		ORDER BY a.is_signatory DESC LIMIT 1`, caller, r.society).Scan(&member, &post)
	write := func(x pgx.Tx, act *string) error {
		_, err := x.Exec(ctx, `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, $3, $4)`,
			r.id, member, post, act)
		return err
	}
	err = f.actThen(ctx, caller, "approval_signature", proof, f.brk == "appr_sign_without_step_up", write)
	if err != nil && stepUpRefusals[pgConstraint(err)] && f.brk == "appr_failed_step_up_signs" {
		err = f.actThen(ctx, caller, "", "", true, write)
	}
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c]:
			return api.SignApproval403JSONResponse(api.Problem{Code: "step_up_required", Message: "sign with a fresh PIN proof for approval_signature, your own"}), nil
		case c == "signatory_holds_signing_post" || c == "signatory_not_auditor" || c == "signatory_has_user":
			if f.brk == "appr_not_signatory_as_404" {
				return api.SignApproval404JSONResponse(f.apprNotFound(false)), nil
			}
			return api.SignApproval403JSONResponse(api.Problem{Code: "not_a_signatory", Message: "you hold no current signing post in this society"}), nil
		case c == "borrower_never_signs_own_loan" || c == "signatory_not_guarantor":
			return api.SignApproval403JSONResponse(api.Problem{Code: "conflict_of_interest", Message: "the borrower and the guarantors of a loan never sign its requests"}), nil
		case c == "signature_before_expiry" || c == "signature_on_open_request":
			if f.brk == "appr_closed_as_403" {
				return api.SignApproval403JSONResponse(api.Problem{Code: "step_up_required", Message: "sign again"}), nil
			}
			return api.SignApproval409JSONResponse(api.Problem{Code: "request_closed", Message: "this request has expired or been rejected"}), nil
		case c == "signatory_approvals_one_per_user":
			code := "already_signed"
			if f.brk == "appr_already_signed_as_closed" {
				code = "request_closed"
			}
			return api.SignApproval409JSONResponse(api.Problem{Code: code, Message: "you have already signed this request"}), nil
		}
		return nil, err
	}
	if f.brk == "appr_status_stale" {
		before.SignedCount++
		return api.SignApproval201JSONResponse(before), nil
	}
	v, err := f.apprView(ctx, r, "officer")
	if err != nil {
		return nil, err
	}
	return api.SignApproval201JSONResponse(v), nil
}

func (f *fakeAPI) RejectApproval(ctx context.Context, req api.RejectApprovalRequestObject) (api.RejectApprovalResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	r, err := f.apprLoad(ctx, req.RequestId.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return api.RejectApproval404JSONResponse(f.apprNotFound(false)), nil
	}
	if err != nil {
		return nil, err
	}
	if f.apprProjection(ctx, caller, r) != "officer" {
		return api.RejectApproval404JSONResponse(f.apprNotFound(true)), nil
	}
	var member, post string
	f.db.QueryRow(ctx, `SELECT m.id::text, a.post::text FROM members m JOIN appointments a ON a.member_id = m.id
		WHERE m.user_id = $1 AND a.society_id = $2 AND (a.held_until IS NULL OR a.held_until >= current_date)
		ORDER BY a.is_signatory DESC LIMIT 1`, caller, r.society).Scan(&member, &post)
	err = f.actThen(ctx, caller, "approval_rejection", req.Body.PinProofId.String(), f.brk == "appr_reject_without_step_up",
		func(x pgx.Tx, act *string) error {
			_, err := x.Exec(ctx, `INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, note, act_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, r.id, member, caller, post, string(req.Body.Reason), req.Body.Note, act)
			return err
		})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c]:
			return api.RejectApproval403JSONResponse(api.Problem{Code: "step_up_required", Message: "reject with a fresh PIN proof for approval_rejection, your own"}), nil
		case c == "rejecter_holds_signing_post" || c == "rejecter_user_is_member_user":
			return api.RejectApproval403JSONResponse(api.Problem{Code: "not_a_signatory", Message: "only a current signatory rejects"}), nil
		case c == "rejection_note_for_looks_wrong":
			if f.brk == "appr_note_refusal_as_500" {
				return nil, err
			}
			return api.RejectApproval422JSONResponse(api.Problem{Code: "note_required", Message: "say what looks wrong: it goes to the committee and the auditor"}), nil
		case c == "rejection_on_open_request":
			return api.RejectApproval409JSONResponse(api.Problem{Code: "request_closed", Message: "this request is no longer open"}), nil
		case c == "rejecter_has_not_signed":
			code := "signed_cannot_reject"
			if f.brk == "appr_signed_reject_as_closed" {
				code = "request_closed"
			}
			return api.RejectApproval409JSONResponse(api.Problem{Code: code, Message: "you signed this request; a signatory does not also reject it"}), nil
		}
		return nil, err
	}
	v, err := f.apprView(ctx, r, "officer")
	if err != nil {
		return nil, err
	}
	return api.RejectApproval201JSONResponse(v), nil
}
