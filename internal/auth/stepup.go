package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProvePIN — the step-up (0005). Written against
// test/tier1/tier1_stepup_service_test.go (reviewed; mutation-tested with
// authfake_stepup_test.go before this existed).
//
// The person signed in on sessionToken re-enters their PIN. A right PIN mints
// ONE proof for them, scoped to act (the kind bound here, at mint), good for
// five minutes; the act that uses it spends it. A wrong PIN mints nothing.
//
// One counter, shared with sign-in: every step-up attempt is a sign_in_attempts
// row for the account, so lock_state sees step-up and sign-in failures as one
// run, and a right PIN here ends that run exactly as a sign-in success does.
//
// In production this service's pool is argon_auth: the only role that can
// mint a proof or record an attempt. The API's handlers, which spend proofs,
// cannot (grants.sql; probe P11–P14).
func (s *service) ProvePIN(ctx context.Context, sessionToken, pin string, act ActKind) (Proof, error) {
	var user string
	var device *string
	err := s.d.DB.QueryRow(ctx, `SELECT user_id::text, device_id::text FROM auth_sessions
		WHERE token_digest = $1 AND session_valid($1)`, TokenDigest(sessionToken)).Scan(&user, &device)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proof{}, ErrNoSession
	}
	if err != nil {
		return Proof{}, err
	}
	known := false
	for _, k := range ActKinds {
		known = known || k == act
	}
	if !known {
		return Proof{}, ErrUnknownAct
	}

	var phc string
	if err := s.d.DB.QueryRow(ctx, `SELECT coalesce((SELECT pin_hash FROM auth_credentials WHERE user_id = $1), '')`, user).Scan(&phc); err != nil {
		return Proof{}, err
	}
	locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state($1)`, user)
	if err != nil {
		return Proof{}, err
	}
	ok := s.verify(phc, pin) // always hashed, locked or not, so the answer costs the same
	if locked {
		return Proof{}, ErrTryLater
	}
	if err := s.attempt(ctx, &user, TokenDigest("stepup:"+user), device, ok); err != nil {
		return Proof{}, err
	}
	if !ok {
		return Proof{}, ErrRefused
	}

	p := Proof{Act: act}
	var at time.Time
	if err := s.d.DB.QueryRow(ctx, `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', $2) RETURNING id::text, at`,
		user, string(act)).Scan(&p.ID, &at); err != nil {
		return Proof{}, err
	}
	p.ExpiresAt = at.Add(5 * time.Minute) // 0003: an act accepts a proof entered in the last five minutes
	return p, nil
}

// SignOut ends a live session. The session ends itself (ended_by_session =
// its own id), which 0003 requires of an end made by the person.
func (s *service) SignOut(ctx context.Context, sessionToken string) error {
	tag, err := s.d.DB.Exec(ctx, `UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id
		WHERE token_digest = $1 AND ended_at IS NULL`, TokenDigest(sessionToken))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSession
	}
	return nil
}
