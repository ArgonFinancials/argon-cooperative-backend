//go:build tier1

// Enrol in the service mutation vehicle — test code, NOT the service. The
// reference enrolment, breakable one named way per run
// (ARGON_TEST_AUTH_FAKE=<break>), for tier1_enrol_service_test.go.
//
// Breaks:
//
//	enrol_sets_pin_for_holder  a new PIN with a code replaces an existing PIN (a reset by slip, D-15)
//	enrol_skips_pin_rule       a first PIN is not put through CheckNewPIN
//	enrol_burns_code           the code is spent before the new PIN is checked: a refused PIN loses the slip
//	enrol_code_before_pin      the code is redeemed before the existing PIN is checked: a wrong PIN costs the slip
//	enrol_pin_tries_free       a wrong existing PIN is not counted against the lockout
//	enrol_code_tries_as_pin    a wrong code is counted against the PIN lockout
//	enrol_ignores_lock         a locked account enrols anyway
//	enrol_wrong_code_told      a wrong code answers differently from an unknown number
//	enrol_used_silent          the used slip answers as any wrong code: the member is not told what to do
//	enrol_device_unbound       the device token handed back is not the device the code trusted
//	enrol_no_notice            the person's other devices are not told a new device signed in
//	enrol_unknown_uncounted    an unknown number's wrong PINs are never counted: the lock tells it from an enrolled one
//	enrol_newcomer_pin_ignored a PIN typed by someone who has none is ignored: never counted, never locked
//	enrol_empty_pin_counted    a holder's request with no PIN counts as a wrong PIN
//	enrol_robbed_member_untold the member whose slip was taken, typing it with a new PIN, is refused like a typo, never told it was used
//	enrol_short_code           a code is 12 characters, 30 bits a half
//	enrol_order_matters        the halves redeem only in the order they were issued
//	enrol_typing_strict        case and spaces must match the slip exactly
package tier1

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/auth"
)

