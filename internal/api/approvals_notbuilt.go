package api

import (
	"context"
	"errors"
)

// The approval endpoints (PRD 8.4; 0007, 0010) are specified and tested, NOT
// BUILT: the acceptance tests (test/tier1/tier1_api_approvals_test.go) are
// awaiting review. Until then each fails as "the request failed" rather than
// signing or rejecting anything. Deleted when approvals.go replaces it.
var errApprovalsNotBuilt = errors.New("the approval endpoints are not built yet (awaiting review of their acceptance tests)")

func (s *server) GetApproval(context.Context, GetApprovalRequestObject) (GetApprovalResponseObject, error) {
	return nil, errApprovalsNotBuilt
}

func (s *server) SignApproval(context.Context, SignApprovalRequestObject) (SignApprovalResponseObject, error) {
	return nil, errApprovalsNotBuilt
}

func (s *server) RejectApproval(context.Context, RejectApprovalRequestObject) (RejectApprovalResponseObject, error) {
	return nil, errApprovalsNotBuilt
}
