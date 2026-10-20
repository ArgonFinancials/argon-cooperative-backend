package api

import (
	"context"
	"errors"
)

// A member's withdrawable figure and the withdrawal quote are specified and
// tested, NOT BUILT: the acceptance tests (test/tier1/tier1_api_balance_test.go)
// are awaiting review. Until then each fails as "the request failed". Deleted
// when balance.go replaces it.
var errBalanceNotBuilt = errors.New("the withdrawable figure and the quote are not built yet (awaiting review of their acceptance tests)")

func (s *server) GetMemberWithdrawable(context.Context, GetMemberWithdrawableRequestObject) (GetMemberWithdrawableResponseObject, error) {
	return nil, errBalanceNotBuilt
}

func (s *server) GetWithdrawalQuote(context.Context, GetWithdrawalQuoteRequestObject) (GetWithdrawalQuoteResponseObject, error) {
	return nil, errBalanceNotBuilt
}
