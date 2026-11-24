//go:build tier1

// The reference for a member's status over HTTP (0038), breakable by name
// (ARGON_TEST_API_FAKE=<break>), for tier1_api_status_test.go:
//
//	status_any_officer        any officer may try: the database refuses (500)
//	status_own_db             the secretary's own: the database refuses (500)
//	status_reason_db          no reason: the database refuses (500), not told
//	status_move_db            a move not made: the database refuses (500)
//	status_post_db            an officer in post: the database refuses (500)
//	status_record_unnamed     the record does not say who
//	status_history_oldest     the history oldest first
package tier1

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
)

var fakeMoves = map[string]map[string]bool{"active": {"suspended": true, "exited": true}, "suspended": {"active": true, "exited": true}}

func (f *fakeAPI) ChangeMemberStatus(ctx context.Context, req api.ChangeMemberStatusRequestObject) (api.ChangeMemberStatusResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	if !f.officerHere(ctx, caller, society) {
		return api.ChangeMemberStatus404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	var user *string
	var status string
	var inPost bool
	err := f.db.QueryRow(ctx, `SELECT m.user_id::text, m.status::text,
		  EXISTS (SELECT 1 FROM appointments a WHERE a.member_id = m.id AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))
		FROM members m WHERE m.id = $1 AND m.society_id = $2`, member, society).Scan(&user, &status, &inPost)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ChangeMemberStatus404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	if err != nil {
		return nil, err
	}
	b := req.Body
	to, reason := string(b.Status), strings.TrimSpace(b.Reason)
	var sec bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id WHERE m.user_id = $1 AND a.society_id = $2
		AND a.post = 'secretary' AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, caller, society).Scan(&sec)
	switch {
	case !sec && f.brk != "status_any_officer":
		return api.ChangeMemberStatus403JSONResponse{Code: "not_permitted", Message: "the secretary's"}, nil
	case user != nil && *user == caller && f.brk != "status_own_db":
		return api.ChangeMemberStatus403JSONResponse{Code: "conflict_of_interest", Message: "never their own"}, nil
	case reason == "" && f.brk != "status_reason_db":
		return api.ChangeMemberStatus422JSONResponse{Code: "reason_required", Message: "say why"}, nil
	case !fakeMoves[status][to] && f.brk != "status_move_db":
		return api.ChangeMemberStatus409JSONResponse{Code: "not_allowed", Message: "not that move"}, nil
	case to != "active" && inPost && f.brk != "status_post_db":
		return api.ChangeMemberStatus409JSONResponse{Code: "holds_a_post", Message: "appointment first"}, nil
	}
	var out api.MemberStatusChange
	var id, from, toS string
	err = pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('member_status', $1, $2) RETURNING id`, caller, b.PinProofId.String()).Scan(&act); err != nil {
			return err
		}
		return x.QueryRow(ctx, `INSERT INTO member_status_changes (member_id, to_status, reason, act_id) VALUES ($1, $2::member_status, $3, $4)
			RETURNING id::text, from_status::text, to_status::text, at`, member, to, reason, act).Scan(&id, &from, &toS, &out.At)
	})
	if err != nil {
		if lockStepUpRefused[pgConstraint(err)] || pgConstraint(err) == "member_status_act" {
			return api.ChangeMemberStatus403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for member_status"}, nil
		}
		return nil, err
	}
	out.ChangeId, out.MemberId, out.ByUserId = uuidOf(id), uuidOf(member), uuidOf(caller)
	out.From, out.To, out.Reason = api.MemberStatusChangeFrom(from), api.MemberStatusChangeTo(toS), reason
	if f.brk != "status_record_unnamed" {
		f.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, caller).Scan(&out.ByName)
	}
	return api.ChangeMemberStatus201JSONResponse(out), nil
}

func (f *fakeAPI) GetMemberStatusChanges(ctx context.Context, req api.GetMemberStatusChangesRequestObject) (api.GetMemberStatusChangesResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	var here bool
	f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here)
	if !f.officerHere(ctx, caller, society) || !here {
		return api.GetMemberStatusChanges404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	order := "DESC"
	if f.brk == "status_history_oldest" {
		order = "ASC"
	}
	rows, err := f.db.Query(ctx, `SELECT c.id::text, c.from_status::text, c.to_status::text, c.reason, a.actor_user_id::text, u.full_name, c.at
		FROM member_status_changes c JOIN credential_acts a ON a.id = c.act_id JOIN users u ON u.id = a.actor_user_id
		WHERE c.member_id = $1 ORDER BY c.at `+order+`, c.id `+order, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.GetMemberStatusChanges200JSONResponse{Changes: []api.MemberStatusChange{}}
	for rows.Next() {
		var c api.MemberStatusChange
		var id, from, to, by string
		if err := rows.Scan(&id, &from, &to, &c.Reason, &by, &c.ByName, &c.At); err != nil {
			return nil, err
		}
		c.ChangeId, c.MemberId, c.ByUserId = uuidOf(id), uuidOf(member), uuidOf(by)
		c.From, c.To = api.MemberStatusChangeFrom(from), api.MemberStatusChangeTo(to)
		out.Changes = append(out.Changes, c)
	}
	return out, rows.Err()
}
