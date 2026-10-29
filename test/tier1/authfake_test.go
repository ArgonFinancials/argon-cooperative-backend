//go:build tier1

// THE MUTATION VEHICLE FOR THE SERVICE TESTS — test code, NOT the service.
//
// A deliberately small sign-in service that can be broken in exactly one
// named way per run (ARGON_TEST_AUTH_FAKE=<break>). mutate.sh runs every
// TestAuthS* against each break and requires the targeted tests to go red.
// With no break it is a REFERENCE that shows the tests are satisfiable; it is
// not the implementation, which lives in internal/auth and must pass the same
// tests on its own.
//
// It computes lockout and the code destination itself, so the service tests
// do not depend on 0003's database rules (tested separately in TestAuth[0-H]*).
package tier1

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/argon/backend/internal/auth"
)

// Breaks, each a plausible way to get the service wrong:
//
//	distinct_unknown     unknown number gets its own message
//	timing_fast_unknown  unknown number skips the PIN hash — returns sooner
//	lock_real_only       only registered accounts ever lock
//	per_credential_form  attempts counted per credential string, not account
//	code_in_challenge    the challenge carries the code
//	no_notify            other devices are not told
//	trust_client_token   any device token the client sends is "known"
//	sms_first_record     code sent to the first record's phone, ignoring disagreement
//	always_code          (over-strict) a code even on a known device
//	never_send           (over-strict) new device, but no code is sent
//	first_match          (D-21) a shared credential signs in whichever record is found first
//	ambiguous_before_pin (D-21) routes a shared credential to an officer without checking the PIN
//	one_account_per_record (over-strict) one person's two records counted as two people
//	signin_ignores_lock  a locked account still signs in
type fakeService struct {
	d   auth.Deps
	brk string
}

func newFakeService(d auth.Deps, brk string) auth.Service { return &fakeService{d: d, brk: brk} }

var dummyPHC = hashPIN("not-a-real-pin")

func sha(s string) string { return auth.TokenDigest(s) }

func (f *fakeService) codeDigest(code string) string {
	m := hmac.New(sha256.New, f.d.CodeKey)
	m.Write([]byte(code))
	return hex.EncodeToString(m.Sum(nil))
}

