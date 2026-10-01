// Package auth is sign-in: PIN, new-device codes, sessions.
//
// THIS FILE IS THE INTERFACE ONLY. The acceptance tests in test/tier1
// (TestAuthS*) were written against it before any implementation, for review;
// New returns ErrNotImplemented until then.
//
// The split with the database (migration 0003): the database holds every rule
// about STATE — hash formats, proofs, attributable acts, lockout windows, code
// lifetime, sessions, the two-signatory reset. This service does what the
// database cannot: argon2id (not available in Postgres), code generation, the
// uniform refusal, and talking to the phone network.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SignInRequest is the one credential box plus the PIN. Credential is a
// member number or a phone number: "the credentials resolve the role, always".
type SignInRequest struct {
	Credential string
	PIN        string
	// DeviceToken is the token this server issued to the device on an earlier
	// sign-in, or "" on a new device. A value the server did not issue is
	// treated as a new device.
	DeviceToken string
}

// Result is what a sign-in step returns. At most one of SessionToken,
// Challenge or SeeOfficer is meaningful.
type Result struct {
	SessionToken string // signed in
	DeviceToken  string // issued when a new device becomes trusted
	// Challenge identifies a code that was sent to the registered phone. It is
	// an opaque id. It NEVER contains or implies the code: "the code goes to
	// the phone, not to the screen".
	Challenge string
	// SeeOfficer: the PIN was right on a new device, but there is no single
	// registered number to send a code to. That case cannot be solved on a
	// screen; it goes to officers who know the member by sight.
	SeeOfficer bool
}

var (
	// ErrRefused is the ONE answer to a wrong credential or a wrong PIN. It
	// never says which half was wrong: "no account with that number" confirms
	// which numbers are registered.
	ErrRefused = errors.New("auth: those details do not match an account")
	// ErrTryLater follows too many attempts, for a registered and an
	// unregistered credential alike.
	ErrTryLater = errors.New("auth: too many attempts; try again later")

	ErrNotImplemented = errors.New("auth: not implemented — tests first")
)

type Service interface {
	SignIn(ctx context.Context, req SignInRequest) (Result, error)
	VerifyCode(ctx context.Context, challenge, code string) (Result, error)
}

// SMS delivers a text to a phone number. The real one is a procurement item
// (see deploy/AUTH_SMS.md); tests use a fake.
type SMS interface {
	Send(ctx context.Context, to, text string) error
}

// Notifier tells a person's other devices that a new device signed in, with
// the date and time.
type Notifier interface {
	NewDeviceSignedIn(ctx context.Context, userID string, at time.Time) error
}

// Argon2 parameters for PIN hashes (argon2id, PHC string format).
type Argon2 struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

type Deps struct {
	DB      *pgxpool.Pool
	SMS     SMS
	Notify  Notifier
	CodeKey []byte // HMAC key for code digests; the code itself is never stored
	Argon   Argon2
}

func New(d Deps) (Service, error) { return nil, ErrNotImplemented }

// TokenDigest is how a session or device token is stored: hex SHA-256. The
// token itself is never stored.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
