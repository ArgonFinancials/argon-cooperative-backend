package api

import (
	"context"
	"errors"
)

// The loan read side (LOAN_SHAPE.md §1, §5, §6) is specified and tested, NOT
// BUILT: the acceptance tests (test/tier1/tier1_api_loans_test.go) are
// awaiting review. Until then each read fails as "the request failed" rather
// than answering with anything a screen could mistake for a loan's position.
// This file is deleted when loans.go, written against the approved tests,
// replaces it.
var errLoansNotBuilt = errors.New("the loan read side is not built yet (awaiting review of its acceptance tests)")

func (s *server) GetLoan(context.Context, GetLoanRequestObject) (GetLoanResponseObject, error) {
	return nil, errLoansNotBuilt
}

func (s *server) GetMemberLoans(context.Context, GetMemberLoansRequestObject) (GetMemberLoansResponseObject, error) {
	return nil, errLoansNotBuilt
}

func (s *server) GetSocietyLoans(context.Context, GetSocietyLoansRequestObject) (GetSocietyLoansResponseObject, error) {
	return nil, errLoansNotBuilt
}
