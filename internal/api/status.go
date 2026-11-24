package api

// SUSPENDING A MEMBER, AND RECORDING ONE WHO LEAVES (0038, 2026-11-24).
//
//   POST /societies/{s}/members/{m}/status-changes: the society's current
//   secretary, on their own step-up (member_status), with a reason. Active and
//   suspended move to each other; either may leave; leaving is final. Never
//   their own; never an officer in post. The database checks every rule and
//   moves the member; every refusal that can be known is checked here first,
//   so a refused request spends nothing (the proof stays good).
//   GET the same path: the member's changes, newest first, for officers.

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// statusMoves: from -> to, as 0038's member_status_move allows.
var statusMoves = map[string]map[string]bool{
	"active":    {"suspended": true, "exited": true},
	"suspended": {"active": true, "exited": true},
}

func (s *server) ChangeMemberStatus(ctx context.Context, req ChangeMemberStatusRequestObject) (ChangeMemberStatusResponseObject, error) {
	caller, society, member := callerOf(ctx), req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ChangeMemberStatus404JSONResponse(notFound("member")), nil
	}
	var user *string
	var status string
	var inPost bool
	err := s.db.QueryRow(ctx, `SELECT m.user_id::text, m.status::text,
		  EXISTS (SELECT 1 FROM appointments a WHERE a.member_id = m.id AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))
		FROM members m WHERE m.id = $1 AND m.society_id = $2`, member, society).Scan(&user, &status, &inPost)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChangeMemberStatus404JSONResponse(notFound("member")), nil
	}
	if err != nil {
		return nil, err
	}
	b := req.Body
	if b == nil {
		return ChangeMemberStatus400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	to, reason := string(b.Status), strings.TrimSpace(b.Reason)
	if ok, err := s.holdsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ChangeMemberStatus403JSONResponse(notPermittedStatus), nil
	}
	if user != nil && *user == caller {
		return ChangeMemberStatus403JSONResponse(ownStatus), nil
	}
	if reason == "" {
		return ChangeMemberStatus422JSONResponse(reasonRequired), nil
	}
	if !statusMoves[status][to] {
		return ChangeMemberStatus409JSONResponse(moveNotAllowed(status, to)), nil
	}
	if to != "active" && inPost {
		return ChangeMemberStatus409JSONResponse(holdsAPost), nil
	}

	var out MemberStatusChange
	var id, from, toS string
	err = s.onStepUp(ctx, "member_status", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO member_status_changes (member_id, to_status, reason, act_id) VALUES ($1, $2::member_status, $3, $4)
			RETURNING id::text, from_status::text, to_status::text, at`, member, to, reason, act).Scan(&id, &from, &toS, &out.At)
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "member_status_act":
			return ChangeMemberStatus403JSONResponse(stepUpProblem("member_status")), nil
		case c == "member_status_by_secretary":
			return ChangeMemberStatus403JSONResponse(notPermittedStatus), nil
		case c == "member_status_not_self":
			return ChangeMemberStatus403JSONResponse(ownStatus), nil
		case c == "member_status_says_why":
			return ChangeMemberStatus422JSONResponse(reasonRequired), nil
		case c == "member_status_move": // the status changed since it was read
			return ChangeMemberStatus409JSONResponse(Problem{Code: "not_allowed", Message: dbMessage(err)}), nil
		case c == "member_status_holds_a_post":
			return ChangeMemberStatus409JSONResponse(holdsAPost), nil
		}
		return nil, err
	}
	out.ChangeId, out.MemberId, out.ByUserId = uuidOf(id), uuidOf(member), uuidOf(caller)
	out.From, out.To, out.Reason = MemberStatusChangeFrom(from), MemberStatusChangeTo(toS), reason
	if err := s.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, caller).Scan(&out.ByName); err != nil {
		return nil, err
	}
	return ChangeMemberStatus201JSONResponse(out), nil
}

func (s *server) GetMemberStatusChanges(ctx context.Context, req GetMemberStatusChangesRequestObject) (GetMemberStatusChangesResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return GetMemberStatusChanges404JSONResponse(notFound("member")), nil
	}
	var here bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND society_id = $2)`, member, society).Scan(&here); err != nil {
		return nil, err
	}
	if !here {
		return GetMemberStatusChanges404JSONResponse(notFound("member")), nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.id::text, c.from_status::text, c.to_status::text, c.reason, a.actor_user_id::text, u.full_name, c.at
		FROM member_status_changes c JOIN credential_acts a ON a.id = c.act_id JOIN users u ON u.id = a.actor_user_id
		WHERE c.member_id = $1 ORDER BY c.at DESC, c.id DESC`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := GetMemberStatusChanges200JSONResponse{Changes: []MemberStatusChange{}}
	for rows.Next() {
		var c MemberStatusChange
		var id, from, to, by string
		if err := rows.Scan(&id, &from, &to, &c.Reason, &by, &c.ByName, &c.At); err != nil {
			return nil, err
		}
		c.ChangeId, c.MemberId, c.ByUserId = uuidOf(id), uuidOf(member), uuidOf(by)
		c.From, c.To = MemberStatusChangeFrom(from), MemberStatusChangeTo(to)
		out.Changes = append(out.Changes, c)
	}
	return out, rows.Err()
}

var (
	notPermittedStatus = Problem{Code: "not_permitted", Message: "a member is suspended, reinstated or recorded as leaving by the society's current secretary"}
	ownStatus          = Problem{Code: "conflict_of_interest", Message: "the secretary never changes their own status"}
	reasonRequired     = Problem{Code: "reason_required", Message: "say why: it is recorded as written"}
	holdsAPost         = Problem{Code: "holds_a_post", Message: "this member holds a post in the society: their appointment ends first (a suspended officer would still sign)"}
)

func moveNotAllowed(from, to string) Problem {
	msg := "a member who is " + from + " is not made " + to + ": active and suspended move to each other, either may leave, and leaving is final"
	return Problem{Code: "not_allowed", Message: msg}
}
