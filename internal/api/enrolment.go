package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/auth"
	"github.com/argon/backend/internal/email"
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
	delivery := "slip"
	if req.Body.Delivery != nil {
		delivery = string(*req.Body.Delivery)
	}
	if delivery != "slip" && delivery != "email" {
		return IssueEnrolmentCode400JSONResponse{Code: "bad_request", Message: "delivery is slip or email"}, nil
	}

	// A signatory's code waiting for its second half: the one who issued the
	// first is told a different signatory issues the other; the second half
	// goes the way the first went.
	var pending, firstBy, pendingDelivery string
	if signs {
		err := s.db.QueryRow(ctx, `SELECT c.id::text, a.actor_user_id::text, c.delivery FROM enrolment_codes c
			JOIN credential_acts a ON a.id = c.issued_by_act
			WHERE c.user_id = $1 AND c.used_at IS NULL AND c.voided_at IS NULL AND c.pending`, target).Scan(&pending, &firstBy, &pendingDelivery)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if pending != "" && firstBy == caller {
			return secondSignatoryNeeded(), nil
		}
		if pending != "" && pendingDelivery != delivery {
			return IssueEnrolmentCode409JSONResponse{Code: "delivery_mismatch",
				Message: "this code's first half went by " + pendingDelivery + ": its second half goes the same way"}, nil
		}
	}

	// By email: everything that needs nothing drawn, before the transaction
	// opens (no refusal spends the proof either way: a refusal rolls it back).
	var societyName string
	if delivery == "email" {
		if s.email == nil {
			return IssueEnrolmentCode503JSONResponse(Problem{Code: "email_unavailable",
				Message: "This server cannot send email yet (no sender is configured): issue the code on a slip. Nothing was issued."}), nil
		}
		var addr, setter *string
		if err := s.db.QueryRow(ctx, `SELECT m.email, a.actor_user_id::text, s.registered_name FROM members m JOIN societies s ON s.id = m.society_id
			LEFT JOIN credential_acts a ON a.id = m.email_set_by_act WHERE m.id = $1`, member).Scan(&addr, &setter, &societyName); err != nil {
			return nil, err
		}
		if addr == nil {
			return noEmail(), nil
		}
		if setter != nil && *setter == caller {
			return emailSetByYou(), nil
		}
	}

	var out IssueEnrolmentCode201JSONResponse
	var id, sentTo string
	var expires time.Time
	var sendErr error
	err = pgx.BeginFunc(ctx, s.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_user_id, target_member_id)
			VALUES ('enrolment_issue', $1, $2, $3, $4) RETURNING id`, caller, req.Body.PinProofId.String(), target, member).Scan(&act); err != nil {
			return err
		}
		var text, d string
		var sent *string
		var err error
		switch {
		case signs && pending != "":
			text, d = s.auth.NewEnrolmentHalf()
			out.Kind, out.Complete, id = SecondHalf, true, pending
			err = x.QueryRow(ctx, `UPDATE enrolment_codes SET second_half_digest = $2, second_by_act = $3 WHERE id = $1 RETURNING expires_at, sent_to`,
				pending, d, act).Scan(&expires, &sent)
		case signs:
			text, d = s.auth.NewEnrolmentHalf()
			out.Kind, out.Complete = FirstHalf, false
			err = x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, first_half_digest, issued_by_act, delivery) VALUES ($1, $2, $3, $4, $5)
				RETURNING id::text, expires_at, sent_to`, target, member, d, act, delivery).Scan(&id, &expires, &sent)
		default:
			text, d = s.auth.NewEnrolmentCode()
			out.Kind, out.Complete = Whole, true
			err = x.QueryRow(ctx, `INSERT INTO enrolment_codes (user_id, member_id, code_digest, issued_by_act, delivery) VALUES ($1, $2, $3, $4, $5)
				RETURNING id::text, expires_at, sent_to`, target, member, d, act, delivery).Scan(&id, &expires, &sent)
		}
		if err != nil {
			return err
		}
		if delivery == "slip" {
			out.Slip = &text
			return nil
		}
		// The address is the one the database recorded (the member's own),
		// never one this request named; sent inside the transaction, so a code
		// that was not sent was never issued.
		if sent == nil {
			return errors.New("enrolment: an emailed code with no address recorded")
		}
		sentTo = *sent
		pid, err := s.email.Send(ctx, enrolmentEmail(sentTo, societyName, string(out.Kind), text, s.appOrigin))
		if err != nil {
			sendErr = err
			return err
		}
		_, err = x.Exec(ctx, `INSERT INTO email_sends (member_id, code_id, kind, sent_to, outcome, provider_id) VALUES ($1, $2, $3, $4, 'sent', $5)`,
			member, id, string(out.Kind), sentTo, pid)
		return err
	})
	if sendErr != nil {
		// Nothing was issued (the transaction rolled back); the failure is recorded.
		if _, err := s.db.Exec(ctx, `INSERT INTO email_sends (member_id, kind, sent_to, outcome, detail)
			VALUES ($1, $2, $3, 'failed', $4)`, member, string(out.Kind), sentTo, sendErr.Error()); err != nil {
			s.log.ErrorContext(ctx, "recording a failed email send", "err", err)
		}
		return IssueEnrolmentCode502JSONResponse{Code: "email_not_sent",
			Message: "The email could not be sent, so nothing was issued and your PIN proof is still good. Try again, or issue a slip."}, nil
	}
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
		case c == "enrolment_email_set_by_issuer": // the email changed between our read and our write
			return emailSetByYou(), nil
		case c == "enrolment_email_needs_address":
			return noEmail(), nil
		}
		return nil, err
	}
	out.CodeId, out.ExpiresAt = uuidOf(id), expires
	out.Delivery = EnrolmentSlipDelivery(delivery)
	if delivery == "email" {
		m := email.Masked(sentTo)
		out.SentTo = &m
	}
	return out, nil
}