func token() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (f *fakeService) SignIn(ctx context.Context, req auth.SignInRequest) (auth.Result, error) {
	// D-21: the credential must resolve to exactly ONE person.
	type cand struct{ user, phc string }
	var cands []cand
	q := `SELECT DISTINCT m.user_id::text, c.pin_hash FROM members m JOIN auth_credentials c ON c.user_id = m.user_id
		WHERE m.member_number = $1 OR m.phone = $1`
	switch f.brk {
	case "first_match":
		q += ` LIMIT 1`
	case "one_account_per_record": // counts member RECORDS, not people
		q = strings.Replace(q, "SELECT DISTINCT m.user_id::text", "SELECT m.user_id::text", 1)
	}
	rows, _ := f.d.DB.Query(ctx, q, req.Credential)
	for rows.Next() {
		var c cand
		rows.Scan(&c.user, &c.phc)
		cands = append(cands, c)
	}
	rows.Close()
	if len(cands) > 1 {
		// Ambiguous. A wrong PIN answers as any wrong PIN; a right one for any
		// candidate routes to an officer. Every candidate is hashed.
		if f.brk == "ambiguous_before_pin" {
			return auth.Result{SeeOfficer: true}, nil // tells anyone the number is shared
		}
		any := false
		for _, c := range cands {
			any = verifyPHC(c.phc, req.PIN) || any
		}
		f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (credential_digest, succeeded) VALUES ($1, false)`, sha("cred:"+req.Credential))
		if !any {
			return auth.Result{}, auth.ErrRefused
		}
		return auth.Result{SeeOfficer: true}, nil
	}
	var user, phc string
	found := len(cands) == 1
	if found {
		user, phc = cands[0].user, cands[0].phc
	}

	// The key lockout is counted on: the ACCOUNT when there is one.
	key := sha("cred:" + req.Credential)
	if found && f.brk != "per_credential_form" {
		key = sha("user:" + user)
	}
	if (found || f.brk != "lock_real_only") && f.brk != "signin_ignores_lock" && f.recentFailures(ctx, key) >= 5 {
		return auth.Result{}, auth.ErrTryLater
	}

	ok := false
	if found {
		ok = verifyPHC(phc, req.PIN)
	} else if f.brk != "timing_fast_unknown" {
		verifyPHC(dummyPHC, req.PIN) // spend the same time as a real check
	}
	var uid any
	if found {
		uid = user
	}
	f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, $3)`, uid, key, ok)
	if !ok {
		if !found && f.brk == "distinct_unknown" {
			return auth.Result{}, errors.New("auth: no account with that number")
		}
		return auth.Result{}, auth.ErrRefused
	}

	known := false
	var device any // the session runs on the device that signed it in, as the real service records (2026-10-23)
	if req.DeviceToken != "" && f.brk != "always_code" {
		if f.brk == "trust_client_token" {
			known = true
		} else {
			var id string
			if f.d.DB.QueryRow(ctx, `SELECT id::text FROM auth_devices WHERE user_id = $1 AND token_digest = $2 AND revoked_at IS NULL`,
				user, sha(req.DeviceToken)).Scan(&id) == nil {
				known, device = true, id
			}
		}
	}
	if known {
		t := token()
		if _, err := f.d.DB.Exec(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, user, device, sha(t)); err != nil {
			return auth.Result{}, err
		}
		return auth.Result{SessionToken: t}, nil
	}

	// New device: a code to the ONE registered number, or an officer.
	var phones []string
	rows, _ = f.d.DB.Query(ctx, `SELECT DISTINCT phone FROM members WHERE user_id = $1 ORDER BY phone`, user)
	for rows.Next() {
		var p string
		rows.Scan(&p)
		phones = append(phones, p)
	}
	rows.Close()
	if len(phones) == 0 || (len(phones) > 1 && f.brk != "sms_first_record") {
		return auth.Result{SeeOfficer: true}, nil
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	code := fmt.Sprintf("%06d", n.Int64())
	var id string
	if err := f.d.DB.QueryRow(ctx, `INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to) VALUES ($1, 'new_device', $2, $3) RETURNING id`,
		user, f.codeDigest(code), phones[0]).Scan(&id); err != nil {
		return auth.Result{}, err
	}
	var sent auth.Sent
	if f.brk != "never_send" {
		// A failed send is an error, never a code_sent that delivers nothing.
		var err error
		sent, err = f.d.SMS.Send(ctx, phones[0], "Your Argon code is "+code+". It expires in 5 minutes. Nobody from Argon will ask you for it.")
		if err != nil {
			return auth.Result{}, err
		}
	}
	// A real provider's every send is recorded (0019), as the service does.
	if sent.Provider != "" && f.brk != "send_unrecorded" {
		if _, err := f.d.DB.Exec(ctx, `INSERT INTO sms_sends (code_id, provider, outcome, provider_message_id, provider_code)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))`, id, sent.Provider, string(sent.Outcome), sent.MessageID, sent.ProviderCode); err != nil {
			return auth.Result{}, err
		}
	}
	challenge := id
	if f.brk == "code_in_challenge" {
		challenge = id + ":" + code
	}
	switch sent.Outcome {
	case auth.SendRefused, auth.SendNotReached:
		if f.brk == "send_failure_counted" { // our failure counted against the member's lockout
			f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, false)`, user, digest())
		}
		if f.brk == "refused_as_sent" { // a code that certainly did not go, reported as on its way
			return auth.Result{Challenge: challenge}, nil
		}
		return auth.Result{}, auth.ErrCodeNotSent
	case auth.SendUnknown:
		if f.brk == "unknown_as_not_sent" { // strands a code that may arrive
			return auth.Result{}, auth.ErrCodeNotSent
		}
		return auth.Result{Challenge: challenge, Delayed: f.brk != "unknown_unsaid"}, nil
	}
	return auth.Result{Challenge: challenge}, nil
}

func (f *fakeService) recentFailures(ctx context.Context, key string) int {
	var n int
	f.d.DB.QueryRow(ctx, `SELECT count(*) FROM sign_in_attempts
		WHERE credential_digest = $1 AND NOT succeeded
		  AND at > coalesce((SELECT max(at) FROM sign_in_attempts WHERE credential_digest = $1 AND succeeded), '-infinity')
		  AND at > now() - interval '15 minutes'`, key).Scan(&n)
	return n
}

func (f *fakeService) VerifyCode(ctx context.Context, challenge, code string) (auth.Result, error) {
	id := strings.SplitN(challenge, ":", 2)[0]
	var user string
	var ok bool
	if err := f.d.DB.QueryRow(ctx, `SELECT user_id, code_digest = $2 FROM auth_codes WHERE id = $1 AND consumed_at IS NULL`,
		id, f.codeDigest(code)).Scan(&user, &ok); err != nil || !ok {
		return auth.Result{}, auth.ErrRefused
	}
	f.d.DB.Exec(ctx, `UPDATE auth_codes SET consumed_at = now() WHERE id = $1`, id)
	dt, st := token(), token()
	var dev string
	f.d.DB.QueryRow(ctx, `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id`, user, sha(dt)).Scan(&dev)
	f.d.DB.Exec(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, user, dev, sha(st))
	if f.brk != "no_notify" {
		var at time.Time
		f.d.DB.QueryRow(ctx, `SELECT now()`).Scan(&at)
		f.d.Notify.NewDeviceSignedIn(ctx, user, at)
	}
	return auth.Result{SessionToken: st, DeviceToken: dt}, nil
}
