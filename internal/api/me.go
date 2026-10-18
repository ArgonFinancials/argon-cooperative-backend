package api

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// GET /me: WHO THE CALLER IS (finding 40). Written against the approved
// acceptance tests, test/tier1/tier1_api_me_test.go.
//
//   - The caller's own records only: their user, each of their member records
//     (ordered by society code, so a client with several is deterministic),
//     and the posts each holds TODAY. Point-in-time applied to authorisation:
//     an appointment that has ended, or has not yet begun, grants nothing now.
//     The member record is listed either way.
//   - Empty lists are [], never null.
//   - The session middleware answers 401 before this runs.

func (s *server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	caller := callerOf(ctx)
	m := Me{UserId: uuidOf(caller), Memberships: []Membership{}}
	if err := s.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, caller).Scan(&m.FullName); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT s.id::text, s.code, s.registered_name, m.id::text, m.member_number, m.status::text
		FROM members m JOIN societies s ON s.id = m.society_id WHERE m.user_id = $1 ORDER BY s.code, m.id`, caller)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ms Membership
		var soc, member string
		if err := rows.Scan(&soc, &ms.SocietyCode, &ms.SocietyName, &member, &ms.MemberNumber, &ms.MemberStatus); err != nil {
			rows.Close()
			return nil, err
		}
		ms.SocietyId, ms.MemberId, ms.Posts = uuidOf(soc), uuidOf(member), []HeldPost{}
		m.Memberships = append(m.Memberships, ms)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range m.Memberships {
		prow, err := s.db.Query(ctx, `SELECT a.post::text, a.is_signatory, a.held_from FROM appointments a
			WHERE a.member_id = $1 AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)
			ORDER BY a.held_from, a.post`, m.Memberships[i].MemberId.String())
		if err != nil {
			return nil, err
		}
		for prow.Next() {
			var p HeldPost
			var post string
			var from time.Time
			if err := prow.Scan(&post, &p.Signatory, &from); err != nil {
				prow.Close()
				return nil, err
			}
			p.Post, p.HeldFrom = OfficerPost(post), openapi_types.Date{Time: from}
			m.Memberships[i].Posts = append(m.Memberships[i].Posts, p)
		}
		prow.Close()
		if err := prow.Err(); err != nil {
			return nil, err
		}
	}
	return GetMe200JSONResponse(m), nil
}