func (f *fakeService) Enrol(ctx context.Context, req auth.EnrolRequest) (auth.Result, error) {
	type cand struct{ user, phc string }
	var cands []cand
	rows, _ := f.d.DB.Query(ctx, `SELECT DISTINCT m.user_id::text, coalesce(c.pin_hash, '') FROM members m
		LEFT JOIN auth_credentials c ON c.user_id = m.user_id WHERE m.user_id IS NOT NULL AND (m.member_number = $1 OR m.phone = $1)`, req.Credential)
	for rows.Next() {
		var c cand
		rows.Scan(&c.user, &c.phc)
		cands = append(cands, c)
	}
	rows.Close()
	// WHETHER A REQUEST COUNTS DEPENDS ON THE REQUEST, NEVER ON THE ACCOUNT:
	// carrying a PIN, it meets a lock and is counted, whoever the number
	// names; carrying none, it is never counted. Otherwise the lock would
	// tell an enrolled number from an unknown one.
	pinGiven := req.PIN != ""
	if len(cands) != 1 {
		verifyPHC(dummyPHC(), req.PIN)
		if !pinGiven || f.brk == "enrol_unknown_uncounted" {
			return auth.Result{}, auth.ErrRefused
		}
		ck := sha("cred:" + req.Credential)
		if f.recentFailures(ctx, ck, false, false) >= 5 {
			return auth.Result{}, auth.ErrTryLater
		}
		f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (credential_digest, succeeded) VALUES ($1, false)`, ck)
		return auth.Result{}, auth.ErrRefused
	}
	user, phc := cands[0].user, cands[0].phc
	key := sha("user:" + user)
	if phc == "" && pinGiven && f.brk != "enrol_newcomer_pin_ignored" {
		// Someone with no PIN who types one: a wrong PIN, counted, as for anyone.
		verifyPHC(dummyPHC(), req.PIN)
		if f.recentFailures(ctx, key, false, false) >= 5 {
			return auth.Result{}, auth.ErrTryLater
		}
		f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, false)`, user, key)
		return auth.Result{}, auth.ErrRefused
	}
	if phc != "" && !pinGiven && f.brk != "enrol_empty_pin_counted" {
		// A holder who gives no PIN: refused, never counted, the code untouched.
		verifyPHC(dummyPHC(), req.PIN)
		// Unless they say they never had one, and the code is their own spent
		// slip: the member whose slip was taken is told (§12, 0030).
		if req.NewPIN != "" && f.brk != "enrol_robbed_member_untold" {
			var used bool
			f.d.DB.QueryRow(ctx, `SELECT enrolment_slip_used($1, $2)`, user, f.enrolDigest(req.Code)).Scan(&used)
			if used {
				return auth.Result{}, auth.ErrSlipUsed
			}
		}
		return auth.Result{}, auth.ErrRefused
	}
	codeDigest := f.enrolDigest(req.Code)
	devTok, sessTok := token(), token()

	redeem := func(x pgx.Tx) (string, string, bool, error) {
		var code, dev *string
		var used bool
		err := x.QueryRow(ctx, `SELECT code_id::text, device_id::text, coalesce(already_used, false) FROM enrolment_redeem($1, $2, $3)`,
			user, codeDigest, sha(devTok)).Scan(&code, &dev, &used)
		if err != nil || code == nil {
			return "", "", used, err
		}
		return *code, *dev, false, nil
	}
	wrongCode := func(used bool) (auth.Result, error) {
		if used && f.brk != "enrol_used_silent" {
			return auth.Result{}, auth.ErrSlipUsed
		}
		if f.brk == "enrol_code_tries_as_pin" {
			f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, false)`, user, key)
		}
		if f.brk == "enrol_wrong_code_told" {
			return auth.Result{}, errors.New("auth: that enrolment code is not right")
		}
		return auth.Result{}, auth.ErrRefused
	}

	hasPIN := phc != ""
	spentFirst := false
	if hasPIN && f.brk == "enrol_code_before_pin" {
		// The break: the slip is spent before the PIN is known to be right.
		var code string
		var used bool
		pgx.BeginFunc(ctx, f.d.DB, func(x pgx.Tx) (err error) { code, _, used, err = redeem(x); return })
		if code == "" {
			return wrongCode(used)
		}
		spentFirst = true
	}
	if hasPIN {
		if f.brk != "enrol_ignores_lock" && f.recentFailures(ctx, key, false, false) >= 5 {
			return auth.Result{}, auth.ErrTryLater
		}
		ok := verifyPHC(phc, req.PIN)
		if f.brk != "enrol_pin_tries_free" || ok {
			f.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($1, $2, $3)`, user, key, ok)
		}
		if !ok {
			return auth.Result{}, auth.ErrRefused
		}
	} else {
		verifyPHC(dummyPHC(), req.PIN) // the same cost whether or not the person has a PIN
	}

	// One transaction: a wrong code's try is committed; everything else that
	// refuses after a right code rolls back, and the code stays live.
	var res auth.Result
	var refusal error
	errRollback := errors.New("rollback")
	tryOnce := func(burn bool) error {
		return pgx.BeginFunc(ctx, f.d.DB, func(x pgx.Tx) error {
			code, dev, used, err := redeem(x)
			if err != nil {
				return err
			}
			if code == "" {
				res, refusal = wrongCode(used)
				return nil // commit: the wrong try counts
			}
			fail := func(e error) error {
				refusal = e
				if burn {
					return nil // the break: the slip is spent anyway
				}
				return errRollback
			}
			switch {
			case hasPIN && req.NewPIN != "" && f.brk != "enrol_sets_pin_for_holder":
				return fail(auth.ErrPINAlreadySet)
			case !hasPIN && req.NewPIN == "":
				return fail(auth.ErrNeedsNewPIN)
			}
			if req.NewPIN != "" {
				if f.brk != "enrol_skips_pin_rule" {
					var phone string
					x.QueryRow(ctx, `SELECT phone FROM members WHERE user_id = $1 LIMIT 1`, user).Scan(&phone)
					if err := auth.CheckNewPIN(req.NewPIN, phone); err != nil {
						return fail(err)
					}
				}
				if _, err := x.Exec(ctx, `INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)
					ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash`, user, hashPIN(req.NewPIN)); err != nil {
					return err
				}
			}
			if f.brk == "enrol_device_unbound" {
				var other string
				x.QueryRow(ctx, `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id::text`, user, sha(token())).Scan(&other)
				_ = dev
			}
			if _, err := x.Exec(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`, user, dev, sha(sessTok)); err != nil {
				return err
			}
			res = auth.Result{SessionToken: sessTok, DeviceToken: devTok}
			if f.brk == "enrol_device_unbound" {
				res.DeviceToken = token()
			}
			return nil
		})
	}
	burn := f.brk == "enrol_burns_code"
	if spentFirst {
		return auth.Result{SessionToken: sessTok, DeviceToken: devTok}, nil // the PIN was right; the break is already visible
	}
	if err := tryOnce(burn); err != nil && !errors.Is(err, errRollback) {
		return auth.Result{}, err
	}
	if refusal != nil {
		return auth.Result{}, refusal
	}
	if f.brk != "enrol_no_notice" {
		f.d.Notify.NewDeviceSignedIn(ctx, user, time.Now())
	}
	return res, nil
}

// ---- the code itself: the reference, breakable ----------------------------------

const fakeCrockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func (f *fakeService) halfLen() int {
	if f.brk == "enrol_short_code" {
		return 6
	}
	return 8
}

func (f *fakeService) drawHalf() string {
	b := make([]byte, f.halfLen())
	rand.Read(b)
	for i := range b {
		b[i] = fakeCrockford[b[i]&31]
	}
	return string(b)
}

func (f *fakeService) halfDigest(h string) string { return f.codeDigest("enrolment-half:" + h) }

func (f *fakeService) assemble(a, b string) string {
	if b < a && f.brk != "enrol_order_matters" {
		a, b = b, a
	}
	return sha(a + b)
}

func (f *fakeService) NewEnrolmentCode() (string, string) {
	a, b := f.drawHalf(), f.drawHalf()
	g := func(h string) string { return h[:4] + "-" + h[4:] }
	return g(a) + "-" + g(b), f.assemble(f.halfDigest(a), f.halfDigest(b))
}

func (f *fakeService) NewEnrolmentHalf() (string, string) {
	h := f.drawHalf()
	return h[:4] + "-" + h[4:], f.halfDigest(h)
}

func (f *fakeService) enrolDigest(typed string) string {
	n := typed
	if f.brk != "enrol_typing_strict" {
		n = strings.NewReplacer(" ", "", "-", "", "I", "1", "L", "1", "O", "0").Replace(strings.ToUpper(typed))
	} else {
		n = strings.ReplaceAll(typed, "-", "")
	}
	if len(n) != 2*f.halfLen() {
		return sha("not a code: " + typed)
	}
	return f.assemble(f.halfDigest(n[:f.halfLen()]), f.halfDigest(n[f.halfLen():]))
}
