package vfd

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 60 seconds: the wallet's, since 2026-11-21 (§1.2). A send that runs out of
// time is pending, never refused: a longer wait means fewer of those.
const defaultTimeout = 60 * time.Second

// replyCap: the most of a reply kept (VFD's are a few hundred bytes).
const replyCap = 16 << 10

func keep(raw []byte) string {
	if len(raw) > replyCap {
		raw = raw[:replyCap]
	}
	return string(raw)
}

// wat: VFD's own time (West Africa Time), the time its history is asked in.
var wat = time.FixedZone("WAT", 3600)

// encode: a query with spaces as %20, as VFD is asked (§1.2, §4).
func encode(q url.Values) string { return strings.ReplaceAll(q.Encode(), "+", "%20") }

type client struct {
	cfg Config
	log *slog.Logger
	hc  *http.Client
}

// New returns the VFD client, or refuses a configuration it cannot call with:
// no base URL or no token, no client. No default that works.
func New(cfg Config) (Client, error) {
	if cfg.BaseURL == "" || cfg.AccessToken == "" {
		return nil, errors.New("vfd: the client needs a base URL and an access token, both set")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &client{cfg: cfg, log: log, hc: &http.Client{Timeout: cfg.Timeout}}, nil
}

var tenDigits = regexp.MustCompile(`^[0-9]{10}$`)

func last4(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "…" + s[len(s)-4:]
}

// answer: getting an answer at all. outcome is empty when VFD answered with a
// status below 500 and a body that could be read off the wire.
type answer struct {
	outcome Outcome
	status  int
	raw     []byte
}

// call makes ONE request. Its error is logged as it happened, and never with
// the request (which carries the token, and may carry a BVN).
func (c *client) call(ctx context.Context, what, method, path string, query url.Values, body any) answer {
	u := strings.TrimRight(c.cfg.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + encode(query)
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.log.ErrorContext(ctx, "vfd request not built", "what", what, "err", err.Error())
			return answer{outcome: NotReached}
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		c.log.ErrorContext(ctx, "vfd request not built", "what", what, "err", err.Error())
		return answer{outcome: NotReached}
	}
	req.Header.Set("AccessToken", c.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		// A timeout may have reached VFD (unknown); a connection never made did
		// not (not_reached).
		var ne net.Error
		out := NotReached
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			out = Unknown
		}
		c.log.ErrorContext(ctx, "vfd call failed", "what", what, "outcome", out, "err", err.Error())
		return answer{outcome: out}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.log.ErrorContext(ctx, "vfd answer could not be read", "what", what, "status", resp.StatusCode, "err", err.Error())
		return answer{outcome: Unknown, status: resp.StatusCode}
	}
	if resp.StatusCode >= 500 {
		// VFD's own failure: the request reached it, and it may have acted.
		c.log.ErrorContext(ctx, "vfd answered with its own failure", "what", what, "status", resp.StatusCode)
		return answer{outcome: Unknown, status: resp.StatusCode, raw: raw}
	}
	return answer{status: resp.StatusCode, raw: raw}
}

type envelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// read: VFD's envelope, or not readable. An envelope with no status is not
// readable: there is nothing in it to believe.
func read(a answer) (envelope, bool) {
	var e envelope
	if json.Unmarshal(a.raw, &e) != nil || e.Status == "" {
		return e, false
	}
	return e, true
}

func naira(kobo int64) string { return fmt.Sprintf("%d.%02d", kobo/100, kobo%100) }

// kobo from VFD's naira decimal, as text: digits, then optionally a point and
// one or two digits. A JSON string or a JSON number, read as its text, never
// through a float.
var nairaDecimal = regexp.MustCompile(`^([0-9]{1,15})(?:\.([0-9]{1,2}))?$`)

func koboOf(raw json.RawMessage) (int64, bool) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(raw, &s) != nil {
			return 0, false
		}
	}
	m := nairaDecimal.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	frac := m[2]
	if len(frac) == 1 {
		frac += "0"
	}
	var k int64
	if frac != "" {
		k, _ = strconv.ParseInt(frac, 10, 64)
	}
	return n*100 + k, true
}

