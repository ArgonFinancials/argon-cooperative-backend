package api

import (
	"context"
	"errors"
)

// THE REGISTER over HTTP (2026-11-10). Stubs: written against
// test/tier1/tier1_api_members_test.go, which is red against them.

var errMembersNotBuilt = errors.New("api: the register is not built yet")

func (s *server) FindMembers(ctx context.Context, req FindMembersRequestObject) (FindMembersResponseObject, error) {
	return nil, errMembersNotBuilt
}

func (s *server) RegisterMember(ctx context.Context, req RegisterMemberRequestObject) (RegisterMemberResponseObject, error) {
	return nil, errMembersNotBuilt
}

func (s *server) RegisterMembers(ctx context.Context, req RegisterMembersRequestObject) (RegisterMembersResponseObject, error) {
	return nil, errMembersNotBuilt
}

func (s *server) ListBranches(ctx context.Context, req ListBranchesRequestObject) (ListBranchesResponseObject, error) {
	return nil, errMembersNotBuilt
}
