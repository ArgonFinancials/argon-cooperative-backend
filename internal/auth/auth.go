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
	"log/slog"
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
	// Delayed: the code was handed to the SMS provider and no readable answer
	// came back (the Termii sender's "unknown"). It may arrive; the challenge
	// is returned so the member can use it, with word that it may be late.
	Delayed bool
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
	// SignOut ends the session behind sessionToken, as its own act. The API's
	// role cannot end sessions itself (auth state is argon_auth's to write).
	// ErrNoSession if it is not a live session.
	SignOut(ctx context.Context, sessionToken string) error
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
	ActGuarantee             ActKind = "guarantee"                // 0008: a guarantor's own consent
	ActGuaranteeWithdrawal   ActKind = "guarantee_withdrawal"     // 0008
	ActApprovalSignature     ActKind = "approval_signature"       // 0010: signing a request
	ActApprovalRejection     ActKind = "approval_rejection"       // 0010: rejecting one
	ActContributionRecord    ActKind = "contribution_record"      // 0011: recording a contribution
	ActWithdrawalPayout      ActKind = "withdrawal_payout"        // 0011: recording a payout
	ActWithdrawalAuthorise   ActKind = "withdrawal_authorisation" // 0012: authorising one to execute
	ActWithdrawalRequest     ActKind = "withdrawal_request"       // 0016: raising one
)

// ActKinds is every kind a proof can be minted for: credential_acts.kind.
// (Finding 24: 0008's guarantee kinds were missing here, so no client could
// obtain a proof for a guarantee through the API. Finding 36, the same again:
// 0011's and 0012's money acts. TestApiMoney01 now mints, through the API,
// every kind the schema's proof_act_kind_known allows, so the next one fails a
// test instead of waiting to be found.)
var ActKinds = []ActKind{ActLedgerBatch, ActPinChange, ActMemberReset, ActPhoneChange, ActLockRelease,
	ActSignatoryResetRequest, ActSignatoryPhoneRequest, ActSignatoryApproval,
	ActGuarantee, ActGuaranteeWithdrawal, ActApprovalSignature, ActApprovalRejection,
	ActContributionRecord, ActWithdrawalPayout, ActWithdrawalAuthorise, ActWithdrawalRequest}

// Proof is a minted step-up. Its id is what the act names; it is spent by the
// act, inside the act's own transaction, and by nothing else.
type Proof struct {
	ID        string
	Act       ActKind
	ExpiresAt time.Time
}

// SMS hands a text to a provider for a phone number (deploy/AUTH_SMS.md).
// It answers WHAT HAPPENED (Sent); its error is only for a sender that cannot
// send at all (UnavailableSMS). Tests use fakes.
type SMS interface {
	Send(ctx context.Context, to, text string) (Sent, error)
}

// SendOutcome: what came of handing a text to the provider. Four facts, never
// blurred (the Termii sender, piece 2, 2026-10-28):
type SendOutcome string

const (
	SendAccepted   SendOutcome = "accepted"    // the provider said yes and gave a message id; NOT "delivered"
	SendRefused    SendOutcome = "refused"     // the provider said no: the code certainly did not go
	SendNotReached SendOutcome = "not_reached" // the connection was never made: the code certainly did not go
	SendUnknown    SendOutcome = "unknown"     // it may have reached the provider; no readable answer came back
)

// Sent: one send's outcome. A sender with no Provider (a test fake) is
// recorded nowhere; a real provider's every send is (sms_sends, 0019).
type Sent struct {
	Provider     string
	Outcome      SendOutcome
	MessageID    string // the provider's id, when it accepted
	ProviderCode string // the provider's refusal CODE, never its wording
}

// ErrCodeNotSent: the provider refused the code or could not be reached. The
// member is told to try again in a minute (the database's resend rule), with
// an officer as the fallback if it keeps failing; never routed to an officer
// by itself, and nothing counts against the lockout: the PIN was right.
var ErrCodeNotSent = errors.New("auth: the sign-in code could not be handed to the SMS provider")

// ErrSMSUnavailable is what UnavailableSMS answers EVERY send with. The API
// turns it into "see an officer" for a new device, saying why.
var ErrSMSUnavailable = errors.New("auth: TEMPORARY: no SMS provider yet (deploy/AUTH_SMS.md); sign-in codes cannot be sent, so a new device goes to an officer")

// UnavailableSMS is the SMS adapter to run with until a provider is
// procured. It refuses LOUDLY: every send is an error and an Error-level log
// line, never a success that delivers nothing. Its refusal says it is
// temporary, so nobody later reads it as the design. Written against
// test/tier1/tier1_api_signin_test.go (under review).
type UnavailableSMS struct {
	Log *slog.Logger // nil: slog.Default()
}

func (u UnavailableSMS) Send(ctx context.Context, to, _ string) (Sent, error) {
	log := u.Log
	if log == nil {
		log = slog.Default()
	}
	// Never the text: it carries the code. Never the whole number either.
	log.ErrorContext(ctx, "REFUSED to send a sign-in code: TEMPORARY, no SMS provider yet (deploy/AUTH_SMS.md); the person is routed to an officer",
		"to_last4", last4(to))
	return Sent{}, ErrSMSUnavailable
}

func last4(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return "…" + phone[len(phone)-4:]
}

// LogNotifier stands in for telling a person's other devices that a new one
// signed in, until a push or SMS channel exists. It does not pretend to have
// told anyone: every call is a Warn line saying nobody was told, and that this
// is temporary. It returns nil so a sign-in is not refused for want of it.
type LogNotifier struct {
	Log *slog.Logger // nil: slog.Default()
}

func (n LogNotifier) NewDeviceSignedIn(ctx context.Context, userID string, at time.Time) error {
	log := n.Log
	if log == nil {
		log = slog.Default()
	}
	log.WarnContext(ctx, "NOT TOLD: a new device signed in, and no channel exists yet to tell the person's other devices (TEMPORARY)",
		"user", userID, "at", at)
	return nil
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
