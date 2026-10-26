package api

import "context"

// A member's withdrawable figure and the withdrawal quote are specified and
// tested, NOT BUILT: their acceptance tests (test/tier1/tier1_api_balance_test.go)
// wait for the balance stage. Each is marked x-argon-built: false in
// api/openapi.yaml and answers 501 not_built, never a 500 that reads as a
// crash (2026-10-26). Building one changes three things in one commit: its
// flag to true, its 501 out of the spec, and its handler here deleted.
// TestApiSpec02 refuses any of the three alone.
var notBuilt = Problem{Code: "not_built", Message: "This is not available yet."}

func (s *server) GetMemberWithdrawable(context.Context, GetMemberWithdrawableRequestObject) (GetMemberWithdrawableResponseObject, error) {
	return GetMemberWithdrawable501JSONResponse(notBuilt), nil
}

func (s *server) GetWithdrawalQuote(context.Context, GetWithdrawalQuoteRequestObject) (GetWithdrawalQuoteResponseObject, error) {
	return GetWithdrawalQuote501JSONResponse(notBuilt), nil
}
