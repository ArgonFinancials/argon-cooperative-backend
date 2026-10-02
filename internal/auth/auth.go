// Package auth is sign-in: PIN, new-device codes, sessions.
//
// This file is the interface; service.go implements it. The acceptance tests
// in test/tier1 (TestAuthS*) were written against this interface and reviewed
// before the implementation existed.
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
	// ErrNoSession: the step-up was asked for without a live session.
	ErrNoSession = errors.New("auth: no live session")
	// ErrUnknownAct: a proof was asked for an act that does not exist.
	ErrUnknownAct = errors.New("auth: no such act")
)

type Service interface {
	SignIn(ctx context.Context, req SignInRequest) (Result, error)
	VerifyCode(ctx context.Context, challenge, code string) (Result, error)
	// ProvePIN is the STEP-UP (0005): the person signed in on sessionToken
	// re-enters their PIN, and gets a proof that authorises ONE act of kind
	// act, within five minutes. A wrong PIN counts towards the same per-account
	// lockout as sign-in (same counter, same escalation): otherwise a stolen
	// session is an unlimited PIN oracle. Errors: ErrNoSession, ErrUnknownAct,
	// ErrRefused (wrong PIN), ErrTryLater (locked).
	ProvePIN(ctx context.Context, sessionToken, pin string, act ActKind) (Proof, error)
}

// ActKind names the attributable act a PIN proof is minted FOR. The kind is
// bound at mint, never at use: a proof minted for an import does not satisfy
// a reversal (0005).
type ActKind string

const (
	ActLedgerBatch           ActKind = "ledger_batch"
	ActPinChange             ActKind = "pin_change"
	ActMemberReset           ActKind = "member_reset"
	ActPhoneChange           ActKind = "phone_change"
	ActLockRelease           ActKind = "lock_release"
	ActSignatoryResetRequest ActKind = "signatory_reset_request"
	ActSignatoryPhoneRequest ActKind = "signatory_phone_request"
	ActSignatoryApproval     ActKind = "signatory_approval"
)

// ActKinds is every kind a proof can be minted for: credential_acts.kind.
var ActKinds = []ActKind{ActLedgerBatch, ActPinChange, ActMemberReset, ActPhoneChange, ActLockRelease,
	ActSignatoryResetRequest, ActSignatoryPhoneRequest, ActSignatoryApproval}

// Proof is a minted step-up. Its id is what the act names; it is spent by the
// act, inside the act's own transaction, and by nothing else.
type Proof struct {
	ID        string
	Act       ActKind
	ExpiresAt time.Time
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

// TokenDigest is how a session or device token is stored: hex SHA-256. The
// token itself is never stored.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