func (c *client) Transfer(ctx context.Context, t Transfer) (Sent, error) {
	if strings.TrimSpace(t.Reference) == "" {
		return Sent{}, errors.New("vfd: a transfer names its reference (derived from what authorised it); nothing was sent")
	}
	if t.AmountKobo <= 0 {
		return Sent{}, errors.New("vfd: a transfer moves a positive amount; nothing was sent")
	}
	s := sha512.Sum512([]byte(t.From.AccountNo + t.To.AccountNo))
	body := map[string]string{
		"fromAccount": t.From.AccountNo, "fromClientId": t.From.ClientID, "fromClient": t.From.Name, "fromSavingsId": t.From.AccountID,
		"toClient": t.To.Name, "toAccount": t.To.AccountNo, "toBank": t.To.Bank,
		"signature": hex.EncodeToString(s[:]), "amount": naira(t.AmountKobo),
		"remark": t.Remark, "reference": t.Reference,
	}
	if t.To.Bank == IntraBank {
		body["transferType"], body["toClientId"], body["toSavingsId"] = "intra", t.To.ClientID, t.To.AccountID
	} else {
		body["transferType"], body["toSession"], body["toBvn"] = "inter", t.To.AccountID, t.To.BVN
	}
	a := c.call(ctx, "transfer", http.MethodPost, "/transfer", nil, body)
	out := Sent{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := read(a)
		var d struct {
			TxnID     string `json:"txnId"`
			SessionID string `json:"sessionId"`
		}
		if ok && len(e.Data) > 0 {
			_ = json.Unmarshal(e.Data, &d) // ids are informative; the status decides
		}
		switch {
		case !ok:
			out = Sent{Outcome: Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case a.status >= 400 || e.Status != "00":
			out = Sent{Outcome: Refused, Code: e.Status}
		default:
			out = Sent{Outcome: Accepted, Code: e.Status, TxnID: d.TxnID, SessionID: d.SessionID}
		}
	}
	out.HTTPStatus, out.Reply = a.status, keep(a.raw)
	c.log.InfoContext(ctx, "vfd transfer", "reference", t.Reference, "outcome", out.Outcome, "code", out.Code, "txn", out.TxnID, "to_last4", last4(t.To.AccountNo))
	return out, nil
}

func (c *client) Requery(ctx context.Context, reference string) (Requeried, error) {
	if strings.TrimSpace(reference) == "" {
		return Requeried{}, errors.New("vfd: a requery names the reference")
	}
	a := c.call(ctx, "requery", http.MethodGet, "/transactions", url.Values{"reference": {reference}}, nil)
	out := Requeried{State: StateUnknown}
	if a.outcome == "" {
		e, ok := read(a)
		notFound := ok && e.Status == "108" // only 108 has proven safe (§1.6: 29 of 29)
		var d struct {
			TxnID     string          `json:"TxnId"`
			Amount    json.RawMessage `json:"amount"`
			AccountNo string          `json:"accountNo"`
			From      string          `json:"fromAccountNo"`
			TxnStatus string          `json:"transactionStatus"`
			Date      string          `json:"transactionDate"`
			SessionID string          `json:"sessionId"`
			Type      string          `json:"transactionType"`
		}
		dataOK := ok && len(e.Data) > 0 && json.Unmarshal(e.Data, &d) == nil
		switch {
		case notFound:
			out = Requeried{State: NotFound, Code: e.Status}
		case !ok || a.status >= 400:
			out = Requeried{State: StateUnknown, Code: e.Status}
		case e.Status != "00" || !dataOK:
			out = Requeried{State: Pending, Code: e.Status, TxnStatus: d.TxnStatus}
		default:
			out = Requeried{State: Pending, Code: e.Status, TxnStatus: d.TxnStatus, TxnID: d.TxnID, SessionID: d.SessionID, FromAcct: d.From, ToAcct: d.AccountNo, At: d.Date, TxnType: d.Type}
			if d.TxnStatus == "00" { // §1.6: 00 with 00, and nothing else, is paid
				if k, ok := koboOf(d.Amount); ok {
					out.State, out.AmountKobo = Succeeded, k
				} else {
					// Succeeded, for an amount nobody can read: not a success to
					// record on a guess. A person looks.
					out.State = StateUnknown
				}
			}
		}
	}
	out.HTTPStatus, out.Reply = a.status, keep(a.raw)
	c.log.InfoContext(ctx, "vfd requery", "reference", reference, "state", out.State, "code", out.Code, "txn_status", out.TxnStatus)
	return out, nil
}

func (c *client) Pool(ctx context.Context) (Pool, error) {
	a := c.call(ctx, "pool enquiry", http.MethodGet, "/account/enquiry", nil, nil)
	out := Pool{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := read(a)
		var d struct {
			AccountNo string `json:"accountNo"`
			AccountID string `json:"accountId"`
			Client    string `json:"client"`
			ClientID  string `json:"clientId"`
		}
		dataOK := ok && len(e.Data) > 0 && json.Unmarshal(e.Data, &d) == nil
		switch {
		case !ok:
			out = Pool{Outcome: Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case a.status >= 400 || e.Status != "00" || !dataOK || !tenDigits.MatchString(d.AccountNo) || d.AccountID == "" || d.ClientID == "":
			out = Pool{Outcome: Refused, Code: e.Status}
		default:
			out = Pool{Outcome: Accepted, Code: e.Status, Party: Party{AccountNo: d.AccountNo, Bank: IntraBank, Name: d.Client, ClientID: d.ClientID, AccountID: d.AccountID}}
		}
	}
	c.log.InfoContext(ctx, "vfd pool enquiry", "outcome", out.Outcome, "code", out.Code, "pool_last4", last4(out.Party.AccountNo))
	return out, nil
}

func (c *client) History(ctx context.Context, accountNo string, from, to time.Time, page int) (History, error) {
	if !tenDigits.MatchString(accountNo) {
		return History{}, errors.New("vfd: an account's history names a ten-digit account")
	}
	const layout = "2006-01-02 15:04:05"
	q := url.Values{"accountNo": {accountNo}, "startDate": {from.In(wat).Format(layout)}, "endDate": {to.In(wat).Format(layout)},
		"transactionType": {"wallet"}, "page": {strconv.Itoa(page)}}
	a := c.call(ctx, "history", http.MethodGet, "/account/transactions", q, nil)
	out := History{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := read(a)
		var d struct {
			Content []struct {
				TxnID     string          `json:"transactionId"`
				Amount    json.RawMessage `json:"amount"`
				From      string          `json:"fromAccountNo"`
				To        string          `json:"toAccountNo"`
				SessionID string          `json:"sessionId"`
				Time      string          `json:"time"`
				Type      string          `json:"transactionType"`
				Response  string          `json:"transactionResponse"`
			} `json:"content"`
		}
		dataOK := ok && len(e.Data) > 0 && json.Unmarshal(e.Data, &d) == nil
		switch {
		case !ok:
			out = History{Outcome: Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case a.status >= 400 || e.Status != "00" || !dataOK:
			out = History{Outcome: Refused, Code: e.Status}
		default:
			out = History{Outcome: Accepted, Code: e.Status, Rows: []HistoryRow{}}
			for _, r := range d.Content {
				k, ok := koboOf(r.Amount)
				if !ok {
					// A line whose amount cannot be read exactly: not a page to
					// believe in part. The sweep says it could not run.
					return History{Outcome: Unknown, Code: "amount_unreadable"}, nil
				}
				out.Rows = append(out.Rows, HistoryRow{TxnID: r.TxnID, AmountKobo: k, FromAcct: r.From, ToAcct: r.To, SessionID: r.SessionID, Time: r.Time, Type: r.Type, Response: r.Response})
			}
		}
	}
	c.log.InfoContext(ctx, "vfd history", "account_last4", last4(accountNo), "page", page, "outcome", out.Outcome, "rows", len(out.Rows))
	return out, nil
}

func (c *client) NameEnquiry(ctx context.Context, accountNo, bank string) (Lookup, error) {
	tt := "inter"
	if bank == IntraBank {
		tt = "intra"
	}
	a := c.call(ctx, "name enquiry", http.MethodGet, "/transfer/recipient", url.Values{"accountNo": {accountNo}, "bank": {bank}, "transfer_type": {tt}}, nil)
	out := Lookup{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := read(a)
		var d struct {
			Name     string `json:"name"`
			ClientID string `json:"clientId"`
			BVN      string `json:"bvn"`
			Account  struct {
				ID string `json:"id"`
			} `json:"account"`
		}
		if ok && len(e.Data) > 0 {
			_ = json.Unmarshal(e.Data, &d)
		}
		switch {
		case !ok:
			out = Lookup{Outcome: Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case a.status >= 400 || e.Status != "00" || strings.TrimSpace(d.Name) == "":
			out = Lookup{Outcome: Refused, Code: e.Status}
		default:
			out = Lookup{Outcome: Accepted, Code: e.Status, Party: Party{AccountNo: accountNo, Bank: bank, Name: d.Name, ClientID: d.ClientID, AccountID: d.Account.ID, BVN: d.BVN}}
		}
	}
	c.log.InfoContext(ctx, "vfd name enquiry", "account_last4", last4(accountNo), "bank", bank, "outcome", out.Outcome, "code", out.Code)
	return out, nil
}

// opened reads an account-opening answer. The number is VFD's, ten digits as
// a JSON string (data.accountNo, or account_number at the top), or there is
// no account: a number VFD sent as a JSON number is not taken (a leading zero
// would be gone), and none is ever made up.
func opened(a answer) Opened {
	if a.outcome != "" {
		return Opened{Outcome: a.outcome}
	}
	e, ok := read(a)
	if !ok {
		return Opened{Outcome: Unknown, Code: fmt.Sprintf("http_%d", a.status)}
	}
	var top struct {
		AccountNumber json.RawMessage `json:"account_number"`
	}
	_ = json.Unmarshal(a.raw, &top)
	var d struct {
		AccountNo json.RawMessage `json:"accountNo"`
		ClientID  string          `json:"clientId"`
	}
	if len(e.Data) > 0 {
		_ = json.Unmarshal(e.Data, &d)
	}
	number := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil && tenDigits.MatchString(s) {
			return s
		}
		return ""
	}
	n := number(d.AccountNo)
	if n == "" {
		n = number(top.AccountNumber)
	}
	switch {
	case e.Status == "929":
		return Opened{Outcome: Exists, Code: e.Status, Message: e.Message}
	case a.status >= 400 || e.Status != "00":
		return Opened{Outcome: Refused, Code: e.Status, Message: e.Message}
	case n == "":
		return Opened{Outcome: Unknown, Code: e.Status, Message: "VFD answered success without an account number: no account is recorded"}
	}
	return Opened{Outcome: Accepted, Code: e.Status, Message: e.Message, AccountNo: n, ClientID: d.ClientID}
}

func (c *client) OpenAccount(ctx context.Context, p Person) (Opened, error) {
	a := c.call(ctx, "open account", http.MethodPost, "/client/tier/individual", nil,
		map[string]string{"firstname": p.OtherNames, "lastname": p.Surname, "bvn": p.BVN, "dob": p.DOB.Format("02-Jan-2006"), "phone": p.Phone})
	out := opened(a)
	c.log.InfoContext(ctx, "vfd open account", "outcome", out.Outcome, "code", out.Code, "account_last4", last4(out.AccountNo))
	return out, nil
}

func (c *client) OpenAnother(ctx context.Context, previous string) (Opened, error) {
	a := c.call(ctx, "open another account", http.MethodPost, "/client/individual", nil, map[string]string{"previousAccountNo": previous})
	out := opened(a)
	c.log.InfoContext(ctx, "vfd open another account", "outcome", out.Outcome, "code", out.Code, "account_last4", last4(out.AccountNo))
	return out, nil
}
