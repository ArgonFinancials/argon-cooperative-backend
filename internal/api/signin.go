package api

import (
	"context"
	"errors"
	"time"

	"github.com/argon/backend/internal/auth"
)

// Sign-in over HTTP: POST /auth/sign-in, /auth/codes/verify, /auth/sign-out.
// Written against test/tier1/tier1_api_signin_test.go (reviewed; mutation-
// tested with apifake_signin_test.go before this existed).
//
// The rules are the auth service's and the database's (0003): the uniform
// refusal, D-21's one-person rule, lockout, and the session's length, which
// follows the highest privilege the person holds. This layer only carries
// them to HTTP without undoing any: one answer for every refusal, nothing
// looked up that the service did not, and `surface` read by nobody.

var signInUnavailable = Problem{Code: "sign_in_unavailable",
	Message: "Sign-in is not available on this server yet: it runs without an auth service (pending the argon_auth database connection). Nobody was signed in."}

// The MEMBER reads this (decided 2026-10-22: any field a client can show, some
// client will). What happened and what to do next; nothing about SMS providers
// or what is temporary. The cause is for the client, in Reason
// ("sms_unavailable"), and for us, in the adapter's Error-level log line.
const smsUnavailableMessage = "You are signing in on a new device. We cannot send you a code to confirm it, so an officer of your society needs to confirm this device with you in person."

func (s *server) PostSignIn(ctx context.Context, req PostSignInRequestObject) (PostSignInResponseObject, error) {
	if s.auth == nil {
		return PostSignIn503JSONResponse(signInUnavailable), nil
	}
	if req.Body == nil {
		return PostSignIn400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	dev := ""
	if req.Body.DeviceToken != nil {
		dev = *req.Body.DeviceToken
	}
	r, err := s.auth.SignIn(ctx, auth.SignInRequest{Credential: req.Body.Credential, PIN: req.Body.Pin, DeviceToken: dev})
	switch {
	case errors.Is(err, auth.ErrSMSUnavailable):
		// A new device, and no code can be sent: an officer confirms it.
		// Loudly logged by the adapter; said plainly to the person.
		why, msg := "sms_unavailable", smsUnavailableMessage
		return PostSignIn200JSONResponse{Outcome: SeeOfficer, Reason: &why, Message: &msg}, nil
	case errors.Is(err, auth.ErrCodeNotSent):
		return codeNotSent(), nil // try again; never routed to an officer by itself
	case errors.Is(err, auth.ErrResendTooSoon):
		return resendTooSoon(), nil // never the lockout's answer
	case errors.Is(err, auth.ErrRefused):
		return PostSignIn401JSONResponse{Code: "refused", Message: "those details do not match an account"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return PostSignIn429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case err != nil:
		return nil, err
	case r.SessionToken != "":
		out := PostSignIn200JSONResponse{Outcome: SignedIn, SessionToken: &r.SessionToken}
		if out.ExpiresAt, err = s.sessionExpiry(ctx, r.SessionToken); err != nil {
			return nil, err
		}
		if r.DeviceToken != "" {
			out.DeviceToken = &r.DeviceToken
		}
		return out, nil
	case r.Challenge != "":
		return codeSent(r.Challenge, r.Delayed), nil
	}
	return PostSignIn200JSONResponse{Outcome: SeeOfficer}, nil
}

func (s *server) PostVerifyCode(ctx context.Context, req PostVerifyCodeRequestObject) (PostVerifyCodeResponseObject, error) {
	if s.auth == nil {
		return PostVerifyCode503JSONResponse(signInUnavailable), nil
	}
	if req.Body == nil {
		return PostVerifyCode400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	r, err := s.auth.VerifyCode(ctx, req.Body.Challenge, req.Body.Code)
	switch {
	case errors.Is(err, auth.ErrRefused):
		return PostVerifyCode401JSONResponse{Code: "refused", Message: "that code is not right"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return PostVerifyCode429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case err != nil:
		return nil, err
	}
	out := PostVerifyCode200JSONResponse{Outcome: SignedIn, SessionToken: &r.SessionToken}
	if out.ExpiresAt, err = s.sessionExpiry(ctx, r.SessionToken); err != nil {
		return nil, err
	}
	if r.DeviceToken != "" {
		out.DeviceToken = &r.DeviceToken
	}
	return out, nil
}

func (s *server) PostSignOut(ctx context.Context, _ PostSignOutRequestObject) (PostSignOutResponseObject, error) {
	if s.auth == nil {
		return PostSignOut503JSONResponse(signInUnavailable), nil
	}
	tok, _ := ctx.Value(tokenKey{}).(string)
	if err := s.auth.SignOut(ctx, tok); errors.Is(err, auth.ErrNoSession) {
		return PostSignOut401JSONResponse{Code: "unauthenticated", Message: "sign in again"}, nil
	} else if err != nil {
		return nil, err
	}
	return PostSignOut204Response{}, nil
}

// sessionExpiry: what session_valid will enforce, read back for the client —
// never computed from anything the client said.
func (s *server) sessionExpiry(ctx context.Context, token string) (*time.Time, error) {
	var at time.Time
	if err := s.db.QueryRow(ctx, `SELECT started_at + CASE WHEN privileged OR person_holds_post(user_id)
		THEN interval '8 hours' ELSE interval '30 days' END FROM auth_sessions WHERE token_digest = $1`,
		auth.TokenDigest(token)).Scan(&at); err != nil {
		return nil, err
	}
	return &at, nil
}
