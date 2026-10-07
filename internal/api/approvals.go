package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// THE APPROVAL ENDPOINTS (PRD 8.4; 0007, 0010). Written against the approved
// acceptance tests, test/tier1/tier1_api_approvals_test.go.
//
//   - The status is DERIVED by the database (approval_status), never stored.
//   - One record, two projections. Officers (a current post in the society,
//     the auditor included) see who signed and who rejected. The request's
//     member sees progress and why, never who. On the FRAUD PATH (looks_wrong)
//     the member sees "referred" and what to do next, and nothing else: it
//     does not go back to the requester, who may be part of it.
//   - Anyone else: 404, identical to a request that does not exist.
//   - Signing and rejecting rest on the person's own PIN step-up: the act and
//     the signature are written in ONE transaction, and the database's rules
//     (0002, 0007–0010) decide. Each refusal is mapped to what the person can
//     do about it.

const referredNextStep = "Speak to your secretary."

var approvalNotFound = Problem{Code: "not_found", Message: "no such request"}

type approvalRow struct {
	id, society          string
	kind                 string
	amount               int64
	member, loan, reason *string
	required             int
	raised, expires      time.Time
}

func (s *server) loadApproval(ctx context.Context, id string) (approvalRow, error) {
	var r approvalRow
	err := s.db.QueryRow(ctx, `SELECT id::text, society_id::text, kind::text, amount_kobo, member_id::text, loan_id::text, reason,
		       required, raised_at, expires_at FROM approval_requests WHERE id = $1`, id).
		Scan(&r.id, &r.society, &r.kind, &r.amount, &r.member, &r.loan, &r.reason, &r.required, &r.raised, &r.expires)
	return r, err
}

// projectionFor: "officer" (a current post in the request's society),
// "member" (the request's own member), or "" (neither: 404).
func (s *server) projectionFor(ctx context.Context, r approvalRow) (string, error) {
	var officer, member bool
	err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2),
		       EXISTS (SELECT 1 FROM members WHERE id = $3 AND user_id = $1)`, callerOf(ctx), r.society, r.member).
		Scan(&officer, &member)
	switch {
	case err != nil:
		return "", err
	case officer:
		return "officer", nil
	case member:
		return "member", nil
	}
	return "", nil
}

func (s *server) approvalView(ctx context.Context, r approvalRow, projection string) (ApprovalView, error) {
	v := ApprovalView{RequestId: uuidOf(r.id), SocietyId: uuidOf(r.society), Kind: MovementKind(r.kind),
		AmountKobo: Magnitude(koboOf(r.amount)), MemberId: uuidPtr(r.member), LoanId: uuidPtr(r.loan), Reason: r.reason,
		Required: r.required, RaisedAt: r.raised, ExpiresAt: r.expires, Projection: ApprovalViewProjection(projection)}
	var st string
	if err := s.db.QueryRow(ctx, `SELECT approval_status($1)::text, (SELECT count(DISTINCT user_id) FROM signatory_approvals WHERE request_id = $1)`,
		r.id).Scan(&st, &v.SignedCount); err != nil {
		return v, err
	}
	v.Status = ApprovalStatus(st)
	if projection == "officer" {
		rows, err := s.db.Query(ctx, `SELECT user_id::text, member_id::text, post_at_signing::text, signed_at
			FROM signatory_approvals WHERE request_id = $1 ORDER BY signed_at, id`, r.id)
		if err != nil {
			return v, err
		}
		sigs := []ApprovalSignature{}
		for rows.Next() {
			var sg ApprovalSignature
			var u, m, post string
			if err := rows.Scan(&u, &m, &post, &sg.SignedAt); err != nil {
				rows.Close()
				return v, err
			}
			sg.UserId, sg.MemberId, sg.Post = uuidOf(u), uuidOf(m), OfficerPost(post)
			sigs = append(sigs, sg)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return v, err
		}
		v.Signatures = &sigs
	}
	var rj ApprovalRejection
	var reason, by, post string
	err := s.db.QueryRow(ctx, `SELECT reason, note, rejected_at, user_id::text, post_at_rejection::text
		FROM approval_rejections WHERE request_id = $1 LIMIT 1`, r.id).Scan(&reason, &rj.Note, &rj.RejectedAt, &by, &post)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	rj.Reason = RejectionReason(reason)
	fraud := reason == "looks_wrong"
	switch {
	case projection == "officer":
		u, p := uuidOf(by), OfficerPost(post)
		rj.ByUserId, rj.ByPost = &u, &p
		v.Rejection, v.Escalated = &rj, fraud
	case fraud:
		ns := referredNextStep
		v.Status, v.NextStep = "referred", &ns
	default:
		v.Rejection = &rj // why, never by whom
	}
	return v, nil
}

func refusedBy(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		if pe.ConstraintName != "" {
			return pe.ConstraintName
		}
		return pe.Code
	}
	return ""
}

// The person's step-up did not hold: no proof, someone else's, spent, stale,
// for another act, or minted in this transaction.
var stepUpRefused = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true,
	"signature_needs_step_up": true, "signature_act_is_signers": true, "signature_act_kind": true,
	"rejection_needs_step_up": true, "rejection_act_is_rejecters": true, "rejection_act_kind": true}

// onStepUp: in ONE transaction, the caller's act of `kind` spending `proof`,
// then `write` with the act's id.
func (s *server) onStepUp(ctx context.Context, kind, proof string, write func(pgx.Tx, string) error) error {
	x, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer x.Rollback(ctx)
	var act string
	if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
		kind, callerOf(ctx), proof).Scan(&act); err != nil {
		return err
	}
	if err := write(x, act); err != nil {
		return err
	}
	return x.Commit(ctx)
}

// signingRecord: the caller's member record in the society holding a current
// post, a signing one first. None: empty, and the database refuses the write.
func (s *server) signingRecord(ctx context.Context, society string) (member, post string, err error) {
	err = s.db.QueryRow(ctx, `SELECT m.id::text, a.post::text FROM members m JOIN appointments a ON a.member_id = m.id
		WHERE m.user_id = $1 AND a.society_id = $2 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)
		ORDER BY a.is_signatory DESC, a.held_from DESC LIMIT 1`, callerOf(ctx), society).Scan(&member, &post)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return member, post, err
}

// openForOfficer loads a request the caller may act on: 404 for a request
// that does not exist or one they may not read; an officer projection only.
func (s *server) openForOfficer(ctx context.Context, id string) (approvalRow, bool, error) {
	r, err := s.loadApproval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	proj, err := s.projectionFor(ctx, r)
	return r, proj == "officer", err
}

func (s *server) GetApproval(ctx context.Context, req GetApprovalRequestObject) (GetApprovalResponseObject, error) {
	r, err := s.loadApproval(ctx, req.RequestId.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return GetApproval404JSONResponse(approvalNotFound), nil
	}
	if err != nil {
		return nil, err
	}
	proj, err := s.projectionFor(ctx, r)
	if err != nil {
		return nil, err
	}
	if proj == "" {
		return GetApproval404JSONResponse(approvalNotFound), nil
	}
	v, err := s.approvalView(ctx, r, proj)
	if err != nil {
		return nil, err
	}
	return GetApproval200JSONResponse(v), nil
}

func (s *server) SignApproval(ctx context.Context, req SignApprovalRequestObject) (SignApprovalResponseObject, error) {
	r, ok, err := s.openForOfficer(ctx, req.RequestId.String())
	if err != nil {
		return nil, err
	}
	if !ok {
		return SignApproval404JSONResponse(approvalNotFound), nil
	}
	member, post, err := s.signingRecord(ctx, r.society)
	if err != nil {
		return nil, err
	}
	err = s.onStepUp(ctx, "approval_signature", req.Body.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, $3, $4)`,
			r.id, member, post, act)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return SignApproval403JSONResponse(Problem{Code: "step_up_required", Message: "sign with a fresh PIN proof for approval_signature, your own"}), nil
		case c == "signatory_holds_signing_post" || c == "signatory_not_auditor" || c == "signatory_has_user":
			return SignApproval403JSONResponse(Problem{Code: "not_a_signatory", Message: "you hold no current signing post in this society"}), nil
		case c == "borrower_never_signs_own_loan" || c == "signatory_not_guarantor":
			return SignApproval403JSONResponse(Problem{Code: "conflict_of_interest", Message: "the borrower and the guarantors of a loan never sign its requests"}), nil
		case c == "signature_before_expiry" || c == "signature_on_open_request":
			return SignApproval409JSONResponse(Problem{Code: "request_closed", Message: "this request has expired or been rejected"}), nil
		case c == "signatory_approvals_one_per_user":
			return SignApproval409JSONResponse(Problem{Code: "already_signed", Message: "you have already signed this request"}), nil
		}
		return nil, err
	}
	v, err := s.approvalView(ctx, r, "officer")
	if err != nil {
		return nil, err
	}
	return SignApproval201JSONResponse(v), nil
}

