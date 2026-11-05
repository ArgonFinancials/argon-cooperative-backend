package api

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RELEASING A LOCK (0022, decided 2026-11-05). Written against the approved
// acceptance tests, test/tier1/tier1_api_locks_test.go, and shipped with the
// lockout split: without it, an officer lock (the third lockout in 24 h on the
// member's own devices) is one nobody can lift.
//
// The rules are the DATABASE's (0003, 0022): an officer of the member's
// society, never the auditor, never their own lock, a reason, the officer's
// own step-up, and a lock to release. A release clears both of the member's
// locks. This layer's own: anyone who is not an officer of the society, or a
// member who is not the society's, is told nothing is there.
func (s *server) ReleaseLock(ctx context.Context, req ReleaseLockRequestObject) (ReleaseLockResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	nothing := ReleaseLock404JSONResponse(notFound("member"))
	officer, err := s.officerHere(ctx, society)
	if err != nil {
		return nil, err
	}
	if !officer {
		return nothing, nil
	}
	var target string
	err = s.db.QueryRow(ctx, `SELECT user_id::text FROM members WHERE id = $1 AND society_id = $2 AND user_id IS NOT NULL`, member, society).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return nothing, nil
	}
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return ReleaseLock400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if len(strings.TrimSpace(b.Reason)) <= 10 { // the database's own threshold (0003, reset_states_reason)
		return ReleaseLock422JSONResponse{Code: "reason_required", Message: "say what happened and how the member was identified in person"}, nil
	}
	var out LockReleased
	var by string
	err = s.db.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_user_id, target_member_id, reason)
		VALUES ('lock_release', $1, $2, $3, $4, $5) RETURNING actor_user_id::text, at`,
		callerOf(ctx), b.PinProofId.String(), target, member, b.Reason).Scan(&by, &out.ReleasedAt)
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return ReleaseLock403JSONResponse(stepUpProblem("lock_release")), nil
		case c == "reset_by_current_officer": // an officer here who may not: the auditor
			return ReleaseLock403JSONResponse{Code: "not_permitted", Message: "releasing a lock is an officer's; the internal auditor's role is read-only"}, nil
		case c == "release_not_self":
			return ReleaseLock403JSONResponse{Code: "conflict_of_interest", Message: "nobody releases their own lock: another officer releases it"}, nil
		case c == "release_needs_a_lock":
			return ReleaseLock409JSONResponse{Code: "not_locked", Message: "this member is not locked out: there is nothing to release"}, nil
		case c == "reset_states_reason":
			return ReleaseLock422JSONResponse{Code: "reason_required", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	out.MemberId, out.ReleasedBy = uuidOf(member), uuidOf(by)
	return ReleaseLock201JSONResponse(out), nil
}
