package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/argon2"
)

type service struct {
	d     Deps
	dummy string // a real hash, verified against when there is no account, so refusals cost the same
}

// New returns the sign-in service. The rules about state live in the
// database (migration 0003); this does argon2id, codes and the uniform
// refusal.
func New(d Deps) (Service, error) {
	switch {
	case d.DB == nil:
		return nil, errors.New("auth: Deps.DB is required")
	case d.SMS == nil:
		return nil, errors.New("auth: Deps.SMS is required")
	case d.Notify == nil:
		return nil, errors.New("auth: Deps.Notify is required")
	case len(d.CodeKey) < 16:
		return nil, errors.New("auth: Deps.CodeKey must be at least 16 bytes")
	case d.Argon.MemoryKiB == 0 || d.Argon.Time == 0 || d.Argon.Threads == 0:
		return nil, errors.New("auth: Deps.Argon parameters are required")
	}
	s := &service{d: d}
	dummy, err := s.HashPIN(randomToken())
	if err != nil {
		return nil, err
	}
	s.dummy = dummy
	return s, nil
}

// HashPIN returns an argon2id PHC string with this service's parameters.
func (s *service) HashPIN(pin string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	a := s.d.Argon
	h := argon2.IDKey([]byte(pin), salt, a.Time, a.MemoryKiB, a.Threads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", a.MemoryKiB, a.Time, a.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}

// verify checks a PIN against a PHC string in constant time. A malformed or
// empty hash is checked against the dummy instead, so it costs the same.
func (s *service) verify(phc, pin string) bool {
	m, t, p, salt, want, ok := parsePHC(phc)
	if !ok {
		s.verify(s.dummy, pin)
		return false
	}
	got := argon2.IDKey([]byte(pin), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func parsePHC(phc string) (m, t uint32, p uint8, salt, hash []byte, ok bool) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return
	}
	var err1, err2 error
	salt, err1 = base64.RawStdEncoding.DecodeString(parts[4])
	hash, err2 = base64.RawStdEncoding.DecodeString(parts[5])
	ok = err1 == nil && err2 == nil && len(hash) > 0
	return
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS random source failing is not a recoverable state
	}
	return hex.EncodeToString(b)
}

func (s *service) codeDigest(code string) string {
	mac := hmac.New(sha256.New, s.d.CodeKey)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

type candidate struct{ user, pinHash string }

func (s *service) SignIn(ctx context.Context, req SignInRequest) (Result, error) {
	cred := strings.TrimSpace(req.Credential)
	credDigest := TokenDigest("cred:" + cred)

	// D-21: the credential resolves to exactly one PERSON, or nobody is signed in.
	rows, err := s.d.DB.Query(ctx, `SELECT DISTINCT m.user_id::text, coalesce(c.pin_hash, '')
		FROM members m LEFT JOIN auth_credentials c ON c.user_id = m.user_id
		WHERE m.user_id IS NOT NULL AND (m.member_number = $1 OR m.phone = $1)`, cred)
	if err != nil {
		return Result{}, err
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidate, error) {
		var c candidate
		return c, r.Scan(&c.user, &c.pinHash)
	})
	if err != nil {
		return Result{}, err
	}

	switch len(cands) {
	case 0:
		return s.refuseUnknown(ctx, credDigest, req.PIN)
	case 1:
		return s.signInOne(ctx, cands[0], credDigest, req)
	default:
		return s.ambiguous(ctx, cands, credDigest, req.PIN)
	}
}

// An unregistered credential is answered exactly as a registered one with a
// wrong PIN: the same error, the same lockout, the same hashing cost.
func (s *service) refuseUnknown(ctx context.Context, credDigest, pin string) (Result, error) {
	locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state_for_credential($1)`, credDigest)
	if err != nil {
		return Result{}, err
	}
	s.verify(s.dummy, pin)
	if locked {
		return Result{}, ErrTryLater
	}
	if err := s.attempt(ctx, nil, credDigest, nil, false); err != nil {
		return Result{}, err
	}
	return Result{}, ErrRefused
}

// Ambiguous (D-21): a wrong PIN answers as any wrong PIN; a right one for any
// candidate routes to an officer. Every candidate is checked, so the cost does
// not depend on which one matched.
func (s *service) ambiguous(ctx context.Context, cands []candidate, credDigest, pin string) (Result, error) {
	locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state_for_credential($1)`, credDigest)
	if err != nil {
		return Result{}, err
	}
	any := false
	for _, c := range cands {
		any = s.verify(c.pinHash, pin) || any
	}
	if locked {
		return Result{}, ErrTryLater
	}
	if err := s.attempt(ctx, nil, credDigest, nil, any); err != nil {
		return Result{}, err
	}
	if !any {
		return Result{}, ErrRefused
	}
	return Result{SeeOfficer: true}, nil
}