func (s *server) RejectApproval(ctx context.Context, req RejectApprovalRequestObject) (RejectApprovalResponseObject, error) {
	r, ok, err := s.openForOfficer(ctx, req.RequestId.String())
	if err != nil {
		return nil, err
	}
	if !ok {
		return RejectApproval404JSONResponse(approvalNotFound), nil
	}
	member, post, err := s.signingRecord(ctx, r.society)
	if err != nil {
		return nil, err
	}
	err = s.onStepUp(ctx, "approval_rejection", req.Body.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, note, act_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, r.id, member, callerOf(ctx), post, string(req.Body.Reason), req.Body.Note, act)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return RejectApproval403JSONResponse(Problem{Code: "step_up_required", Message: "reject with a fresh PIN proof for approval_rejection, your own"}), nil
		case c == "rejecter_holds_signing_post" || c == "rejecter_user_is_member_user":
			return RejectApproval403JSONResponse(Problem{Code: "not_a_signatory", Message: "only a current signatory rejects"}), nil
		case c == "rejection_note_for_looks_wrong":
			return RejectApproval422JSONResponse(Problem{Code: "note_required", Message: "say what looks wrong: it goes to the committee and the auditor"}), nil
		case c == "rejection_on_open_request":
			return RejectApproval409JSONResponse(Problem{Code: "request_closed", Message: "this request is no longer open"}), nil
		case c == "rejecter_has_not_signed":
			return RejectApproval409JSONResponse(Problem{Code: "signed_cannot_reject", Message: "you signed this request; a signatory does not also reject it"}), nil
		}
		return nil, err
	}
	v, err := s.approvalView(ctx, r, "officer")
	if err != nil {
		return nil, err
	}
	return RejectApproval201JSONResponse(v), nil
}
