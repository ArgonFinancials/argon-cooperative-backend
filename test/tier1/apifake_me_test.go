//go:build tier1

// THE MUTATION VEHICLE FOR GET /me — test code, NOT the API.
//
// A reference implementation of GET /me, breakable one named way per run.
//
// Breaks:
//
//	me_any_member          every member record in the caller's societies is listed, not only the caller's
//	me_posts_ever          an appointment that has ended is listed as a post
//	me_future_posts        an appointment not yet begun is listed as a post
//	me_null_lists          empty lists are sent as null
//	me_first_society_only  only one membership is listed
//	me_unordered           memberships in reverse society-code order
//	me_signatory_dropped   every post reads as not a signatory
//	me_status_fixed        every member record reads as "active"
//	me_public              GET /me answers without a session
//
// Shared: ended_session_ok.
package tier1

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

func (f *fakeAPI) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	caller, _ := ctx.Value(ctxUser{}).(string)
	m := api.Me{UserId: uuidOf(caller), Memberships: []api.Membership{}}
	if caller == "" { // me_public: served with no session
		return api.GetMe200JSONResponse(m), nil
	}
	if err := f.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, caller).Scan(&m.FullName); err != nil {
		return nil, err
	}
	// A cookie session gets its CSRF token again (the reload); a Bearer
	// session has no use for one and is given none.
	if viaCookie, _ := ctx.Value(ctxCookie{}).(bool); viaCookie && f.brk != "me_omits_token" {
		tok, _ := ctx.Value(ctxToken{}).(string)
		c := csrfOf(tok)
		m.CsrfToken = &c
	}
	whose := `m.user_id = $1`
	if f.brk == "me_any_member" {
		whose = `m.society_id IN (SELECT society_id FROM members WHERE user_id = $1)`
	}
	order := `s.code`
	if f.brk == "me_unordered" {
		order = `s.code DESC`
	}
	limit := ""
	if f.brk == "me_first_society_only" {
		limit = " LIMIT 1"
	}
	rows, err := f.db.Query(ctx, `SELECT s.id::text, s.code, s.registered_name, m.id::text, m.member_number, m.status::text
		FROM members m JOIN societies s ON s.id = m.society_id WHERE `+whose+` ORDER BY `+order+`, m.id`+limit, caller)
	if err != nil {
		return nil, err
	}
	type rec struct{ soc, code, name, member, number, status string }
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.soc, &r.code, &r.name, &r.member, &r.number, &r.status); err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	current := `a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date)`
	switch f.brk {
	case "me_posts_ever":
		current = `a.held_from <= current_date`
	case "me_future_posts":
		current = `(a.held_until IS NULL OR a.held_until >= current_date)`
	}
	for _, r := range recs {
		ms := api.Membership{SocietyId: uuidOf(r.soc), SocietyCode: r.code, SocietyName: r.name, MemberId: uuidOf(r.member),
			MemberNumber: r.number, MemberStatus: r.status, Posts: []api.HeldPost{}}
		if f.brk == "me_status_fixed" {
			ms.MemberStatus = "active"
		}
		prow, err := f.db.Query(ctx, `SELECT a.post::text, a.is_signatory, a.held_from FROM appointments a
			WHERE a.member_id = $1 AND `+current+` ORDER BY a.held_from, a.post`, r.member)
		if err != nil {
			return nil, err
		}
		for prow.Next() {
			var p api.HeldPost
			var post string
			var from time.Time
			if err := prow.Scan(&post, &p.Signatory, &from); err != nil {
				prow.Close()
				return nil, err
			}
			p.Post, p.HeldFrom = api.OfficerPost(post), openapi_types.Date{Time: from}
			if f.brk == "me_signatory_dropped" {
				p.Signatory = false
			}
			ms.Posts = append(ms.Posts, p)
		}
		prow.Close()
		if f.brk == "me_null_lists" && len(ms.Posts) == 0 {
			ms.Posts = nil
		}
		m.Memberships = append(m.Memberships, ms)
	}
	if f.brk == "me_null_lists" && len(m.Memberships) == 0 {
		m.Memberships = nil
	}
	return api.GetMe200JSONResponse(m), nil
}
