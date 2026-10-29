package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"
)

// THE TERMII SENDER (reviewed in four pieces, 2026-10-28; written against
// test/tier1/tier1_sms_termii_test.go and its reference, smsfake_termii_test.go).
// Re-implemented here, not called through the wallet or loan services: the
// cooperative's sign-in must not depend on a service whose own review found
// real defects (deploy/AUTH_SMS.md).
//
//   - "Sent" means ACCEPTED BY TERMII: a 2xx, a readable body reporting
//     success, and a message id. Never "delivered" (delivery reports: a later
//     stage). Four outcomes, never blurred: accepted, refused, not_reached,
//     unknown. A dead connection is reported as one, never as a parsing error.
//   - ONE CALL PER CODE. No retry here: a retried send is a second code on the
//     member's phone, and how often a member may be sent one is the database's
//     rule (60 seconds, 0003), not the sender's.
//   - THE DND ROUTE, fixed in code: most Nigerian numbers are on
//     Do-Not-Disturb, and a code that cannot arrive is no path at all.
//   - ONLY +234 E.164. Anything else is refused before sending; no guessing.
//   - NEVER LOGGED: the code, the message text, the API key, the full number.
//     One line per send: the outcome, Termii's message id or refusal code, the
//     last four digits, and for a failure the error as it happened.

const termiiDefaultTimeout = 10 * time.Second

var e164Nigeria = regexp.MustCompile(`^\+234[0-9]{10}$`)

type termii struct {
	cfg TermiiConfig
	log *slog.Logger
	hc  *http.Client
}

// NewTermii returns the Termii sender, or refuses a configuration it cannot
// send with: no URL, no key or no sender ID, no sender.
func NewTermii(cfg TermiiConfig) (SMS, error) {
	if cfg.URL == "" || cfg.APIKey == "" || cfg.SenderID == "" {
		return nil, errors.New("auth: the Termii sender needs TERMII_URL, TERMII_API_KEY and TERMII_SENDER_ID, all set")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = termiiDefaultTimeout
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &termii{cfg: cfg, log: log, hc: &http.Client{Timeout: cfg.Timeout}}, nil
}

func (t *termii) Send(ctx context.Context, to, text string) (Sent, error) {
	out := Sent{Provider: "termii"}
	if !e164Nigeria.MatchString(to) {
		out.Outcome, out.ProviderCode = SendRefused, "number_not_e164"
		t.log.ErrorContext(ctx, "sign-in code NOT SENT: the number is not +234 E.164", "outcome", out.Outcome, "to_last4", last4(to))
		return out, nil
	}
	body, err := json.Marshal(map[string]string{"to": to, "from": t.cfg.SenderID, "sms": text, "type": "plain", "channel": "dnd", "api_key": t.cfg.APIKey})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.hc.Do(req)
	if err != nil {
		// A timeout may have reached Termii (unknown); a connection never made
		// did not (not_reached). The error is logged AS IT HAPPENED, and never
		// with the request, which carries the code and the key.
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
			out.Outcome = SendUnknown
		} else {
			out.Outcome = SendNotReached
		}
		t.log.ErrorContext(ctx, "sign-in code send failed", "outcome", out.Outcome, "to_last4", last4(to), "err", err.Error())
		return out, nil
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(resp.Body)
	var ans struct {
		Code      string `json:"code"`
		MessageID any    `json:"message_id"`
	}
	readable := rerr == nil && json.Unmarshal(raw, &ans) == nil
	id := ""
	if ans.MessageID != nil {
		id = fmt.Sprint(ans.MessageID)
	}
	switch {
	case resp.StatusCode >= 500:
		out.Outcome = SendUnknown // Termii's own failure: the request reached it
	case resp.StatusCode >= 400:
		out.Outcome, out.ProviderCode = SendRefused, ans.Code
		if out.ProviderCode == "" {
			out.ProviderCode = fmt.Sprintf("http_%d", resp.StatusCode)
		}
	case !readable:
		out.Outcome = SendUnknown
	case ans.Code != "ok":
		out.Outcome, out.ProviderCode = SendRefused, ans.Code
	case id == "":
		out.Outcome = SendUnknown
	default:
		out.Outcome, out.MessageID = SendAccepted, id
	}
	if out.Outcome == SendAccepted {
		t.log.InfoContext(ctx, "sign-in code accepted by Termii", "outcome", out.Outcome, "message_id", out.MessageID, "to_last4", last4(to))
	} else {
		t.log.ErrorContext(ctx, "sign-in code not accepted by Termii", "outcome", out.Outcome, "provider_code", out.ProviderCode, "status", resp.StatusCode, "to_last4", last4(to))
	}
	return out, nil
}
