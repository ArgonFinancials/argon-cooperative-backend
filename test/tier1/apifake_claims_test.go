//go:build tier1

// THE MUTATION VEHICLE FOR THE CLAIM ENDPOINTS — test code, NOT the API. A
// reference implementation of GET /members/{m}/contribution-account and
// GET/POST /members/{m}/contribution-claims (2026-11-11), breakable one named
// way per run (ARGON_TEST_API_FAKE=<break>):
//
//	claims_any_claimant    anyone signed in claims for any member (an officer, another member)
//	claims_session_raw     the session id is checked as typed, spaces and all
//	claims_repeat_500      a repeat claim goes to the database and answers 500, not the first claim
//	claims_state_stale     a new claim answers "waiting", whatever the database already posted
//	claims_list_society    the list carries every claim in the member's society
//	account_any_reader     anyone signed in reads a member's payment account
//	account_missing_500    a society with no VFD account answers 500, not 422 no_account
package tier1

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
)

var sessionRef = regexp.MustCompile(`^[0-9A-Za-z]{10,64}$`)

func (f *fakeAPI) claimSociety(ctx context.Context, caller, member string, anyone bool) string {
	var society string
	q := `SELECT society_id::text FROM members WHERE id = $1 AND user_id = $2`
	args := []any{member, caller}
	if anyone {
		q, args = `SELECT society_id::text FROM members WHERE id = $1`, []any{member}
	}
	f.db.QueryRow(ctx, q, args...).Scan(&society)
	return society
}

func (f *fakeAPI) GetContributionAccount(ctx context.Context, req api.GetContributionAccountRequestObject) (api.GetContributionAccountResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	society := f.claimSociety(ctx, caller, req.MemberId.String(), f.brk == "account_any_reader")
	if society == "" {
		return api.GetContributionAccount404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	var a api.ContributionAccount
	err := f.db.QueryRow(ctx, `SELECT account_number, account_name FROM vfd_accounts WHERE society_id = $1 AND member_id IS NULL`, society).Scan(&a.AccountNumber, &a.AccountName)
	if errors.Is(err, pgx.ErrNoRows) {
		if f.brk == "account_missing_500" {
			return nil, err
		}
		return api.GetContributionAccount422JSONResponse{Code: "no_account", Message: "the society's VFD account is not set up yet"}, nil
	}
	if err != nil {
		return nil, err
	}
	a.BankName = "VFD Microfinance Bank"
	return api.GetContributionAccount200JSONResponse(a), nil
}

const fakeClaimCols = `s.claim_id::text, s.member_id::text, s.session_id, s.amount_kobo, s.claimed_at, s.state, s.entry_id::text,
	(SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = s.entry_id AND a.role = 'bank')`

func fakeScanClaim(row pgx.Row) (api.ContributionClaim, error) {
	var c api.ContributionClaim
	var id, member, state string
	var amount int64
	var at time.Time
	var entry *string
	var credited *int64
	if err := row.Scan(&id, &member, &c.SessionId, &amount, &at, &state, &entry, &credited); err != nil {
		return c, err
	}
	c.ClaimId, c.MemberId, c.AmountKobo, c.ClaimedAt, c.State = uuidOf(id), uuidOf(member), api.Magnitude(strconv.FormatInt(amount, 10)), at, api.ContributionClaimState(state)
	if entry != nil {
		e := uuidOf(*entry)
		c.EntryId = &e
	}
	if credited != nil {
		v := strconv.FormatInt(*credited, 10)
		c.CreditedKobo = &v
	}
	return c, nil
}

func (f *fakeAPI) claimOf(ctx context.Context, member, session string) (api.ContributionClaim, bool, error) {
	c, err := fakeScanClaim(f.db.QueryRow(ctx, `SELECT `+fakeClaimCols+` FROM contribution_claim_states s WHERE s.member_id = $1 AND s.session_id = $2`, member, session))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (f *fakeAPI) ClaimContribution(ctx context.Context, req api.ClaimContributionRequestObject) (api.ClaimContributionResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	society := f.claimSociety(ctx, caller, member, f.brk == "claims_any_claimant")
	if society == "" {
		return api.ClaimContribution404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	b := req.Body
	if b == nil {
		return api.ClaimContribution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	amount, err := strconv.ParseInt(string(b.AmountKobo), 10, 64)
	if err != nil || amount < 0 {
		return api.ClaimContribution400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	session := strings.Join(strings.Fields(b.SessionId), "")
	if f.brk == "claims_session_raw" {
		session = b.SessionId
	}
	if !sessionRef.MatchString(session) {
		return api.ClaimContribution422JSONResponse{Code: "bad_session", Message: "a session id is 10 to 64 letters and digits"}, nil
	}
	if f.brk != "claims_repeat_500" {
		if c, found, err := f.claimOf(ctx, member, session); err != nil || found {
			if err != nil {
				return nil, err
			}
			return api.ClaimContribution200JSONResponse(c), nil
		}
	}
	if amount == 0 {
		return api.ClaimContribution422JSONResponse{Code: "zero_amount", Message: "a contribution of nothing is not claimed"}, nil
	}
	// The claimant is the member's own person: the database refuses anyone else's name.
	var user string
	f.db.QueryRow(ctx, `SELECT user_id::text FROM members WHERE id = $1`, member).Scan(&user)
	if _, err := f.db.Exec(ctx, `INSERT INTO contribution_claims (society_id, member_id, user_id, session_id, amount_kobo) VALUES ($1, $2, $3, $4, $5)`,
		society, member, user, session, amount); err != nil {
		return nil, err
	}
	c, found, err := f.claimOf(ctx, member, session)
	if err != nil || !found {
		return nil, errors.Join(err, errors.New("the claim just made is not there"))
	}
	if f.brk == "claims_state_stale" {
		c.State, c.EntryId, c.CreditedKobo = api.ContributionClaimState("waiting"), nil, nil
	}
	return api.ClaimContribution201JSONResponse(c), nil
}

func (f *fakeAPI) GetContributionClaims(ctx context.Context, req api.GetContributionClaimsRequestObject) (api.GetContributionClaimsResponseObject, error) {
	caller := ctx.Value(ctxUser{}).(string)
	member := req.MemberId.String()
	society := f.claimSociety(ctx, caller, member, false)
	if society == "" {
		return api.GetContributionClaims404JSONResponse{Code: "not_found", Message: "no such member"}, nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	where, arg := `s.member_id = $1`, member
	if f.brk == "claims_list_society" {
		where, arg = `s.society_id = $1`, society
	}
	rows, err := f.db.Query(ctx, `SELECT `+fakeClaimCols+` FROM contribution_claim_states s WHERE `+where+` ORDER BY s.claimed_at DESC, s.claim_id LIMIT $2`, arg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.ContributionClaimList{Claims: []api.ContributionClaim{}}
	for rows.Next() {
		c, err := fakeScanClaim(rows)
		if err != nil {
			return nil, err
		}
		out.Claims = append(out.Claims, c)
	}
	return api.GetContributionClaims200JSONResponse(out), rows.Err()
}
