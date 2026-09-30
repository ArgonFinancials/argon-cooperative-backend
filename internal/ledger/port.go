// Package ledger is the Go half of /LEDGER_CONTRACT.md.
//
// That document is the contract; this is its server-side shape. Change one and
// you must change the other. The frontend is written against the same contract
// and generates its client from the same OpenAPI spec.
package ledger

import (
	"context"
	"time"

	"github.com/argon/backend/internal/money"
)

// MovementKind mirrors the movement_kind enum in the database and the
// MovementKind union in the frontend. All three are generated from the OpenAPI
// spec; none is hand-maintained.
type MovementKind string

const (
	ContributionIn    MovementKind = "contribution_in"
	WithdrawalOut     MovementKind = "withdrawal_out"
	LoanDisbursement  MovementKind = "loan_disbursement"
	LoanRepayment     MovementKind = "loan_repayment"
	TransferOut       MovementKind = "transfer_out"
	PayrollRemittance MovementKind = "payroll_remittance"
	ProjectFunding    MovementKind = "project_funding"
	// OpeningBalance carries NO CHARGE, deliberately. Charging on a migration
	// takes a cut of money that was in the society's books before Argon
	// existed. It is also not a contribution: a member's statement must not
	// say they contributed on the day the software changed.
	OpeningBalance MovementKind = "opening_balance"
)

// Line is one side of a double entry. Positive debits, negative credits.
type Line struct {
	AccountID  string      `json:"accountId"`
	MemberID   *string     `json:"memberId,omitempty"`
	AmountKobo money.Kobo  `json:"amountKobo"`
	ChargeRole ChargeRole  `json:"chargeRole"`
	// SourceLine traces a posted line back to the file row it came from, which
	// is how an import is audited after the fact. A member id cannot serve this
	// purpose: a duplicate member number is itself a rejection reason, so the
	// same id legitimately appears on an importing row and a rejected one.
	SourceLine *int `json:"sourceLine,omitempty"`
}

// ChargeRole keeps the partner charge and the Argon margin on separate lines.
// Summed into one total the distinction is unrecoverable from history, and
// transaction charges are the only revenue line in the product.
type ChargeRole string

const (
	Principal     ChargeRole = "principal"
	PartnerCharge ChargeRole = "partner_charge"
	ArgonMargin   ChargeRole = "argon_margin"
)

// Batch is what postAll accepts: a whole import, in one call.
type Batch struct {
	SocietyID string       `json:"societyId"`
	Kind      MovementKind `json:"kind"`
	// AsOf is the date the society's own records say the balances are
	// accumulated to. It is NEVER the import date. A member whose savings
	// accrued over seven years must not have a ledger that begins on the day
	// the software changed.
	AsOf        *time.Time `json:"asOf,omitempty"`
	ActorUserID string     `json:"actorUserId"`
	Lines       []Line     `json:"lines"`
}

// PostedRecord is what a commit returns — and what a REPEAT commit returns.
//
// A bare boolean cannot answer the question an admin actually has. Someone who
// committed under the wrong as-at date needs to be told which date the
// balances carry, and "already posted" does not say. Without it the admin's
// next move is to re-save the file to force new bytes, which double-posts the
// society's entire opening balance — a workaround the system taught them.
type PostedRecord struct {
	Reference  string     `json:"reference"`
	Rows       int        `json:"rows"`
	TotalKobo  money.Kobo `json:"totalKobo"`
	AsOf       *time.Time `json:"asOf,omitempty"`
	PostedAt   time.Time  `json:"postedAt"`
	AlreadyHad bool       `json:"alreadyPosted"`
}

// Port is the whole write surface. THERE IS NO UPDATE OPERATION — not a
// disabled one, not one that returns an error, not one behind a permission.
// An operation that exists is an operation someone eventually calls, and if
// UpdateEntry were on this interface some future migration script or support
// tool would reach it at 2am and the audit trail would have a hole nobody can
// date.
//
// A correction is PostReversal or PostAdjustment. Both write NEW entries with
// their own ids, actors and timestamps. Neither alters the original.
type Port interface {
	// PostAll is ONE TRANSACTIONAL CALL. Either every line is written or none
	// is; there is no partial outcome.
	//
	// reference is the idempotency key, minted CLIENT-SIDE from a SHA-256 of
	// the uploaded file's content. The server never generates one. A retry of
	// one upload carries the same key; a corrected file carries its own.
	//
	// A second call with a reference already committed writes nothing, is not
	// an error, and returns the record of what the first call did.
	//
	// A FAILED call writes nothing AND does not mark the reference committed,
	// so a retry after failure can succeed.
	PostAll(ctx context.Context, reference string, batch Batch) (PostedRecord, error)

	// PostReversal writes a new entry mirroring an existing one and linking to
	// it. Per D-02 the Argon charge is refunded with the principal: a member is
	// not out of pocket for a treasurer's duplicate entry.
	PostReversal(ctx context.Context, originalID, reason, actorUserID string) (PostedRecord, error)

	// PostAdjustment writes a new balancing entry. Used when the original
	// period is closed and cannot be posted into.
	PostAdjustment(ctx context.Context, batch Batch, reason string) (PostedRecord, error)

	// Posted reports what a reference committed, or nil if it never did.
	Posted(ctx context.Context, societyID, reference string) (*PostedRecord, error)
}
