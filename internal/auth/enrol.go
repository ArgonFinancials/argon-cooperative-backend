package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Enrol redeems an enrolment code for a new trusted device (0024, 0025;
// LEDGER_CONTRACT.md §12, approved 2026-11-07).
//
//   - The code never stands in for a PIN reset (D-15). It sets a first PIN
//     only for someone who has never had one. Anyone with a PIN must give
//     it, and may not choose a new one here.
//   - The PIN is checked before the code is touched. A wrong PIN never costs
//     the slip, and counts against the lock that an enrolment meets: the
//     other-device lock (0022), since an enrolment is never from the
//     member's own phone. A slip is outside the own-phone lock by ruling
//     (§12, 2026-11-09).
//   - A wrong code costs one of the code's own tries (the database counts
//     them), never a PIN try, and answers exactly as an unknown number.
//   - Everything after a right code happens in one transaction. Any refusal
//     there (a refused or missing first PIN, a holder asking for a new one)
//     rolls back, and the code stays live.
//   - The person's own used slip says so, without costing a try (0025).
func (s *service) Enrol(ctx context.Context, req EnrolRequest) (Result, error) {
	cred := strings.TrimSpace(req.Credential)
	credDigest := TokenDigest("cred:" + cred)

	cands, err := s.candidates(ctx, cred)
	if err != nil {
		return Result{}, err
	}
	// WHETHER A REQUEST COUNTS DEPENDS ON THE REQUEST, NEVER ON THE ACCOUNT
	// (2026-11-09). Carrying a PIN, it meets a lock and is counted, whoever
	// the number names. Carrying none, it is never counted. Otherwise five
	// wrong PINs would tell an enrolled number from an unknown one.
	pinGiven := req.PIN != ""
	if len(cands) != 1 {
		return s.enrolNobody(ctx, cands, credDigest, req.PIN)
	}
	c := cands[0]
	hasPIN := c.pinHash != ""

	switch {
	case pinGiven:
		locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state_unknown_device($1)`, c.user)
		if err != nil {
			return Result{}, err
		}
		ok := s.verify(c.pinHash, req.PIN) // "" for someone with no PIN: the dummy, and never right
		if locked {
			return Result{}, ErrTryLater
		}
		user := c.user
		if err := s.attempt(ctx, &user, credDigest, nil, ok); err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, ErrRefused // the code is not touched
		}
	case hasPIN:
		s.verify(s.dummy, req.PIN)
		return Result{}, ErrRefused // a holder must give their PIN; not a guess, so not counted
	default:
		s.verify(s.dummy, req.PIN) // the same cost whether or not the person has a PIN
	}

	devTok, sessTok := randomToken(), randomToken()
	var refusal error
	var at time.Time
	errRollback := errors.New("rollback")
	err = pgx.BeginFunc(ctx, s.d.DB, func(tx pgx.Tx) error {
		var codeID, deviceID *string
		var used *bool
		if err := tx.QueryRow(ctx, `SELECT code_id::text, device_id::text, already_used FROM enrolment_redeem($1, $2, $3)`,
			c.user, s.enrolmentDigest(req.Code), TokenDigest(devTok)).Scan(&codeID, &deviceID, &used); err != nil {
			return err
		}
		if codeID == nil {
			// Commit: a wrong code's try counts. Nothing else was written.
			if used != nil && *used {
				refusal = ErrSlipUsed
			} else {
				refusal = ErrRefused
			}
			return nil
		}
		refuse := func(e error) error { refusal = e; return errRollback }
		switch {
		case hasPIN && req.NewPIN != "":
			return refuse(ErrPINAlreadySet)
		case !hasPIN && req.NewPIN == "":
			return refuse(ErrNeedsNewPIN)
		}
		if !hasPIN {
			phones, err := s.phonesOf(ctx, tx, c.user)
			if err != nil {
				return err
			}
			if err := CheckNewPIN(req.NewPIN, phones...); err != nil {
				return refuse(err)
			}
			h, err := s.HashPIN(req.NewPIN)
			if err != nil {
				return err
			}
			// INSERT only (argon_auth cannot UPDATE a PIN): a PIN set since
			// this request began is never replaced.
			if _, err := tx.Exec(ctx, `INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, c.user, h); err != nil {
				if isConstraint(err, "auth_credentials_pkey") {
					return refuse(ErrPINAlreadySet)
				}
				return err
			}
		}
		return tx.QueryRow(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3) RETURNING started_at`,
			c.user, *deviceID, TokenDigest(sessTok)).Scan(&at)
	})
	if err != nil && !errors.Is(err, errRollback) {
		return Result{}, err
	}
	if refusal != nil {
		return Result{}, refusal
	}
	if err := s.d.Notify.NewDeviceSignedIn(ctx, c.user, at); err != nil {
		return Result{}, err
	}
	return Result{SessionToken: sessTok, DeviceToken: devTok}, nil
}

// enrolNobody: the credential names no one, or more than one person (D-21).
// Answered exactly as a named person would be for the same request: carrying
// a PIN, it meets the credential's lock and is counted as a wrong PIN;
// carrying none, it is refused and never counted. The same hashing cost.
func (s *service) enrolNobody(ctx context.Context, cands []candidate, credDigest, pin string) (Result, error) {
	if pin == "" {
		s.verify(s.dummy, pin)
		return Result{}, ErrRefused
	}
	locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state_for_credential($1)`, credDigest)
	if err != nil {
		return Result{}, err
	}
	if len(cands) == 0 {
		s.verify(s.dummy, pin)
	}
	for _, c := range cands {
		s.verify(c.pinHash, pin)
	}
	if locked {
		return Result{}, ErrTryLater
	}
	if err := s.attempt(ctx, nil, credDigest, nil, false); err != nil {
		return Result{}, err
	}
	return Result{}, ErrRefused
}

// phonesOf: every phone on the person's member records, which a new PIN may
// not end with (CheckNewPIN).
func (s *service) phonesOf(ctx context.Context, tx pgx.Tx, user string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT phone FROM members WHERE user_id = $1 AND phone IS NOT NULL`, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
