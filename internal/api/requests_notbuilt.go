package api

import (
	"context"
	"errors"
)

// Raising withdrawal requests and listing requests (0016) are specified and
// tested, NOT BUILT: the acceptance tests (test/tier1/tier1_api_requests_test.go)
// are awaiting review, with 0016's. Until then each fails as "the request
// failed". Deleted when requests.go replaces it.
var errRequestsNotBuilt = errors.New("withdrawal requests are not built yet (awaiting review of their acceptance tests)")

func (s *server) RaiseWithdrawalRequest(context.Context, RaiseWithdrawalRequestRequestObject) (RaiseWithdrawalRequestResponseObject, error) {
	return nil, errRequestsNotBuilt
}

func (s *server) GetMemberWithdrawalRequests(context.Context, GetMemberWithdrawalRequestsRequestObject) (GetMemberWithdrawalRequestsResponseObject, error) {
	return nil, errRequestsNotBuilt
}

func (s *server) GetSocietyApprovalRequests(context.Context, GetSocietyApprovalRequestsRequestObject) (GetSocietyApprovalRequestsResponseObject, error) {
	return nil, errRequestsNotBuilt
}