func (s *service) signInOne(ctx context.Context, c candidate, credDigest string, req SignInRequest) (Result, error) {
	locked, err := s.locked(ctx, `SELECT locked_until, needs_officer FROM lock_state($1)`, c.user)
	if err != nil {
		return Result{}, err
	}
	ok := s.verify(c.pinHash, req.PIN)
	if locked {
		return Result{}, ErrTryLater
	}

	// A device is known only by a token this server issued to THIS person.
	var deviceID *string
	if req.DeviceToken != "" {
		var id string
		err := s.d.DB.QueryRow(ctx, `SELECT id::text FROM auth_devices
			WHERE user_id = $1 AND token_digest = $2 AND revoked_at IS NULL`, c.user, TokenDigest(req.DeviceToken)).Scan(&id)
		if err == nil {
			deviceID = &id
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, err
		}
	}

	user := c.user
	if err := s.attempt(ctx, &user, credDigest, deviceID, ok); err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrRefused
	}

	if deviceID != nil {
		tok := randomToken()
		if _, err := s.d.DB.Exec(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3)`,
			c.user, *deviceID, TokenDigest(tok)); err != nil {
			return Result{}, err
		}
		return Result{SessionToken: tok}, nil
	}

	// A new device: a code to the ONE registered number, or an officer.
	var dest *string
	if err := s.d.DB.QueryRow(ctx, `SELECT otp_destination($1)`, c.user).Scan(&dest); err != nil {
		return Result{}, err
	}
	if dest == nil {
		return Result{SeeOfficer: true}, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return Result{}, err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	var challenge string
	if err := s.d.DB.QueryRow(ctx, `INSERT INTO auth_codes (user_id, purpose, code_digest, sent_to)
		VALUES ($1, 'new_device', $2, $3) RETURNING id::text`, c.user, s.codeDigest(code), *dest).Scan(&challenge); err != nil {
		if isConstraint(err, "code_resend_interval") {
			return Result{}, ErrTryLater
		}
		return Result{}, err
	}
	if err := s.d.SMS.Send(ctx, *dest, "Your Argon code is "+code+". It expires in 5 minutes. Nobody from Argon will ask you for it."); err != nil {
		return Result{}, err
	}
	return Result{Challenge: challenge}, nil
}

func (s *service) locked(ctx context.Context, q, arg string) (bool, error) {
	var until *time.Time
	var officer bool
	if err := s.d.DB.QueryRow(ctx, q, arg).Scan(&until, &officer); err != nil {
		return false, err
	}
	return until != nil || officer, nil
}

func (s *service) attempt(ctx context.Context, user *string, credDigest string, device *string, ok bool) error {
	_, err := s.d.DB.Exec(ctx, `INSERT INTO sign_in_attempts (user_id, credential_digest, device_id, succeeded) VALUES ($1, $2, $3, $4)`,
		user, credDigest, device, ok)
	return err
}

func (s *service) VerifyCode(ctx context.Context, challenge, code string) (Result, error) {
	var ok bool
	if err := s.d.DB.QueryRow(ctx, `SELECT code_verify($1::uuid, $2)`, challenge, s.codeDigest(code)).Scan(&ok); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" { // not a uuid: not a challenge we issued
			return Result{}, ErrRefused
		}
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrRefused
	}
	var user string
	if err := s.d.DB.QueryRow(ctx, `SELECT user_id::text FROM auth_codes WHERE id = $1::uuid`, challenge).Scan(&user); err != nil {
		return Result{}, err
	}
	devTok, sessTok := randomToken(), randomToken()
	var at time.Time
	err := pgx.BeginFunc(ctx, s.d.DB, func(tx pgx.Tx) error {
		var dev string
		if err := tx.QueryRow(ctx, `INSERT INTO auth_devices (user_id, token_digest) VALUES ($1, $2) RETURNING id::text`,
			user, TokenDigest(devTok)).Scan(&dev); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO auth_sessions (user_id, device_id, token_digest) VALUES ($1, $2, $3) RETURNING started_at`,
			user, dev, TokenDigest(sessTok)).Scan(&at)
	})
	if err != nil {
		return Result{}, err
	}
	if err := s.d.Notify.NewDeviceSignedIn(ctx, user, at); err != nil {
		return Result{}, err
	}
	return Result{SessionToken: sessTok, DeviceToken: devTok}, nil
}

func isConstraint(err error, name string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == name
}
