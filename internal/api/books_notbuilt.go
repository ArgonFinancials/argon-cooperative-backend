package api

import (
	"context"
	"errors"
)

// The society's books (LEDGER_CONTRACT §5) are specified and tested, NOT
// BUILT: the acceptance tests (test/tier1/tier1_api_books_test.go) are awaiting
// review. Until then each read fails as "the request failed" rather than
// answering with anything a screen could mistake for the books. This file is
// deleted when books.go, written against the approved tests, replaces it.
var errBooksNotBuilt = errors.New("the society's books are not built yet (awaiting review of their acceptance tests)")

func (s *server) GetChartOfAccounts(context.Context, GetChartOfAccountsRequestObject) (GetChartOfAccountsResponseObject, error) {
	return nil, errBooksNotBuilt
}

func (s *server) GetTrialBalance(context.Context, GetTrialBalanceRequestObject) (GetTrialBalanceResponseObject, error) {
	return nil, errBooksNotBuilt
}

func (s *server) GetGeneralLedger(context.Context, GetGeneralLedgerRequestObject) (GetGeneralLedgerResponseObject, error) {
	return nil, errBooksNotBuilt
}

func (s *server) GetJournal(context.Context, GetJournalRequestObject) (GetJournalResponseObject, error) {
	return nil, errBooksNotBuilt
}
