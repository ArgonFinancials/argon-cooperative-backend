// Package vfd is the boundary to VFD, the bank behind Argon's wallet:
// re-implemented here in Go (LEDGER_CONTRACT §11), taking the knowledge of
// the running wallet platform and none of its defects (docs/VFD_INTEGRATION.md
// §2). Written against test/tier1/tier1_vfd_client_test.go and its reference,
// vfdfake_client_test.go.
//
// What every call here guarantees:
//   - A TIMEOUT. No call waits forever (the wallet's client had none).
//   - EVERY ANSWER READ. A 2xx is not success: VFD's own status is read, and
//     an answer that cannot be read strictly is not taken as anything good.
//   - FOUR OUTCOMES, NEVER BLURRED, for a call that asks VFD to act:
//     accepted, refused, not_reached (the connection was never made, so VFD
//     did nothing), unknown (VFD may have acted: a timeout, a 5xx, an
//     unreadable answer). Unknown is never reported as refused: a caller that
//     believed "refused" would release money that may be leaving.
//   - A TRANSFER'S STATE is succeeded, not_found, pending or unknown. The
//     client never reports a failure VFD did not state: no failure status of
//     VFD's is known from VFD's own documentation, so none is mapped. A
//     definitive failure is the CALLER's: not found after its settlement
//     window. "99" is not taken as success (UNVERIFIED in the spec).
//   - THE CALLER'S REFERENCE, never one made here: a reference derived from
//     what authorised the transfer is what stops a retry becoming a second
//     payment. A transfer with no reference is refused before sending.
//   - NOTHING INVENTED: an account opened is an account VFD numbered, ten
//     digits as a string, or no account. One request per opening: no second
//     endpoint after an ambiguous answer (that is how one person gets two).
//   - NEVER LOGGED: the access token, a BVN, a date of birth.
package vfd

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// IntraBank is VFD's own bank code: a transfer to it is intra-bank, and is
// sent as one (sent as inter-bank, a VFD-to-VFD payout failed silently while
// still debiting, on the wallet platform).
const IntraBank = "090110"

type Config struct {
	BaseURL     string // https in production (config refuses anything else)
	AccessToken string // VFD's AccessToken header
	Timeout     time.Duration
	Log         *slog.Logger
}

// Outcome of a call that asks VFD to do something.
type Outcome string

const (
	Accepted   Outcome = "accepted"
	Refused    Outcome = "refused"
	NotReached Outcome = "not_reached"
	Unknown    Outcome = "unknown"
	// Exists: opening an account for a person VFD already has as a client
	// (VFD's 929). The account is opened with OpenAnother.
	Exists Outcome = "exists"
)

// State of a transfer, by requery.
type State string

const (
	Succeeded    State = "succeeded"
	NotFound     State = "not_found"
	Pending      State = "pending"
	StateUnknown State = "unknown"
)

// Party: an account at VFD as name enquiry reports it. A transfer's source
// and its recipient are both Parties.
type Party struct {
	AccountNo string
	Bank      string
	Name      string
	ClientID  string
	AccountID string // VFD's account id: toSession / toSavingsId / fromSavingsId
	BVN       string // inter-bank only; never logged
}

type Lookup struct {
	Outcome Outcome // accepted (found), refused (VFD says no such account), not_reached, unknown
	Code    string  // VFD's status, or http_<n>
	Party   Party
}

type Transfer struct {
	Reference  string
	From       Party
	To         Party
	AmountKobo int64
	Remark     string
}

type Sent struct {
	Outcome   Outcome
	Code      string // VFD's status, or http_<n>
	TxnID     string
	SessionID string
}

type Requeried struct {
	State      State
	Code       string // VFD's status
	TxnStatus  string // VFD's transactionStatus, as given
	TxnID      string
	SessionID  string
	AmountKobo int64 // parsed exactly from VFD's decimal string; 0 when not succeeded
	FromAcct   string
	ToAcct     string
	At         string // VFD's transactionDate, as given
}

type Person struct {
	Surname    string // sent as lastname
	OtherNames string // sent as firstname (the wallet sent them swapped for months)
	BVN        string
	DOB        time.Time
	Phone      string
}

type Opened struct {
	Outcome   Outcome
	Code      string
	Message   string
	AccountNo string // ten digits, as VFD gave it; empty unless accepted
	ClientID  string
}

// ErrStatementUnavailable: no endpoint listing an account's transactions over
// a period is known from VFD's documentation or the wallet's code. Asked for
// a statement, the client says so; it never answers an empty one (an empty
// statement says "nothing was missed").
var ErrStatementUnavailable = errors.New("vfd: no statement endpoint is known (docs/VFD_INTEGRATION.md: confirm with VFD); reconciliation cannot run")

type Client interface {
	NameEnquiry(ctx context.Context, accountNo, bank string) (Lookup, error)
	Transfer(ctx context.Context, t Transfer) (Sent, error)
	Requery(ctx context.Context, reference string) (Requeried, error)
	OpenAccount(ctx context.Context, p Person) (Opened, error)
	OpenAnother(ctx context.Context, previousAccountNo string) (Opened, error)
	Statement(ctx context.Context, accountNo string, from, to time.Time) error
}
