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
//   - A TRANSFER'S STATE is succeeded, not_found, pending or unknown, as
//     docs/VFD_INTEGRATION.md §1.6 has it from 193 transfers looked up at VFD
//     (2026-11-24): succeeded ONLY on status 00 with transactionStatus 00;
//     not_found ONLY on 108; pending is anything else VFD said (91, 25, 99,
//     a word, another code): VFD may have created and sent that transfer, so
//     the caller holds it and shows a person. Unknown: no readable answer.
//   - EVERY REPLY KEPT: a send's and a requery's HTTP status and body, as
//     received, for the caller to store (the wallet stored neither, and so
//     nobody can say which send codes led where).
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
	"log/slog"
	"time"
)

// IntraBank is VFD's own bank code: a transfer to it is intra-bank, and is
// sent as one (sent as inter-bank, a VFD-to-VFD payout failed silently while
// still debiting, on the wallet platform).
const IntraBank = "090110"

type Config struct {
	BaseURL     string // https in production (config refuses anything else)
	AccessToken string // VFD's AccessToken header: calling VFD directly
	// ProxyKey: calling VFD through the relay on the wallet's server
	// (cmd/vfd-proxy, 2026-11-28) instead: every call signed with this key,
	// and NO token held here. Exactly one of AccessToken and ProxyKey.
	ProxyKey []byte
	Timeout  time.Duration
	Log      *slog.Logger
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
	Outcome    Outcome
	Code       string // VFD's status, or http_<n>
	TxnID      string
	SessionID  string
	HTTPStatus int    // 0: no answer at all
	Reply      string // VFD's body as received (capped); empty when none came
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
	TxnType    string // VFD's transactionType: OUTFLOW or INFLOW
	HTTPStatus int
	Reply      string
}

// Pool: Argon's VFD pool account (§1.1): every transfer goes out from it.
type Pool struct {
	Outcome Outcome
	Code    string
	Party   Party // AccountNo, AccountID (fromSavingsId), ClientID, Name (client)
}

// HistoryRow: one line of an account's history (§4).
type HistoryRow struct {
	TxnID      string // transactionId: the caller's reference for an outflow, VFD's for an inflow
	AmountKobo int64
	FromAcct   string
	ToAcct     string
	SessionID  string
	Time       string
	Type       string // INFLOW, OUTFLOW, POOL TRANSFER
	Response   string // transactionResponse, e.g. 00, 91
}

type History struct {
	Outcome Outcome
	Code    string
	Rows    []HistoryRow // empty: the last page
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

type Client interface {
	NameEnquiry(ctx context.Context, accountNo, bank string) (Lookup, error)
	Transfer(ctx context.Context, t Transfer) (Sent, error)
	Requery(ctx context.Context, reference string) (Requeried, error)
	OpenAccount(ctx context.Context, p Person) (Opened, error)
	OpenAnother(ctx context.Context, previousAccountNo string) (Opened, error)
	// Pool: GET /account/enquiry.
	Pool(ctx context.Context) (Pool, error)
	// History: GET /account/transactions, one page (0, 1, ...), from..to.
	History(ctx context.Context, accountNo string, from, to time.Time, page int) (History, error)
	// Banks: GET /bank, VFD's list of banks (0046).
	Banks(ctx context.Context) (BankList, error)
}

// Bank: one of VFD's banks, by VFD's code for it.
type Bank struct{ Code, Name string }

// BankList: VFD's list. Accepted only on VFD's 00 with at least one bank;
// an entry with no code or no name is left out, never guessed, and a code
// seen twice is kept once (the first).
type BankList struct {
	Outcome Outcome
	Code    string
	Banks   []Bank
	Skipped int // entries left out: no code, no name, or a code seen already
}
