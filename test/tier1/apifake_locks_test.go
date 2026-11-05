//go:build tier1

// The REFERENCE lock release (test code, not the server), breakable one named
// way per run (ARGON_TEST_API_FAKE=<break>):
//
//	lock_step_up_as_500         a step-up the database refused answers 500, not 403 step_up_required
//	lock_auditor_as_500         the auditor's release answers 500, not 403 not_permitted
//	lock_self_as_500            an officer releasing their own lock answers 500, not 403 conflict_of_interest
//	lock_outsider_reaches_db    someone who is not an officer here is answered from the database (403), not told nothing is there
//	lock_member_any_society     the member may be any society's: the database answers (403), not 404
//	lock_nothing_locked_as_500  a release with nothing locked answers 500, not 409 not_locked
//	lock_reason_as_500          a release with no reason answers 500, not 422 reason_required
//	lock_releaser_is_member     the answer names the member as the one who released it
package tier1

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
)

var lockStepUpRefused = map[string]bool{"act_needs_pin_proof": true, "act_proof_for_this_act": true, "proof_minted_elsewhere": true}

func (f *fakeAPI) ReleaseLock(ctx context.Context, req api.ReleaseLockRequestObject) (api.ReleaseLockResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	nothing := api.ReleaseLock404JSONResponse{Code: "not_found", Message: "no such member"}
	if !f.officerHere(ctx, caller, society) && f.brk != "lock_outsider_reaches_db" {
		return nothing, nil
	}
	var target string
	err := f.db.QueryRow(ctx, `SELECT user_id::text FROM members WHERE id = $1 AND ($3 OR society_id = $2)`,
		member, society, f.brk == "lock_member_any_society").Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return nothing, nil
	}
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return api.ReleaseLock400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if len(strings.TrimSpace(req.Body.Reason)) <= 10 && f.brk != "lock_reason_as_500" {
		return api.ReleaseLock422JSONResponse{Code: "reason_required", Message: "say what happened and how the member was identified"}, nil
	}
	var by string
	var released api.LockReleased
	err = f.db.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_user_id, target_member_id, reason)
		VALUES ('lock_release', $1, $2, $3, $4, $5) RETURNING actor_user_id::text, at`,
		caller, req.Body.PinProofId.String(), target, member, req.Body.Reason).Scan(&by, &released.ReleasedAt)
	if err != nil {
		switch c := pgConstraint(err); {
		case lockStepUpRefused[c] && f.brk != "lock_step_up_as_500":
			return api.ReleaseLock403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for lock_release, your own"}, nil
		case c == "reset_by_current_officer" && f.brk != "lock_auditor_as_500":
			return api.ReleaseLock403JSONResponse{Code: "not_permitted", Message: "releasing a lock is an officer's, not the auditor's"}, nil
		case c == "release_not_self" && f.brk != "lock_self_as_500":
			return api.ReleaseLock403JSONResponse{Code: "conflict_of_interest", Message: "nobody releases their own lock"}, nil
		case c == "release_needs_a_lock" && f.brk != "lock_nothing_locked_as_500":
			return api.ReleaseLock409JSONResponse{Code: "not_locked", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	released.MemberId = uuidOf(member)
	released.ReleasedBy = uuidOf(by)
	if f.brk == "lock_releaser_is_member" {
		released.ReleasedBy = uuidOf(target)
	}
	return api.ReleaseLock201JSONResponse(released), nil
}
