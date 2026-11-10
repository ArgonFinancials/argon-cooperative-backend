package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/auth"
)

// ENROLMENT OVER HTTP (stage 1, 2026-11-10). Written against the acceptance
// tests, test/tier1/tier1_api_enrol_test.go, shown red against stubs first.
//
// The rules are the auth service's (Enrol: the PIN first, one refusal, the
// code's own tries) and the database's (0024–0026: who issues, a signatory's
// code in two halves, one live code, append-only). This layer carries them to
// HTTP: one answer for every refusal of a redemption; the used slip in the
// approved words; anyone who is not an officer of the society told nothing is
// there; and the slip SHOWN ONCE, to its issuer, never stored or logged.

var enrolmentUnavailable = Problem{Code: "enrolment_unavailable",
	Message: "Enrolment codes cannot be issued on this server yet: it runs without an auth service, which draws them. Nothing was issued."}

func (s *server) PostEnrol(ctx context.Context, req PostEnrolRequestObject) (PostEnrolResponseObject, error) {
	if s.auth == nil {
		return PostEnrol503JSONResponse(signInUnavailable), nil
	}
	if req.Body == nil {
		return PostEnrol400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	b := req.Body
	in := auth.EnrolRequest{Credential: b.Credential, Code: b.Code}
	if b.Pin != nil {
		in.PIN = *b.Pin
	}
	if b.NewPin != nil {
		in.NewPIN = *b.NewPin
	}
	r, err := s.auth.Enrol(ctx, in)
	switch {
	case errors.Is(err, auth.ErrRefused):
		return PostEnrol401JSONResponse{Code: "refused", Message: "those details do not match an account"}, nil
	case errors.Is(err, auth.ErrSlipUsed):
		// The accepted residual (§12): the holder of a used slip is told, in
		// these words, and an officer can see who used it, when and where.
		return PostEnrol409JSONResponse{Code: "slip_used", Message: "This code has already been used. Speak to your secretary."}, nil
	case errors.Is(err, auth.ErrTryLater):
		return PostEnrol429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case errors.Is(err, auth.ErrNeedsNewPIN):
		return PostEnrol422JSONResponse{Code: "needs_new_pin", Message: "Choose a six-digit PIN to finish. Your code is still good."}, nil
	case errors.Is(err, auth.ErrPINNotAllowed):
		return PostEnrol422JSONResponse{Code: "pin_not_allowed", Message: "That PIN is too easy to guess. Choose another six digits. Your code is still good."}, nil
	case errors.Is(err, auth.ErrPINAlreadySet):
		return PostEnrol422JSONResponse{Code: "pin_already_set", Message: "You already have a PIN: enter it, not a new one. Your code is still good."}, nil
	case err != nil:
		return nil, err
	}
	out := PostEnrol200JSONResponse{Outcome: SignedIn, SessionToken: &r.SessionToken, DeviceToken: &r.DeviceToken}
	if out.ExpiresAt, err = s.sessionExpiry(ctx, r.SessionToken); err != nil {
		return nil, err
	}
	return out, nil
}

// enrolTarget: the member record's person, if it is this society's and the
// caller is an officer of it; ok false means "nothing is there" (404).
func (s *server) enrolTarget(ctx context.Context, society, member string) (user string, signs, ok bool, err error) {
	officer, err := s.officerHere(ctx, society)
	if err != nil || !officer {
		return "", false, false, err
	}
	err = s.db.QueryRow(ctx, `SELECT user_id::text, person_holds_post(user_id, $2, true) FROM members
		WHERE id = $1 AND society_id = $2 AND user_id IS NOT NULL`, member, society).Scan(&user, &signs)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, nil
	}
	return user, signs, err == nil, err
}

