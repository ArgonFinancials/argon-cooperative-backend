//go:build tier1

// ACCEPTANCE TESTS — the PIN step-up in the SERVICE: auth.Service.ProvePIN.
// Written before it, for review. Same harness as the sign-in service tests:
// the real service by default, the fake (authfake_stepup_test.go) with
// ARGON_TEST_AUTH_FAKE=<break>.
//
// What it must make true (the user's terms, 2026-10-01):
//   - a right PIN on a live session mints ONE proof, for the session holder,
//     scoped to the act asked for, good for five minutes;
//   - a wrong PIN mints nothing and counts towards the SAME per-account
//     lockout as sign-in — same counter, same escalation — in both
//     directions, or a stolen session is an unlimited PIN oracle.
//
// Not specified: whether a RIGHT PIN at step-up ends a run of failures, as a
// sign-in success does. Either is safe; nothing here depends on it.
package tier1

import (
	"errors"
	"testing"
	"time"

	"github.com/argon/backend/internal/auth"
)

func (s *svcT) proofsOf(user string) int {
	s.t.Helper()
	var n int
	fixture(s.t, db.QueryRow(bg(), `SELECT count(*) FROM auth_proofs WHERE user_id = $1`, user).Scan(&n), "proofs")
	return n
}

func (s *svcT) failuresOf(user string) int {
	s.t.Helper()
	var n int
	fixture(s.t, db.QueryRow(bg(), `SELECT count(*) FROM sign_in_attempts WHERE user_id = $1 AND NOT succeeded`, user).Scan(&n), "attempts")
	return n
}

func TestStepUpS1_RightPinMintsOneProofForTheHolderAndTheAct(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	p, err := s.svc.ProvePIN(bg(), signIn(t, m.user), m.pin, auth.ActLedgerBatch)
	if err != nil {
		t.Fatalf("REFUSED — the right PIN on a live session: %v", err)
	}
	var user, method string
	var kind *string
	var at time.Time
	var consumed bool
	fixture(t, db.QueryRow(bg(), `SELECT user_id::text, method, act_kind, at, consumed_at IS NOT NULL FROM auth_proofs WHERE id = $1`,
		p.ID).Scan(&user, &method, &kind, &at, &consumed), "the minted proof")
	switch {
	case user != m.user || method != "pin":
		t.Fatalf("WRONG PROOF — minted for %s by %s; want the session holder's PIN", user, method)
	case kind == nil || *kind != string(auth.ActLedgerBatch) || p.Act != auth.ActLedgerBatch:
		t.Fatalf("UNSCOPED — the proof names act %s (reported %q); want ledger_batch, bound at mint", str(kind), p.Act)
	case consumed:
		t.Fatalf("SPENT — the proof was consumed at mint")
	case p.ExpiresAt.Sub(at) < 4*time.Minute || p.ExpiresAt.Sub(at) > 5*time.Minute+time.Second:
		t.Fatalf("WRONG EXPIRY — reported to expire %s after mint; a proof is good for five minutes (0003)", p.ExpiresAt.Sub(at))
	case s.proofsOf(m.user) != 1:
		t.Fatalf("MINTED %d proofs for one step-up", s.proofsOf(m.user))
	}
}

func TestStepUpS2_WrongPinMintsNothingAndCountsAsAFailure(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	before := s.failuresOf(m.user)
	if _, err := s.svc.ProvePIN(bg(), signIn(t, m.user), "0000", auth.ActLedgerBatch); !errors.Is(err, auth.ErrRefused) {
		t.Fatalf("WRONG ANSWER — a wrong PIN at step-up answered %v, want ErrRefused", err)
	}
	if n := s.proofsOf(m.user); n != 0 {
		t.Fatalf("MINTED — a wrong PIN produced %d proof(s)", n)
	}
	if s.failuresOf(m.user) != before+1 {
		t.Fatalf("UNCOUNTED — a wrong PIN at step-up was not recorded against the account")
	}
}

// Same counter, direction 1: failures inside a session lock sign-in.
func TestStepUpS3_StepUpFailuresLockSignIn(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	tok := signIn(t, m.user)
	for i := 0; i < 5; i++ {
		s.svc.ProvePIN(bg(), tok, "0000", auth.ActLedgerBatch)
	}
	if _, err := s.signIn(m.number, m.pin, m.deviceToken); !errors.Is(err, auth.ErrTryLater) {
		t.Fatalf("ORACLE — after five wrong PINs at step-up, sign-in with the right PIN answered %v; want ErrTryLater (one counter)", err)
	}
}

// Same counter, direction 2: failures at sign-in lock the step-up.
func TestStepUpS4_SignInFailuresLockStepUp(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	tok := signIn(t, m.user)
	for i := 0; i < 5; i++ {
		s.signIn(m.number, "0000", m.deviceToken)
	}
	if _, err := s.svc.ProvePIN(bg(), tok, m.pin, auth.ActLedgerBatch); !errors.Is(err, auth.ErrTryLater) {
		t.Fatalf("ORACLE — a locked account stepped up with the right PIN: %v; want ErrTryLater", err)
	}
	if n := s.proofsOf(m.user); n != 0 {
		t.Fatalf("MINTED — a locked account got %d proof(s)", n)
	}
}

// One count, not two half-counts: 3 at sign-in + 2 at step-up is five.
func TestStepUpS5_MixedFailuresShareOneCount(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	tok := signIn(t, m.user)
	for i := 0; i < 3; i++ {
		s.signIn(m.number, "0000", m.deviceToken)
	}
	for i := 0; i < 2; i++ {
		s.svc.ProvePIN(bg(), tok, "0000", auth.ActLedgerBatch)
	}
	if _, err := s.svc.ProvePIN(bg(), tok, m.pin, auth.ActLedgerBatch); !errors.Is(err, auth.ErrTryLater) {
		t.Fatalf("SPLIT COUNT — three wrong at sign-in and two at step-up did not lock: %v", err)
	}
}

func TestStepUpS6_NoLiveSessionNoProof(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	tok := signIn(t, m.user)
	fixture(t, one(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE token_digest = $1`, auth.TokenDigest(tok)), "end session")
	for what, token := range map[string]string{"an ended session": tok, "an invented token": "not-a-session"} {
		if _, err := s.svc.ProvePIN(bg(), token, m.pin, auth.ActLedgerBatch); !errors.Is(err, auth.ErrNoSession) {
			t.Fatalf("ACCEPTED — %s stepped up: %v; want ErrNoSession", what, err)
		}
	}
	if n := s.proofsOf(m.user); n != 0 {
		t.Fatalf("MINTED — %d proof(s) without a live session", n)
	}
}

func TestStepUpS7_UnknownActNoProof(t *testing.T) {
	s := newSvc(t)
	m := s.member("Secretary")
	if _, err := s.svc.ProvePIN(bg(), signIn(t, m.user), m.pin, auth.ActKind("anything")); !errors.Is(err, auth.ErrUnknownAct) {
		t.Fatalf("ACCEPTED — a proof was asked for an act that does not exist: %v; want ErrUnknownAct", err)
	}
	if n := s.proofsOf(m.user); n != 0 {
		t.Fatalf("MINTED — %d proof(s) for no real act", n)
	}
}
