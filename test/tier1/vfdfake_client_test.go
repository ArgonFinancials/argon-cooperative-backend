//go:build tier1

// THE MUTATION VEHICLE FOR THE VFD CLIENT — test code, NOT the client. A
// reference implementation of docs/VFD_INTEGRATION.md §2 as stage 3 builds
// it, breakable one named way per run (ARGON_TEST_VFD_FAKE=<break>). Most
// breaks are a defect the running wallet platform actually had:
//
//	no_timeout             an HTTP client with no timeout (the wallet's)
//	status_any_2xx         any 2xx taken as accepted, VFD's status unread (the wallet's: a refusal credited the receiver)
//	http_5xx_read          a 5xx whose body says 00 taken as accepted
//	timeout_as_refused     no answer in time reported as refused (the caller frees money that may be leaving)
//	timeout_as_not_reached no answer in time reported as never sent
//	dead_as_unknown        a connection never made reported as unknown
//	unreadable_as_refused  an unreadable 200 reported as refused
//	reference_made_here    a reference made in the client from the clock (the wallet's: 16 retries, 16 payments)
//	zero_amount_sent       a transfer of nothing sent to VFD
//	intra_as_inter         VFD-to-VFD sent as inter-bank (the wallet's: failed silently while debiting)
//	signature_reversed     the signature over to+from
//	amount_float_in        VFD's naira string read through a float ("4.35" becomes 434 kobo)
//	ninety_nine_success    transactionStatus 99 taken as success (the wallet's, unverified)
//	notfound_as_pending    108 / "no transaction" reported as pending (a failed transfer never ends)
//	unreadable_as_notfound a requery that cannot be read reported as not found (the wallet's: refund, then a late payout)
//	names_swapped          surname sent as firstname (the wallet's, for months)
//	number_invented        no number from VFD, so one is made: "10" + eight digits (the wallet's)
//	number_float_taken     a number VFD sent as a JSON number taken
//	open_fallback          an ambiguous opening retried on /client/create (two accounts for one person)
//	statement_empty        no statement endpoint answered as an empty statement (nothing was missed)
//	token_logged           the request logged with its headers: the token with it
//	token_optional         a client built with no token
//	enquiry_as_inter       name enquiry for VFD's own code asked as inter-bank
//	found_ignoring_status  any 200 to a name enquiry taken as found
//	inter_no_session       inter-bank sent without the name enquiry's session (VFD: "toClient is Mandatory", and its kind)
//	exists_as_refused      VFD's 929 (the client exists) reported as a refusal: the member is never opened
package tier1

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
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/argon/backend/internal/vfd"
)

type refVFD struct {
	cfg vfd.Config
	brk string
	log *slog.Logger
	hc  *http.Client
}