func noEmail() IssueEnrolmentCode422JSONResponse {
	return IssueEnrolmentCode422JSONResponse{Code: "no_email",
		Message: "This member has no email recorded. The secretary sets it, or issue the code on a slip."}
}

func emailSetByYou() IssueEnrolmentCode409JSONResponse {
	return IssueEnrolmentCode409JSONResponse{Code: "email_set_by_you",
		Message: "You set this member's email, so another officer sends their code by email. You may still issue it on a slip."}
}

// enrolmentEmail: the message an officer receives. It names the society and
// the code (or which half), that it works once within 72 hours to set a PIN,
// and that Argon never asks for it.
func enrolmentEmail(to, society, kind, code, appOrigin string) email.Message {
	where := "in the Argon app, under Activate"
	if appOrigin != "" {
		where = "at " + appOrigin + "/activate"
	}
	what := "Your enrolment code is:\n\n    " + code + "\n\n"
	if kind != string(Whole) {
		what = "This is ONE HALF of your enrolment code. The other half comes in a separate email, from another officer. Enter both halves, in either order:\n\n    " + code + "\n\n"
	}
	return email.Message{To: to,
		Subject: "Your Argon enrolment code, " + society,
		Text: "You are being enrolled as an officer of " + society + " on Argon.\n\n" + what +
			"It works once, within 72 hours, to set your PIN " + where + ". After that your PIN is how you sign in, and this code is worth nothing.\n\n" +
			"Argon will never ask you for this code by phone, message or email. If you did not expect it, tell your society's secretary.\n"}
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
		c.wrong_tries, c.used_at, c.used_device_id, c.replaces_id, c.delivery, c.sent_to
		FROM enrolment_codes c
		LEFT JOIN credential_acts a1 ON a1.id = c.issued_by_act LEFT JOIN users u1 ON u1.id = a1.actor_user_id
		LEFT JOIN credential_acts a2 ON a2.id = c.second_by_act LEFT JOIN users u2 ON u2.id = a2.actor_user_id
		WHERE c.user_id = $1 ORDER BY c.issued_at DESC, c.id`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := GetEnrolmentCodes200JSONResponse{Codes: []EnrolmentCodeRecord{}, EmailDelivery: s.email != nil}
	for rows.Next() {
		var r EnrolmentCodeRecord
		var state string
		var reason *string
		var tries int16
		var delivery string
		var sentTo *string
		if err := rows.Scan(&r.CodeId, &r.IssuedAt, &r.ExpiresAt, &r.IssuedBy, &r.IssuedByOperator, &r.SecondHalfBy, &state,
			&reason, &r.Expired, &tries, &r.UsedAt, &r.UsedDeviceId, &r.Replaces, &delivery, &sentTo); err != nil {
			return nil, err
		}
		dl := EnrolmentCodeRecordDelivery(delivery)
		r.Delivery = &dl
		if sentTo != nil {
			m := email.Masked(*sentTo)
			r.SentTo = &m
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
