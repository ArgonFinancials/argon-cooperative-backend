//go:build tier1

// The REFERENCE enrolment over HTTP (test code, not the server), breakable
// one named way per run (ARGON_TEST_API_FAKE=<break>), for
// tier1_api_enrol_test.go:
//
//	enrol_not_public            redeeming a code needs a session the member cannot have yet
//	enrol_wrong_code_told       a wrong code answers differently from an unknown number
//	enrol_slip_used_as_refused  the used slip answers refused: the member is not told what to do
//	enrol_refusals_as_500       needs_new_pin / pin_not_allowed / pin_already_set answer 500
//	enrol_lock_as_refused       a locked account is told refused, not to wait
//	enrol_browser_no_cookies    a browser's enrolment returns tokens in the body, no cookies
//	issue_signatory_whole       a signatory is issued a whole code by one officer
//	issue_same_signatory_db     the same signatory asking for the other half reaches the database (500)
//	issue_second_sees_first     the second signatory's answer carries the first signatory's half
//	issue_refusals_as_500       the auditor, a non-signer, oneself, a wrong step-up: 500
//	issue_outsider_reaches_db   someone who is not an officer here is answered from the database
//	issue_member_any_society    the member may be any society's
//	history_shows_digest        the history carries each code's digest
//	history_issuer_unnamed      the history names the issuer by id, not name
//	history_no_device           the history does not say what device a code trusted
//	history_oldest_first        the history is oldest first
package tier1

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

