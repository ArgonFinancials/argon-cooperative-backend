package api

import (
	"context"
	"errors"
)

// The money endpoints (0011, 0012: record a contribution, authorise a payout,
// release a hold, record a payout) are specified and tested, NOT BUILT: the
// acceptance tests (test/tier1/tier1_api_money_test.go) are awaiting review.
// Until then each fails as "the request failed" rather than recording or
// authorising anything. Deleted when money.go replaces it.
var errMoneyNotBuilt = errors.New("the money endpoints are not built yet (awaiting review of their acceptance tests)")

func (s *server) RecordContribution(context.Context, RecordContributionRequestObject) (RecordContributionResponseObject, error) {
	return nil, errMoneyNotBuilt
}

func (s *server) AuthorisePayout(context.Context, AuthorisePayoutRequestObject) (AuthorisePayoutResponseObject, error) {
	return nil, errMoneyNotBuilt
}

func (s *server) ReleaseHold(context.Context, ReleaseHoldRequestObject) (ReleaseHoldResponseObject, error) {
	return nil, errMoneyNotBuilt
}

func (s *server) RecordPayout(context.Context, RecordPayoutRequestObject) (RecordPayoutResponseObject, error) {
	return nil, errMoneyNotBuilt
}
