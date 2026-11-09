package auth

import (
	"context"
	"errors"
)

// errEnrolNotBuilt: Enrol is written after its tests (tier1_enrol_service_test.go)
// have been shown red against it.
var errEnrolNotBuilt = errors.New("auth: Enrol is not built yet")

func (s *service) Enrol(ctx context.Context, req EnrolRequest) (Result, error) {
	return Result{}, errEnrolNotBuilt
}