func (f *fakeAPI) PostEnrol(ctx context.Context, req api.PostEnrolRequestObject) (api.PostEnrolResponseObject, error) {
	if f.noAuth {
		return api.PostEnrol503JSONResponse{Code: "sign_in_unavailable", Message: "sign-in is not available on this server yet"}, nil
	}
	if req.Body == nil {
		return api.PostEnrol400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	b := req.Body
	svc := &fakeService{d: f.authDeps()}
	in := auth.EnrolRequest{Credential: b.Credential, Code: b.Code}
	if b.Pin != nil {
		in.PIN = *b.Pin
	}
	if b.NewPin != nil {
		in.NewPIN = *b.NewPin
	}
	r, err := svc.Enrol(ctx, in)
	refused := api.PostEnrol401JSONResponse{Code: "refused", Message: "those details do not match an account"}
	switch {
	case errors.Is(err, auth.ErrRefused):
		if f.brk == "enrol_wrong_code_told" {
			var n int
			f.db.QueryRow(ctx, `SELECT count(*) FROM members WHERE member_number = $1 OR phone = $1`, b.Credential).Scan(&n)
			if n > 0 {
				return api.PostEnrol401JSONResponse{Code: "refused", Message: "that code is not right"}, nil
			}
		}
		return refused, nil
	case errors.Is(err, auth.ErrSlipUsed):
		if f.brk == "enrol_slip_used_as_refused" {
			return refused, nil
		}
		return api.PostEnrol409JSONResponse{Code: "slip_used", Message: "This code has already been used. Speak to your secretary."}, nil
	case errors.Is(err, auth.ErrTryLater):
		if f.brk == "enrol_lock_as_refused" {
			return refused, nil
		}
		return api.PostEnrol429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case errors.Is(err, auth.ErrNeedsNewPIN), errors.Is(err, auth.ErrPINNotAllowed), errors.Is(err, auth.ErrPINAlreadySet):
		if f.brk == "enrol_refusals_as_500" {
			return nil, err
		}
		code := map[error]string{auth.ErrNeedsNewPIN: "needs_new_pin", auth.ErrPINNotAllowed: "pin_not_allowed", auth.ErrPINAlreadySet: "pin_already_set"}
		for e, c := range code {
			if errors.Is(err, e) {
				return api.PostEnrol422JSONResponse{Code: c, Message: err.Error()}, nil
			}
		}
	case err != nil:
		return nil, err
	}
	out := api.PostEnrol200JSONResponse{Outcome: api.SignedIn, SessionToken: &r.SessionToken, DeviceToken: &r.DeviceToken}
	out.ExpiresAt = f.expiry(ctx, r.SessionToken, nil)
	return out, nil
}

// The halves' text, kept by the REFERENCE only for its own break
// (issue_second_sees_first). The real server keeps none.
var fakeHalves sync.Map

func (f *fakeAPI) IssueEnrolmentCode(ctx context.Context, req api.IssueEnrolmentCodeRequestObject) (api.IssueEnrolmentCodeResponseObject, error) {
	if f.noAuth {
		return api.IssueEnrolmentCode503JSONResponse{Code: "enrolment_unavailable", Message: "this server has no auth service to draw codes"}, nil
	}
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	nothing := api.IssueEnrolmentCode404JSONResponse{Code: "not_found", Message: "no such member"}
	if !f.officerHere(ctx, caller, society) && f.brk != "issue_outsider_reaches_db" {
		return nothing, nil
	}
	var target string
	var signs bool
	err := f.db.QueryRow(ctx, `SELECT user_id::text, person_holds_post(user_id, $2, true) FROM members
		WHERE id = $1 AND ($3 OR society_id = $2) AND user_id IS NOT NULL`, member, society, f.brk == "issue_member_any_society").Scan(&target, &signs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nothing, nil
	}
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return api.IssueEnrolmentCode400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	svc := &fakeService{d: f.authDeps()}
	proof := req.Body.PinProofId.String()
	signs = signs && f.brk != "issue_signatory_whole"

	var pending, firstBy string
	if signs {
		f.db.QueryRow(ctx, `SELECT c.id::text, a.actor_user_id::text FROM enrolment_codes c JOIN credential_acts a ON a.id = c.issued_by_act
			WHERE c.user_id = $1 AND c.used_at IS NULL AND c.voided_at IS NULL AND c.pending`, target).Scan(&pending, &firstBy)
		if pending != "" && firstBy == caller && f.brk != "issue_same_signatory_db" {
			return api.IssueEnrolmentCode409JSONResponse{Code: "second_signatory_needed", Message: "you issued the first half; a different current signatory issues the other"}, nil
		}
	}
	var out api.IssueEnrolmentCode201JSONResponse
	var id string
	var expires time.Time
	err = pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_user_id, target_member_id)
			VALUES ('enrolment_issue', $1, $2, $3, $4) RETURNING id`, caller, proof, target, member).Scan(&act); err != nil {
			return err
		}
		switch {
		case signs && pending != "":
			text, d := svc.NewEnrolmentHalf()
			out.Kind, out.Slip, out.Complete = api.SecondHalf, text, true
			if f.brk == "issue_second_sees_first" {
				if first, ok := fakeHalves.Load(pending); ok {
					out.Slip = text + " " + first.(string)
				}
			}
			id = pending
			return x.QueryRow(ctx, `UPDATE enrolment_codes SET second_half_digest = $2, second_by_act = $3 WHERE id = $1 RETURNING expires_at`,
				pending, d, act).Scan(&expires)
		case signs:
			text, d := svc.NewEnrolmentHalf()
			out.Kind, out.Slip, out.Complete = api.FirstHalf, text, false
			err := x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act) VALUES ($1, $2, $3, $4)
				RETURNING id::text, expires_at`, target, member, d, act).Scan(&id, &expires)
			fakeHalves.Store(id, text)
			return err
		default:
			text, d := svc.NewEnrolmentCode()
			out.Kind, out.Slip, out.Complete = api.Whole, text, true
			return x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_act) VALUES ($1, $2, $3, $4)
				RETURNING id::text, expires_at`, target, member, d, act).Scan(&id, &expires)
		}
	})
	if err != nil {
		if f.brk == "issue_refusals_as_500" {
			return nil, err
		}
		switch c := pgConstraint(err); {
		case lockStepUpRefused[c]:
			return api.IssueEnrolmentCode403JSONResponse{Code: "step_up_required", Message: "a fresh PIN proof for enrolment_issue, your own"}, nil
		case c == "enrolment_by_current_officer" || c == "enrolment_half_by_signatory":
			return api.IssueEnrolmentCode403JSONResponse{Code: "not_permitted", Message: "not yours to issue"}, nil
		case c == "enrolment_not_self":
			return api.IssueEnrolmentCode403JSONResponse{Code: "conflict_of_interest", Message: "nobody issues their own code"}, nil
		}
		return nil, err
	}
	out.CodeId, out.ExpiresAt = uuid.MustParse(id), expires
	return out, nil
}

func (f *fakeAPI) GetEnrolmentCodes(ctx context.Context, req api.GetEnrolmentCodesRequestObject) (api.GetEnrolmentCodesResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society, member := req.SocietyId.String(), req.MemberId.String()
	nothing := api.GetEnrolmentCodes404JSONResponse{Code: "not_found", Message: "no such member"}
	if !f.officerHere(ctx, caller, society) {
		return nothing, nil
	}
	var target string
	if err := f.db.QueryRow(ctx, `SELECT user_id::text FROM members WHERE id = $1 AND society_id = $2 AND user_id IS NOT NULL`, member, society).Scan(&target); err != nil {
		return nothing, nil
	}
	order := "DESC"
	if f.brk == "history_oldest_first" {
		order = "ASC"
	}
	who := "coalesce(u1.full_name, c.issued_by_operator)"
	if f.brk == "history_issuer_unnamed" {
		who = "coalesce(a1.actor_user_id::text, c.issued_by_operator)"
	}
	rows, err := f.db.Query(ctx, `SELECT c.id, c.issued_at, c.expires_at, `+who+`, c.issued_by_operator IS NOT NULL, u2.full_name,
		CASE WHEN c.used_at IS NOT NULL THEN 'used' WHEN c.voided_at IS NOT NULL THEN 'voided' WHEN c.pending THEN 'waiting_for_second_half' ELSE 'live' END,
		c.void_reason, c.used_at IS NULL AND c.voided_at IS NULL AND auth_now() >= c.expires_at, c.wrong_tries, c.used_at, c.used_device_id, c.replaces_id,
		coalesce(c.code_digest, '')
		FROM enrolment_codes c
		LEFT JOIN credential_acts a1 ON a1.id = c.issued_by_act LEFT JOIN users u1 ON u1.id = a1.actor_user_id
		LEFT JOIN credential_acts a2 ON a2.id = c.second_by_act LEFT JOIN users u2 ON u2.id = a2.actor_user_id
		WHERE c.user_id = $1 ORDER BY c.issued_at `+order+`, c.id`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.GetEnrolmentCodes200JSONResponse{Codes: []api.EnrolmentCodeRecord{}}
	for rows.Next() {
		var r api.EnrolmentCodeRecord
		var state, d string
		var reason, second *string
		var tries int16
		if err := rows.Scan(&r.CodeId, &r.IssuedAt, &r.ExpiresAt, &r.IssuedBy, &r.IssuedByOperator, &second, &state,
			&reason, &r.Expired, &tries, &r.UsedAt, &r.UsedDeviceId, &r.Replaces, &d); err != nil {
			return nil, err
		}
		r.State, r.WrongTries, r.SecondHalfBy = api.EnrolmentCodeRecordState(state), int(tries), second
		if reason != nil {
			v := api.EnrolmentCodeRecordVoidReason(*reason)
			r.VoidReason = &v
		}
		if f.brk == "history_no_device" {
			r.UsedDeviceId = nil
		}
		if f.brk == "history_shows_digest" && d != "" {
			r.IssuedBy += " (" + d + ")"
		}
		out.Codes = append(out.Codes, r)
	}
	return out, rows.Err()
}
