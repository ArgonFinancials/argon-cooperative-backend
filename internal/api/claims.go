package api

// A MEMBER'S CONTRIBUTION, CLAIMED (0032; LEDGER_CONTRACT §12, 2026-11-11).
// The member pays into their society's VFD account and names the payment by
// its session id and amount. The claim moves no money and needs no step-up:
// the database credits it, in whichever transaction arrives second, when VFD
// confirms a payment with that session id and exactly that amount and no
// other member claims it. Only the member: anyone else gets 404.

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var sessionShape = regexp.MustCompile(`^[0-9A-Za-z]{10,64}$`)

const vfdBankName = "VFD Microfinance Bank"

// ownMember: the society of the caller's own member record, or "" when the
// member does not exist or is not the caller's.
func (s *server) ownMember(ctx context.Context, member string) (string, error) {
	var society string
	err := s.db.QueryRow(ctx, `SELECT society_id::text FROM members WHERE id = $1 AND user_id = $2`, member, callerOf(ctx)).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return society, err
}

func (s *server) GetContributionAccount(ctx context.Context, req GetContributionAccountRequestObject) (GetContributionAccountResponseObject, error) {
	society, err := s.ownMember(ctx, req.MemberId.String())
	if err != nil {
		return nil, err
	}
	if society == "" {
		return GetContributionAccount404JSONResponse(notFound("member")), nil
	}
	var a ContributionAccount
	err = s.db.QueryRow(ctx, `SELECT account_number, account_name FROM vfd_accounts WHERE society_id = $1 AND member_id IS NULL`, society).
		Scan(&a.AccountNumber, &a.AccountName)
	if errors.Is(err, pgx.ErrNoRows) {
		return GetContributionAccount422JSONResponse{Code: "no_account",
			Message: "Your society's VFD account is not set up yet, so nothing can be paid in through Argon. Ask your treasurer."}, nil
	}
	if err != nil {
		return nil, err
	}
	a.BankName = vfdBankName
	return GetContributionAccount200JSONResponse(a), nil
}

const claimColumns = `s.claim_id::text, s.member_id::text, s.session_id, s.amount_kobo, s.claimed_at, s.state, s.entry_id::text,
	(SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = s.entry_id AND a.role = 'bank')`

func scanClaim(row pgx.Row) (ContributionClaim, error) {
	var c ContributionClaim
	var id, member, state string
	var amount int64
	var at time.Time
	var entry *string
	var credited *int64
	if err := row.Scan(&id, &member, &c.SessionId, &amount, &at, &state, &entry, &credited); err != nil {
		return c, err
	}
	c.ClaimId, c.MemberId, c.AmountKobo, c.ClaimedAt, c.State = uuidOf(id), uuidOf(member), magnitudeOf(amount), at, ContributionClaimState(state)
	c.EntryId = uuidPtr(entry)
	if credited != nil {
		v := string(magnitudeOf(*credited))
		c.CreditedKobo = &v
	}
	return c, nil
}

// claimOf: this member's claim on this session, if any.
func (s *server) claimOf(ctx context.Context, member, session string) (ContributionClaim, bool, error) {
	c, err := scanClaim(s.db.QueryRow(ctx, `SELECT `+claimColumns+` FROM contribution_claim_states s WHERE s.member_id = $1 AND s.session_id = $2`, member, session))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *server) ClaimContribution(ctx context.Context, req ClaimContributionRequestObject) (ClaimContributionResponseObject, error) {
	member := req.MemberId.String()
	society, err := s.ownMember(ctx, member)
	if err != nil {
		return nil, err
	}
	if society == "" {
		return ClaimContribution404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return ClaimContribution400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	amount, ok := magnitudeOk(b.AmountKobo)
	if !ok {
		return ClaimContribution400JSONResponse{Code: "bad_request", Message: "amountKobo is a non-negative kobo string"}, nil
	}
	session := strings.Join(strings.Fields(b.SessionId), "")
	if !sessionShape.MatchString(session) {
		return ClaimContribution422JSONResponse{Code: "bad_session",
			Message: "A session id is 10 to 64 letters and digits, as printed on your bank's receipt (usually 30 digits)."}, nil
	}
	// Already claimed: the first claim, nothing written.
	if c, found, err := s.claimOf(ctx, member, session); err != nil || found {
		if err != nil {
			return nil, err
		}
		return ClaimContribution200JSONResponse(c), nil
	}
	if amount == 0 {
		return ClaimContribution422JSONResponse{Code: "zero_amount", Message: "a contribution of nothing is not claimed"}, nil
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO contribution_claims (society_id, member_id, user_id, session_id, amount_kobo) VALUES ($1, $2, $3, $4, $5)`,
		society, member, callerOf(ctx), session, amount); err != nil {
		if refusedBy(err) == "claim_once_per_member" { // a retry that raced the first
			if c, found, ferr := s.claimOf(ctx, member, session); ferr == nil && found {
				return ClaimContribution200JSONResponse(c), nil
			}
		}
		return nil, err
	}
	c, found, err := s.claimOf(ctx, member, session)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("claims: the claim just made is not there")
	}
	return ClaimContribution201JSONResponse(c), nil
}

func (s *server) GetContributionClaims(ctx context.Context, req GetContributionClaimsRequestObject) (GetContributionClaimsResponseObject, error) {
	member := req.MemberId.String()
	society, err := s.ownMember(ctx, member)
	if err != nil {
		return nil, err
	}
	if society == "" {
		return GetContributionClaims404JSONResponse(notFound("member")), nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	rows, err := s.db.Query(ctx, `SELECT `+claimColumns+` FROM contribution_claim_states s WHERE s.member_id = $1 ORDER BY s.claimed_at DESC, s.claim_id LIMIT $2`, member, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := ContributionClaimList{Claims: []ContributionClaim{}}
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out.Claims = append(out.Claims, c)
	}
	return GetContributionClaims200JSONResponse(out), rows.Err()
}
