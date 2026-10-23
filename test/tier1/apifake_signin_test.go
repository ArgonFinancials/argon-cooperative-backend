//go:build tier1

// Sign-in over HTTP in the API mutation vehicle — test code, NOT the API.
// POST /auth/sign-in, /auth/codes/verify, /auth/sign-out, delegating to the
// reference service (fakeService) and mapping its answers to HTTP.
//
// Breaks (ARGON_TEST_API_FAKE=<break>):
//
//	unknown_says_why     an unregistered credential is answered differently from a wrong PIN
//	unknown_skips_hash   an unregistered credential skips the PIN hash, so it answers sooner
//	shared_first_match   a credential shared by two people signs in whichever is found first
//	signin_ignores_lock  a locked account still signs in
//	expiry_from_surface  the session's reported length follows the surface, not the person
//	signout_noop         sign-out answers 204 and ends nothing
//	sms_failure_500      a refused code send is a bare 500
//	sms_failure_silent   a refused code send is reported as code_sent: the worst outcome
//	sms_message_for_developers  the member is shown a developer's note about the SMS provider (until 2026-10-22, the real wording)
//	verify_any_code      any code completes a new-device sign-in
//	no_token_reaches     a request with no token reaches the handlers as nobody
//	router_errors_plain  the generated router's own errors answer text/plain, not the JSON Problem
package tier1

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

var signinServiceBreak = map[string]string{
	"unknown_says_why":    "distinct_unknown",
	"unknown_skips_hash":  "timing_fast_unknown",
	"shared_first_match":  "first_match",
	"signin_ignores_lock": "signin_ignores_lock",
}

func (f *fakeAPI) authDeps() auth.Deps {
	if f.deps.DB == nil {
		f.deps = auth.Deps{DB: f.db, SMS: &fakeSMS{}, Notify: &fakeNotifier{}, CodeKey: []byte("test-code-key-not-a-secret"), Argon: testArgon}
	}
	return f.deps
}

// expiry: what the database's session_valid will enforce — 8 h for anyone
// holding a post, 30 d otherwise — read back for the client.
func (f *fakeAPI) expiry(ctx context.Context, token string, surface *api.SignInRequestSurface) *time.Time {
	var at time.Time
	if err := f.db.QueryRow(ctx, `SELECT started_at + CASE WHEN privileged OR person_holds_post(user_id)
		THEN interval '8 hours' ELSE interval '30 days' END FROM auth_sessions WHERE token_digest = $1`, sha(token)).Scan(&at); err != nil {
		return nil
	}
	if f.brk == "expiry_from_surface" && surface != nil && *surface == api.SignInRequestSurface("member") {
		at = time.Now().Add(30 * 24 * time.Hour)
	}
	return &at
}

func (f *fakeAPI) PostSignIn(ctx context.Context, req api.PostSignInRequestObject) (api.PostSignInResponseObject, error) {
	if f.noAuth {
		if f.brk == "unconfigured_500" {
			return nil, errors.New("no auth service")
		}
		return api.PostSignIn503JSONResponse{Code: "sign_in_unavailable", Message: "sign-in is not available on this server yet"}, nil
	}
	if req.Body == nil {
		return api.PostSignIn400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	svc := &fakeService{d: f.authDeps(), brk: signinServiceBreak[f.brk]}
	dev := ""
	if req.Body.DeviceToken != nil {
		dev = *req.Body.DeviceToken
	}
	r, err := svc.SignIn(ctx, auth.SignInRequest{Credential: req.Body.Credential, PIN: req.Body.Pin, DeviceToken: dev})
	switch {
	case errors.Is(err, auth.ErrSMSUnavailable):
		switch f.brk {
		case "sms_failure_500":
			return nil, err
		case "sms_failure_silent":
			c := "unsent"
			return api.PostSignIn200JSONResponse{Outcome: api.CodeSent, Challenge: &c}, nil
		}
		why, msg := "sms_unavailable", "You are signing in on a new device. We cannot send you a code to confirm it, so an officer of your society needs to confirm this device with you in person."
		if f.brk == "sms_message_for_developers" {
			msg = "TEMPORARY: sign-in codes cannot be sent yet (no SMS provider), so a new device is confirmed in person by an officer of your society."
		}
		return api.PostSignIn200JSONResponse{Outcome: api.SeeOfficer, Reason: &why, Message: &msg}, nil
	case errors.Is(err, auth.ErrRefused):
		return api.PostSignIn401JSONResponse{Code: "refused", Message: "those details do not match an account"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return api.PostSignIn429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case err != nil:
		return nil, err
	case r.SessionToken != "":
		out := api.PostSignIn200JSONResponse{Outcome: api.SignedIn, SessionToken: &r.SessionToken, ExpiresAt: f.expiry(ctx, r.SessionToken, req.Body.Surface)}
		if r.DeviceToken != "" {
			out.DeviceToken = &r.DeviceToken
		}
		return out, nil
	case r.Challenge != "":
		return api.PostSignIn200JSONResponse{Outcome: api.CodeSent, Challenge: &r.Challenge}, nil
	}
	return api.PostSignIn200JSONResponse{Outcome: api.SeeOfficer}, nil
}

func (f *fakeAPI) PostVerifyCode(ctx context.Context, req api.PostVerifyCodeRequestObject) (api.PostVerifyCodeResponseObject, error) {
	if req.Body == nil {
		return api.PostVerifyCode400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	svc := &fakeService{d: f.authDeps()}
	r, err := svc.VerifyCode(ctx, req.Body.Challenge, req.Body.Code)
	if errors.Is(err, auth.ErrRefused) && f.brk == "verify_any_code" {
		var user string
		if f.db.QueryRow(ctx, `SELECT user_id::text FROM auth_codes WHERE id::text = $1`, strings.SplitN(req.Body.Challenge, ":", 2)[0]).Scan(&user) == nil {
			tok := token()
			q, args := sessionInsert("user_id, token_digest", "", user, sha(tok))
			f.db.Exec(ctx, q, args...)
			r, err = auth.Result{SessionToken: tok}, nil
		}
	}
	switch {
	case errors.Is(err, auth.ErrRefused):
		return api.PostVerifyCode401JSONResponse{Code: "refused", Message: "that code is not right"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return api.PostVerifyCode429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	case err != nil:
		return nil, err
	}
	out := api.PostVerifyCode200JSONResponse{Outcome: api.SignedIn, SessionToken: &r.SessionToken, ExpiresAt: f.expiry(ctx, r.SessionToken, nil)}
	if r.DeviceToken != "" {
		out.DeviceToken = &r.DeviceToken
	}
	return out, nil
}

func (f *fakeAPI) PostSignOut(ctx context.Context, _ api.PostSignOutRequestObject) (api.PostSignOutResponseObject, error) {
	tok, _ := ctx.Value(ctxToken{}).(string)
	if f.brk != "signout_noop" {
		if _, err := f.db.Exec(ctx, `UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id
			WHERE token_digest = $1 AND ended_at IS NULL`, sha(tok)); err != nil {
			return nil, err
		}
	}
	return api.PostSignOut204Response{}, nil
}
