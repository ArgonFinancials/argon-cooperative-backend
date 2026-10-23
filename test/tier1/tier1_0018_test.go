//go:build tier1

// 0018: EVERY SESSION CARRIES ITS OWN CSRF TOKEN (decided 2026-10-23,
// LEDGER_CONTRACT.md §12). A synchronizer token, bound to the session: made at
// sign-in, its digest stored on the session row, required on every
// state-changing request of a cookie session. Every new session gets one,
// browser or not: one rule, not a branch (a Bearer session simply never uses
// it). Sessions from before 0018 have none, and so can make no cookie POST.
package tier1

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

var (
	csrfOnce sync.Once
	csrfCol  bool
)

// schemaHasCsrf: does a session carry a CSRF digest (0018)?
func schemaHasCsrf() bool {
	csrfOnce.Do(func() {
		db.QueryRow(bg(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'auth_sessions' AND column_name = 'csrf_digest')`).Scan(&csrfCol)
	})
	return csrfCol
}

// sessionInsert writes a session of the given columns and values, plus a
// fresh CSRF digest where the schema has one (0018 refuses a session without
// it), as the auth service does. returning may be empty.
func sessionInsert(cols, returning string, args ...any) (string, []any) {
	if schemaHasCsrf() {
		cols += ", csrf_digest"
		args = append(args, digest())
	}
	ph := make([]string, len(args))
	for i := range args {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	q := "INSERT INTO auth_sessions (" + cols + ") VALUES (" + strings.Join(ph, ", ") + ")"
	if returning != "" {
		q += " RETURNING " + returning
	}
	return q, args
}

func TestCsrf01_ASessionWithoutACsrfDigestIsRefused(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	d := a.device(p.user)
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, p.user, d.id, digest()),
		"a session with no CSRF token: a cookie session that could make no state change, or one checked against nothing",
		w("23514", "session_csrf_required"))
}

// If the digest could change, whatever could write it could make its own
// token valid for someone else's session.
func TestCsrf02_ASessionsCsrfDigestNeverChanges(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	s := a.session(p.user, a.device(p.user))
	refused(t, a.exec(`UPDATE auth_sessions SET csrf_digest = $2 WHERE id = $1`, s.id, digest()),
		"a live session's CSRF digest replaced", w("23514", "session_csrf_fixed"))
}

func TestCsrf03_ACsrfTokenIsStoredAsADigest(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	d := a.device(p.user)
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest, csrf_digest) VALUES ($1, $2, $3, 'csrf-token-in-the-clear')`,
		p.user, d.id, digest()),
		"a CSRF token stored in the clear", w("23514", "csrf_is_digest"))
}

// The CSRF token is NOT the session token. A server that reused the session
// token as the CSRF token would hand the session token back to the page in
// the sign-in body: the exposure the cookie exists to remove.
func TestCsrf04_TheCsrfTokenIsNotTheSessionToken(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	d := a.device(p.user)
	same := digest()
	refused(t, a.exec(`INSERT INTO auth_sessions (user_id, device_id, token_digest, csrf_digest) VALUES ($1, $2, $3, $3)`, p.user, d.id, same),
		"a session whose CSRF token is its own session token", w("23514", "csrf_is_not_the_session"))
}

// Control: a session with its own CSRF digest starts, is valid, and ends by
// its own session (the one change a session may undergo besides privilege).
func TestCsrf_Control_ASessionWithItsDigestStartsAndEnds(t *testing.T) {
	a := newAuth(t)
	p := a.who("M")
	d := a.device(p.user)
	var id string
	tok := digest()
	accepted(t, a.c.QueryRow(bg(), `INSERT INTO auth_sessions (user_id, device_id, token_digest, csrf_digest) VALUES ($1, $2, $3, $4) RETURNING id`,
		p.user, d.id, tok, digest()).Scan(&id), "a session with its own CSRF digest")
	if !a.valid(sess{id: id, digest: tok}) {
		t.Fatalf("REFUSED — a new session with its CSRF digest is not valid")
	}
	accepted(t, a.exec(`UPDATE auth_sessions SET ended_at = auth_now(), ended_by_session = id WHERE id = $1`, id),
		"the session ended by itself (sign-out)")
}
