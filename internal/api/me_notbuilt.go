package api

import (
	"context"
	"errors"
)

// GET /me is specified and tested, NOT BUILT: its acceptance tests
// (test/tier1/tier1_api_me_test.go) are awaiting review. Until then it fails
// as "the request failed". Deleted when me.go replaces it.
var errMeNotBuilt = errors.New("GET /me is not built yet (awaiting review of its acceptance tests)")

func (s *server) GetMe(context.Context, GetMeRequestObject) (GetMeResponseObject, error) {
	return nil, errMeNotBuilt
}
