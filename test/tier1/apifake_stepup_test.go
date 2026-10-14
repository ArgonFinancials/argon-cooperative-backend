//go:build tier1

// POST /auth/pin-proofs in the API mutation vehicle — test code, NOT the API.
// It delegates to the reference step-up (fakeService.ProvePIN) and maps its
// answers to HTTP. The batch side of the step-up is in apifake_batch_test.go.
//
// Breaks (ARGON_TEST_API_FAKE=<break>), the endpoint's:
//
//	pin_proof_any_pin      a wrong PIN still mints
//	pin_proof_ignores_lock a locked account still mints
//	pin_proof_any_act      an unknown act is minted for
//	pin_proof_unscoped     the proof names no act
//	unconfigured_500       with no auth service, a bare 500 instead of a 503 that says why
package tier1

import (
	"context"
	"errors"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

func (f *fakeAPI) PostPinProof(ctx context.Context, req api.PostPinProofRequestObject) (api.PostPinProofResponseObject, error) {
	if f.noAuth {
		if f.brk == "unconfigured_500" {
			return nil, errors.New("no auth service") // a bare 500: reads as a defect
		}
		return api.PostPinProof503JSONResponse{Code: "step_up_unavailable", Message: "the PIN step-up is not available on this server yet"}, nil
	}
	if req.Body == nil {
		return api.PostPinProof400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	brk := map[string]string{
		"pin_proof_any_pin":      "stepup_mints_anyway",
		"pin_proof_ignores_lock": "stepup_ignores_lock",
		"pin_proof_any_act":      "stepup_any_act",
		"pin_proof_unscoped":     "stepup_unscoped",
		// An ended session accepted EVERYWHERE: the middleware's break alone is
		// stopped by the service's own session check, and proves nothing here.
		"ended_session_ok": "stepup_ended_session",
	}[f.brk]
	// Finding 36: the state before the money acts were listed.
	if f.brk == "money_acts_unmintable" && (req.Body.Act == "contribution_record" || req.Body.Act == "withdrawal_payout" || req.Body.Act == "withdrawal_authorisation") {
		return api.PostPinProof400JSONResponse{Code: "unknown_act", Message: "no such act"}, nil
	}
	svc := &fakeService{d: auth.Deps{DB: f.db}, brk: brk}
	tok, _ := ctx.Value(ctxToken{}).(string)
	p, err := svc.ProvePIN(ctx, tok, req.Body.Pin, auth.ActKind(req.Body.Act))
	switch {
	case err == nil:
		return api.PostPinProof201JSONResponse{ProofId: uuidOf(p.ID), Act: api.ActKind(p.Act), ExpiresAt: p.ExpiresAt}, nil
	case errors.Is(err, auth.ErrUnknownAct):
		return api.PostPinProof400JSONResponse{Code: "unknown_act", Message: "no such act"}, nil
	case errors.Is(err, auth.ErrNoSession):
		return api.PostPinProof401JSONResponse{Code: "unauthenticated", Message: "sign in again"}, nil
	case errors.Is(err, auth.ErrRefused):
		return api.PostPinProof403JSONResponse{Code: "pin_refused", Message: "that PIN is not right"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return api.PostPinProof429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	}
	return nil, err
}