func newRefVFD(cfg vfd.Config, brk string) (vfd.Client, error) {
	if cfg.BaseURL == "" || (cfg.AccessToken == "" && brk != "token_optional") {
		return nil, errors.New("vfd: base URL and access token are both required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	hc := &http.Client{Timeout: cfg.Timeout}
	if brk == "no_timeout" {
		hc = &http.Client{}
	}
	return &refVFD{cfg: cfg, brk: brk, log: log, hc: hc}, nil
}

var tenDigitsRef = regexp.MustCompile(`^[0-9]{10}$`)

func last4v(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "…" + s[len(s)-4:]
}

// call: one request; the outcome of getting an answer at all, and the answer.
// reached: false only when the connection was never made.
type refAnswer struct {
	outcome vfd.Outcome // "" when VFD answered readably with a 2xx
	status  int
	raw     []byte
}

func (r *refVFD) call(ctx context.Context, what, method, path string, query url.Values, body any) refAnswer {
	u := strings.TrimRight(r.cfg.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("AccessToken", r.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	if r.brk == "token_logged" {
		r.log.DebugContext(ctx, "vfd request", "what", what, "headers", fmt.Sprint(req.Header))
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		var ne net.Error
		timeout := errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)
		out := vfd.NotReached
		switch {
		case timeout && r.brk == "timeout_as_refused":
			out = vfd.Refused
		case timeout && r.brk == "timeout_as_not_reached":
			out = vfd.NotReached
		case timeout:
			out = vfd.Unknown
		case r.brk == "dead_as_unknown":
			out = vfd.Unknown
		}
		r.log.ErrorContext(ctx, "vfd call failed", "what", what, "outcome", out, "err", err.Error())
		return refAnswer{outcome: out}
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		r.log.ErrorContext(ctx, "vfd answer unreadable", "what", what, "status", resp.StatusCode, "err", rerr.Error())
		return refAnswer{outcome: vfd.Unknown, status: resp.StatusCode}
	}
	if resp.StatusCode >= 500 && r.brk != "http_5xx_read" {
		r.log.ErrorContext(ctx, "vfd answered with its own failure", "what", what, "status", resp.StatusCode)
		return refAnswer{outcome: vfd.Unknown, status: resp.StatusCode, raw: raw}
	}
	return refAnswer{status: resp.StatusCode, raw: raw}
}

type refEnvelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (r *refVFD) read(a refAnswer) (refEnvelope, bool) {
	var e refEnvelope
	if json.Unmarshal(a.raw, &e) != nil || e.Status == "" {
		return e, false
	}
	return e, true
}

func (r *refVFD) Transfer(ctx context.Context, t vfd.Transfer) (vfd.Sent, error) {
	if r.brk == "reference_made_here" {
		t.Reference = "Argon-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if strings.TrimSpace(t.Reference) == "" {
		return vfd.Sent{}, errors.New("vfd: a transfer names its reference (derived from what authorised it); nothing was sent")
	}
	if t.AmountKobo <= 0 && r.brk != "zero_amount_sent" {
		return vfd.Sent{}, errors.New("vfd: a transfer moves a positive amount; nothing was sent")
	}
	intra := t.To.Bank == vfd.IntraBank && r.brk != "intra_as_inter"
	s := sha512.Sum512([]byte(t.From.AccountNo + t.To.AccountNo))
	if r.brk == "signature_reversed" {
		s = sha512.Sum512([]byte(t.To.AccountNo + t.From.AccountNo))
	}
	body := map[string]string{
		"fromAccount": t.From.AccountNo, "fromClientId": t.From.ClientID, "fromClient": t.From.Name, "fromSavingsId": t.From.AccountID,
		"toClient": t.To.Name, "toAccount": t.To.AccountNo, "toBank": t.To.Bank,
		"signature": hex.EncodeToString(s[:]), "amount": fmt.Sprintf("%d.%02d", t.AmountKobo/100, t.AmountKobo%100),
		"remark": t.Remark, "reference": t.Reference,
	}
	if intra {
		body["transferType"], body["toClientId"], body["toSavingsId"] = "intra", t.To.ClientID, t.To.AccountID
	} else {
		body["transferType"], body["toSession"], body["toBvn"] = "inter", t.To.AccountID, t.To.BVN
		if r.brk == "inter_no_session" {
			delete(body, "toSession")
		}
	}
	a := r.call(ctx, "transfer", http.MethodPost, "/transfer", nil, body)
	out := vfd.Sent{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := r.read(a)
		var d struct {
			TxnID     string `json:"txnId"`
			SessionID string `json:"sessionId"`
		}
		if ok && len(e.Data) > 0 {
			json.Unmarshal(e.Data, &d)
		}
		switch {
		case a.status >= 200 && a.status < 300 && r.brk == "status_any_2xx":
			out = vfd.Sent{Outcome: vfd.Accepted, Code: e.Status, TxnID: d.TxnID, SessionID: d.SessionID}
		case !ok && r.brk == "unreadable_as_refused":
			out = vfd.Sent{Outcome: vfd.Refused, Code: "unreadable"}
		case !ok:
			out = vfd.Sent{Outcome: vfd.Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case a.status >= 400:
			out = vfd.Sent{Outcome: vfd.Refused, Code: e.Status}
		case e.Status != "00":
			out = vfd.Sent{Outcome: vfd.Refused, Code: e.Status}
		default:
			out = vfd.Sent{Outcome: vfd.Accepted, Code: e.Status, TxnID: d.TxnID, SessionID: d.SessionID}
		}
	}
	r.log.InfoContext(ctx, "vfd transfer", "reference", t.Reference, "outcome", out.Outcome, "code", out.Code, "txn", out.TxnID, "to_last4", last4v(t.To.AccountNo))
	return out, nil
}

// kobo from VFD's naira decimal text, exactly: digits, optionally a point and
// one or two digits. No float.
var nairaText = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]{1,2}))?$`)

func refKobo(raw json.RawMessage, brk string) (int64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if brk == "amount_float_in" {
		f, err := strconv.ParseFloat(s, 64)
		return int64(f * 100), err == nil
	}
	m := nairaText.FindStringSubmatch(s)
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
	k := int64(0)
	if frac != "" {
		k, _ = strconv.ParseInt(frac, 10, 64)
	}
	return n*100 + k, true
}

func (r *refVFD) Requery(ctx context.Context, reference string) (vfd.Requeried, error) {
	if strings.TrimSpace(reference) == "" {
		return vfd.Requeried{}, errors.New("vfd: a requery names the reference")
	}
	a := r.call(ctx, "requery", http.MethodGet, "/transactions", url.Values{"reference": {reference}}, nil)
	out := vfd.Requeried{State: vfd.StateUnknown}
	if a.outcome == "" {
		e, ok := r.read(a)
		notFound := ok && (e.Status == "108" || strings.Contains(strings.ToLower(e.Message), "no transaction"))
		var d struct {
			TxnID     string          `json:"TxnId"`
			Amount    json.RawMessage `json:"amount"`
			AccountNo string          `json:"accountNo"`
			From      string          `json:"fromAccountNo"`
			TxnStatus string          `json:"transactionStatus"`
			Date      string          `json:"transactionDate"`
			SessionID string          `json:"sessionId"`
		}
		dataOK := ok && len(e.Data) > 0 && json.Unmarshal(e.Data, &d) == nil
		switch {
		case !ok && r.brk == "unreadable_as_notfound":
			out.State = vfd.NotFound
		case !ok || a.status >= 400 && !notFound:
			out.State = vfd.StateUnknown
		case notFound && r.brk == "notfound_as_pending":
			out = vfd.Requeried{State: vfd.Pending, Code: e.Status}
		case notFound:
			out = vfd.Requeried{State: vfd.NotFound, Code: e.Status}
		case e.Status != "00" || !dataOK:
			out = vfd.Requeried{State: vfd.Pending, Code: e.Status, TxnStatus: d.TxnStatus}
		default:
			out = vfd.Requeried{State: vfd.Pending, Code: e.Status, TxnStatus: d.TxnStatus, TxnID: d.TxnID, SessionID: d.SessionID, FromAcct: d.From, ToAcct: d.AccountNo, At: d.Date}
			success := d.TxnStatus == "00" || strings.EqualFold(d.TxnStatus, "SUCCESS") || strings.EqualFold(d.TxnStatus, "SUCCESSFUL") ||
				(d.TxnStatus == "99" && r.brk == "ninety_nine_success")
			if success {
				if k, ok := refKobo(d.Amount, r.brk); ok {
					out.State, out.AmountKobo = vfd.Succeeded, k
				} else {
					out.State = vfd.StateUnknown // succeeded, for an amount nobody can read: a person looks
				}
			}
		}
	}
	r.log.InfoContext(ctx, "vfd requery", "reference", reference, "state", out.State, "code", out.Code, "txn_status", out.TxnStatus)
	return out, nil
}

func (r *refVFD) NameEnquiry(ctx context.Context, accountNo, bank string) (vfd.Lookup, error) {
	tt := "inter"
	if bank == vfd.IntraBank && r.brk != "enquiry_as_inter" {
		tt = "intra"
	}
	a := r.call(ctx, "name enquiry", http.MethodGet, "/transfer/recipient", url.Values{"accountNo": {accountNo}, "bank": {bank}, "transfer_type": {tt}}, nil)
	out := vfd.Lookup{Outcome: a.outcome}
	if a.outcome == "" {
		e, ok := r.read(a)
		var d struct {
			Name     string `json:"name"`
			ClientID string `json:"clientId"`
			BVN      string `json:"bvn"`
			Account  struct {
				Number string `json:"number"`
				ID     string `json:"id"`
			} `json:"account"`
		}
		if ok && len(e.Data) > 0 {
			json.Unmarshal(e.Data, &d)
		}
		switch {
		case ok && r.brk == "found_ignoring_status":
			out = vfd.Lookup{Outcome: vfd.Accepted, Code: e.Status, Party: vfd.Party{AccountNo: accountNo, Bank: bank, Name: d.Name, ClientID: d.ClientID, AccountID: d.Account.ID, BVN: d.BVN}}
		case !ok:
			out = vfd.Lookup{Outcome: vfd.Unknown, Code: fmt.Sprintf("http_%d", a.status)}
		case e.Status != "00" || d.Name == "":
			out = vfd.Lookup{Outcome: vfd.Refused, Code: e.Status}
		default:
			out = vfd.Lookup{Outcome: vfd.Accepted, Code: e.Status, Party: vfd.Party{AccountNo: accountNo, Bank: bank, Name: d.Name, ClientID: d.ClientID, AccountID: d.Account.ID, BVN: d.BVN}}
		}
	}
	r.log.InfoContext(ctx, "vfd name enquiry", "account_last4", last4v(accountNo), "bank", bank, "outcome", out.Outcome, "code", out.Code)
	return out, nil
}

func (r *refVFD) opened(ctx context.Context, what string, a refAnswer) vfd.Opened {
	out := vfd.Opened{Outcome: a.outcome}
	if a.outcome != "" {
		return out
	}
	e, ok := r.read(a)
	if !ok {
		return vfd.Opened{Outcome: vfd.Unknown, Code: fmt.Sprintf("http_%d", a.status)}
	}
	var top struct {
		AccountNumber json.RawMessage `json:"account_number"`
	}
	json.Unmarshal(a.raw, &top)
	var d struct {
		AccountNo json.RawMessage `json:"accountNo"`
		ClientID  string          `json:"clientId"`
	}
	if len(e.Data) > 0 {
		json.Unmarshal(e.Data, &d)
	}
	number := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil && tenDigitsRef.MatchString(s) {
			return s
		}
		if r.brk == "number_float_taken" {
			var f float64
			if json.Unmarshal(raw, &f) == nil && f > 0 {
				return fmt.Sprintf("%.0f", f)
			}
		}
		return ""
	}
	n := number(d.AccountNo)
	if n == "" {
		n = number(top.AccountNumber)
	}
	switch {
	case e.Status == "929" && r.brk != "exists_as_refused":
		return vfd.Opened{Outcome: vfd.Exists, Code: e.Status, Message: e.Message}
	case a.status >= 400 || e.Status != "00":
		return vfd.Opened{Outcome: vfd.Refused, Code: e.Status, Message: e.Message}
	case n == "" && r.brk == "number_invented":
		return vfd.Opened{Outcome: vfd.Accepted, Code: e.Status, AccountNo: fmt.Sprintf("10%08d", rand.IntN(100000000)), ClientID: d.ClientID}
	case n == "":
		// VFD said yes and gave no number: there is no account to record.
		return vfd.Opened{Outcome: vfd.Unknown, Code: e.Status, Message: "VFD answered success without an account number: no account is recorded"}
	}
	return vfd.Opened{Outcome: vfd.Accepted, Code: e.Status, Message: e.Message, AccountNo: n, ClientID: d.ClientID}
}

func (r *refVFD) OpenAccount(ctx context.Context, p vfd.Person) (vfd.Opened, error) {
	first, last := p.OtherNames, p.Surname
	if r.brk == "names_swapped" {
		first, last = last, first
	}
	a := r.call(ctx, "open account", http.MethodPost, "/client/tier/individual", nil,
		map[string]string{"firstname": first, "lastname": last, "bvn": p.BVN, "dob": p.DOB.Format("02-Jan-2006"), "phone": p.Phone})
	out := r.opened(ctx, "open account", a)
	if r.brk == "open_fallback" && (out.Outcome == vfd.Unknown || out.Outcome == vfd.Refused) {
		b := r.call(ctx, "open account (legacy)", http.MethodPost, "/client/create", url.Values{"bvn": {p.BVN}, "dateOfBirth": {p.DOB.Format("02-Jan-2006")}}, nil)
		out = r.opened(ctx, "open account (legacy)", b)
	}
	r.log.InfoContext(ctx, "vfd open account", "outcome", out.Outcome, "code", out.Code, "account_last4", last4v(out.AccountNo))
	return out, nil
}

func (r *refVFD) OpenAnother(ctx context.Context, previous string) (vfd.Opened, error) {
	a := r.call(ctx, "open another account", http.MethodPost, "/client/individual", nil, map[string]string{"previousAccountNo": previous})
	out := r.opened(ctx, "open another account", a)
	r.log.InfoContext(ctx, "vfd open another account", "outcome", out.Outcome, "code", out.Code, "account_last4", last4v(out.AccountNo))
	return out, nil
}

func (r *refVFD) Statement(ctx context.Context, accountNo string, from, to time.Time) error {
	if r.brk == "statement_empty" {
		return nil
	}
	return vfd.ErrStatementUnavailable
}