func (s *server) IssueEnrolmentCode(ctx context.Context, req IssueEnrolmentCodeRequestObject) (IssueEnrolmentCodeResponseObject, error) {
	if s.auth == nil {
		return IssueEnrolmentCode503JSONResponse(enrolmentUnavailable), nil
	}
	society, member := req.SocietyId.String(), req.MemberId.String()
	target, signs, ok, err := s.enrolTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !ok {
		return IssueEnrolmentCode404JSONResponse(notFound("member")), nil
	}
	if req.Body == nil {
		return IssueEnrolmentCode400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	caller := callerOf(ctx)

	// A signatory's code waiting for its second half: the one who issued the
	// first is told a different signatory issues the other.
	var pending, firstBy string
	if signs {
		err := s.db.QueryRow(ctx, `SELECT c.id::text, a.actor_user_id::text FROM enrolment_codes c
			JOIN credential_acts a ON a.id = c.issued_by_act
			WHERE c.user_id = $1 AND c.used_at IS NULL AND c.voided_at IS NULL AND c.pending`, target).Scan(&pending, &firstBy)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if pending != "" && firstBy == caller {
			return secondSignatoryNeeded(), nil
		}
	}

	var out IssueEnrolmentCode201JSONResponse
	var id string
	var expires time.Time
	err = pgx.BeginFunc(ctx, s.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_user_id, target_member_id)
			VALUES ('enrolment_issue', $1, $2, $3, $4) RETURNING id`, caller, req.Body.PinProofId.String(), target, member).Scan(&act); err != nil {
			return err
		}
		switch {
		case signs && pending != "":
			text, d := s.auth.NewEnrolmentHalf()
			out.Kind, out.Slip, out.Complete, id = SecondHalf, text, true, pending
			return x.QueryRow(ctx, `UPDATE enrolment_codes SET second_half_digest = $2, second_by_act = $3 WHERE id = $1 RETURNING expires_at`,
				pending, d, act).Scan(&expires)
		case signs:
			text, d := s.auth.NewEnrolmentHalf()
			out.Kind, out.Slip, out.Complete = FirstHalf, text, false
			return x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act) VALUES ($1, $2, $3, $4)
				RETURNING id::text, expires_at`, target, member, d, act).Scan(&id, &expires)
		default:
			text, d := s.auth.NewEnrolmentCode()
			out.Kind, out.Slip, out.Complete = Whole, text, true
			return x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_act) VALUES ($1, $2, $3, $4)
				RETURNING id::text, expires_at`, target, member, d, act).Scan(&id, &expires)
		}
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return IssueEnrolmentCode403JSONResponse(stepUpProblem("enrolment_issue")), nil
		case c == "enrolment_by_current_officer":
			return IssueEnrolmentCode403JSONResponse{Code: "not_permitted", Message: "issuing an enrolment code is an officer's; the internal auditor's role is read-only"}, nil
		case c == "enrolment_half_by_signatory":
			return IssueEnrolmentCode403JSONResponse{Code: "not_permitted", Message: "a signatory's code is issued in two halves by two other current signatories"}, nil
		case c == "enrolment_not_self":
			return IssueEnrolmentCode403JSONResponse{Code: "conflict_of_interest", Message: "nobody issues their own enrolment code: another officer issues it"}, nil
		case c == "enrolment_halves_two_signatories": // the first issuer, between our read and our write
			return secondSignatoryNeeded(), nil
		}
		return nil, err
	}
	out.CodeId, out.ExpiresAt = uuidOf(id), expires
	return out, nil
}

func secondSignatoryNeeded() IssueEnrolmentCode409JSONResponse {
	return IssueEnrolmentCode409JSONResponse{Code: "second_signatory_needed",
		Message: "you issued the first half of this code; a different current signatory issues the other"}
}

func (s *server) GetEnrolmentCodes(ctx context.Context, req GetEnrolmentCodesRequestObject) (GetEnrolmentCodesResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	target, _, ok, err := s.enrolTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetEnrolmentCodes404JSONResponse(notFound("member")), nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.id, c.issued_at, c.expires_at, coalesce(u1.full_name, c.issued_by_operator),
		c.issued_by_operator IS NOT NULL, u2.full_name,
		CASE WHEN c.used_at IS NOT NULL THEN 'used' WHEN c.voided_at IS NOT NULL THEN 'voided'
		     WHEN c.pending THEN 'waiting_for_second_half' ELSE 'live' END,
		c.void_reason, c.used_at IS NULL AND c.voided_at IS NULL AND auth_now() >= c.expires_at,
		c.wrong_tries, c.used_at, c.used_device_id, c.replaces_id
		FROM enrolment_codes c
		LEFT JOIN credential_acts a1 ON a1.id = c.issued_by_act LEFT JOIN users u1 ON u1.id = a1.actor_user_id
		LEFT JOIN credential_acts a2 ON a2.id = c.second_by_act LEFT JOIN users u2 ON u2.id = a2.actor_user_id
		WHERE c.user_id = $1 ORDER BY c.issued_at DESC, c.id`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := GetEnrolmentCodes200JSONResponse{Codes: []EnrolmentCodeRecord{}}
	for rows.Next() {
		var r EnrolmentCodeRecord
		var state string
		var reason *string
		var tries int16
		if err := rows.Scan(&r.CodeId, &r.IssuedAt, &r.ExpiresAt, &r.IssuedBy, &r.IssuedByOperator, &r.SecondHalfBy, &state,
			&reason, &r.Expired, &tries, &r.UsedAt, &r.UsedDeviceId, &r.Replaces); err != nil {
			return nil, err
		}
		r.State, r.WrongTries = EnrolmentCodeRecordState(state), int(tries)
		if reason != nil {
			v := EnrolmentCodeRecordVoidReason(*reason)
			r.VoidReason = &v
		}
		out.Codes = append(out.Codes, r)
	}
	return out, rows.Err()
}
