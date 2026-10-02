package api

import (
	"context"
	"errors"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/auth"
)

// POST /auth/pin-proofs — the PIN step-up (0005). Written against
// test/tier1/tier1_api_stepup_test.go (reviewed; mutation-tested with
// apifake_stepup_test.go before this existed).
//
// This handler does not mint: the auth service does, through its own role
// (argon_auth). The handlers that SPEND a proof (postAll) run as argon_app,
// which cannot mint one — so the path that uses a proof cannot have made it.
func (s *server) PostPinProof(ctx context.Context, req PostPinProofRequestObject) (PostPinProofResponseObject, error) {
	if s.auth == nil {
		return nil, errors.New("api: no auth service configured for the step-up")
	}
	if req.Body == nil {
		return PostPinProof400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	tok, _ := ctx.Value(tokenKey{}).(string)
	p, err := s.auth.ProvePIN(ctx, tok, req.Body.Pin, auth.ActKind(req.Body.Act))
	switch {
	case err == nil:
		var id openapi_types.UUID
		if err := id.UnmarshalText([]byte(p.ID)); err != nil {
			return nil, err
		}
		return PostPinProof201JSONResponse{ProofId: id, Act: ActKind(p.Act), ExpiresAt: p.ExpiresAt}, nil
	case errors.Is(err, auth.ErrUnknownAct):
		return PostPinProof400JSONResponse{Code: "unknown_act", Message: "no such act"}, nil
	case errors.Is(err, auth.ErrNoSession):
		return PostPinProof401JSONResponse{Code: "unauthenticated", Message: "sign in again"}, nil
	case errors.Is(err, auth.ErrRefused):
		return PostPinProof403JSONResponse{Code: "pin_refused", Message: "that PIN is not right"}, nil
	case errors.Is(err, auth.ErrTryLater):
		return PostPinProof429JSONResponse{Code: "try_later", Message: "too many attempts; try again later"}, nil
	}
	return nil, err
}
