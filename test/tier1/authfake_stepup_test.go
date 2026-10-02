//go:build tier1

// ProvePIN in the service mutation vehicle — test code, NOT the service.
// The reference step-up, breakable one named way per run
// (ARGON_TEST_AUTH_FAKE=<break>), for tier1_stepup_service_test.go.
//
// Breaks:
//
//	stepup_not_counted     a wrong PIN at step-up is not recorded as a failure
//	stepup_own_counter     step-up failures are counted apart from sign-in's
//	stepup_ignores_lock    a locked account can still step up
//	stepup_mints_anyway    a wrong PIN still mints a proof
//	stepup_unscoped        the proof names no act
//	stepup_any_act         any act name is accepted and minted for
//	stepup_ended_session   an ended session can still step up
//	stepup_expiry_wrong    the proof is reported to last an hour, not five minutes
//	stepup_success_silent  a right PIN at step-up is not recorded, so it ends no run
//	stepup_ignores_success the step-up's lock counts failures since ANY time, not since the last success
package tier1

import (
	"context"
	"time"

	"github.com/argon/backend/internal/auth"
)

func (f *fakeService) ProvePIN(ctx context.Context, sessionToken, pin string, act auth.ActKind) (auth.Proof, error) {
	valid := `AND session_valid($1)`
	if f.brk == "stepup_ended_session" {
		valid = ``
	}
	var user string
	if err := f.d.DB.QueryRow(ctx, `SELECT user_id::text FROM auth_sessions WHERE token_digest = $1 `+valid, sha(sessionToken)).Scan(&user); err != nil {
		return auth.Proof{}, auth.ErrNoSession
	}
	known := f.brk == "stepup_any_act"
	for _, k := range auth.ActKinds {
		known = known || k == act
	}
	if !known {
		return auth.Proof{}, auth.ErrUnknownAct
	}

	// The SAME counter as sign-in: the account's.
	key := sha("user:" + user)
	if f.brk == "stepup_own_counter" {
		key = sha("stepup:" + user)
	}
	failures := f.recentFailures(ctx, key)
	if f.brk == "stepup_ignores_success" {
		f.d.DB.QueryRow(ctx, `SELECT count(*) FROM sign_in_attempts WHERE credential_digest = $1 AND NOT succeeded
			AND at > now() - interval '15 minutes'`, key).Scan(&failures)
	}
	if f.brk != "stepup_ignores_lock" && failures >= 5 {
		return auth.Proof{}, auth.ErrTryLater
	}
	var phc string
	f.d.DB.QueryRow(ctx, `SELECT pin_hash FROM auth_credentials WHERE user_id = $1`, user).Scan(&phc)
	ok := phc != "" && verifyPHC(phc, pin)
	if phc == "" {
		verifyPHC(dummyPHC, pin)
	}
	if (ok && f.brk != "stepup_success_silent") || (!ok && f.brk != "stepup_not_counted") {
		f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, $3)`, user, key, ok)
	}
	if !ok && f.brk != "stepup_mints_anyway" {
		return auth.Proof{}, auth.ErrRefused
	}

	var kind any = string(act)
	if f.brk == "stepup_unscoped" {
		kind = nil
	}
	p := auth.Proof{Act: act}
	var at time.Time
	if err := f.d.DB.QueryRow(ctx, `INSERT INTO auth_proofs (user_id, method, act_kind) VALUES ($1, 'pin', $2) RETURNING id::text, at`,
		user, kind).Scan(&p.ID, &at); err != nil {
		return auth.Proof{}, err
	}
	p.ExpiresAt = at.Add(5 * time.Minute)
	if f.brk == "stepup_expiry_wrong" {
		p.ExpiresAt = at.Add(time.Hour)
	}
	return p, nil
}
