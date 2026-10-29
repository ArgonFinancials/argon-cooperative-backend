//go:build tier1

// THE MUTATION VEHICLE FOR THE TERMII SENDER — test code, NOT the sender. A
// reference implementation of what the review decided (2026-10-28), breakable
// one named way per run (ARGON_TEST_SMS_FAKE=<break>):
//
//	status_200_success     any 2xx is taken as sent, Termii's body unread
//	dead_as_parse          a dead connection reported as an unreadable answer (unknown, "parse error")
//	timeout_as_not_reached no answer in time reported as never sent (strands a code that may arrive)
//	retry_once             a refusal is retried: a second code on the member's phone
//	number_guessed         a local number (0803...) turned into +234 and sent
//	key_optional           a sender built with no API key
//	log_request_body       the request body logged (the 2am debugging line): the code and the key with it
//	log_request_error      the error logged by formatting the whole request, body included
//	sender_hardcoded       sent from "N-Alert" whatever is configured (the loan platform's own defect)
//	route_generic          the DND route dropped: most Nigerian numbers never get the code
//	status_4xx_unknown     Termii's 4xx refusal reported as unknown (the member waits for a code that will not come)
//	unreadable_as_refused  an unreadable 2xx reported as refused (strands a code that may arrive)
package tier1

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
	"strings"
	"time"

	"github.com/argon/backend/internal/auth"
)

var e164NG = regexp.MustCompile(`^\+234[0-9]{10}$`)

type refTermii struct {
	cfg auth.TermiiConfig
	brk string
	log *slog.Logger
	hc  *http.Client
}

func newRefTermii(cfg auth.TermiiConfig, brk string) (auth.SMS, error) {
	if cfg.URL == "" || cfg.SenderID == "" || (cfg.APIKey == "" && brk != "key_optional") {
		return nil, errors.New("termii: URL, API key and sender ID are all required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &refTermii{cfg: cfg, brk: brk, log: log, hc: &http.Client{Timeout: cfg.Timeout}}, nil
}

func last4of(p string) string {
	if len(p) <= 4 {
		return "****"
	}
	return "…" + p[len(p)-4:]
}

func (r *refTermii) Send(ctx context.Context, to, text string) (auth.Sent, error) {
	out := auth.Sent{Provider: "termii"}
	if r.brk == "number_guessed" && strings.HasPrefix(to, "0") {
		to = "+234" + to[1:]
	}
	if !e164NG.MatchString(to) {
		out.Outcome, out.ProviderCode = auth.SendRefused, "number_not_e164"
		r.log.ErrorContext(ctx, "sign-in code NOT SENT: the number is not +234 E.164", "outcome", out.Outcome, "to_last4", last4of(to))
		return out, nil
	}
	from, channel := r.cfg.SenderID, "dnd"
	if r.brk == "sender_hardcoded" {
		from = "N-Alert"
	}
	if r.brk == "route_generic" {
		channel = "generic"
	}
	body, _ := json.Marshal(map[string]string{"to": to, "from": from, "sms": text, "type": "plain", "channel": channel, "api_key": r.cfg.APIKey})
	if r.brk == "log_request_body" {
		r.log.DebugContext(ctx, "termii request", "body", string(body))
	}
	attempts := 1
	if r.brk == "retry_once" {
		attempts = 2
	}
	for i := 0; i < attempts; i++ {
		out = r.once(ctx, to, body)
		if out.Outcome == auth.SendAccepted {
			break
		}
	}
	return out, nil
}

func (r *refTermii) once(ctx context.Context, to string, body []byte) auth.Sent {
	out := auth.Sent{Provider: "termii"}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		var ne net.Error
		timeout := errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)
		switch {
		case r.brk == "dead_as_parse" && !timeout:
			out.Outcome = auth.SendUnknown
			r.log.ErrorContext(ctx, "sign-in code send: parse error reading Termii's answer", "outcome", out.Outcome, "to_last4", last4of(to))
			return out
		case timeout && r.brk != "timeout_as_not_reached":
			out.Outcome = auth.SendUnknown
		default:
			out.Outcome = auth.SendNotReached
		}
		if r.brk == "log_request_error" {
			r.log.ErrorContext(ctx, "sign-in code send failed", "request", fmt.Sprintf("%s %s %s", req.Method, req.URL, body), "err", err)
			return out
		}
		r.log.ErrorContext(ctx, "sign-in code send failed", "outcome", out.Outcome, "to_last4", last4of(to), "err", err)
		return out
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
	case resp.StatusCode >= 200 && resp.StatusCode < 300 && r.brk == "status_200_success":
		out.Outcome, out.MessageID = auth.SendAccepted, id
		if id == "" {
			out.MessageID = "unknown"
		}
	case resp.StatusCode >= 500:
		out.Outcome = auth.SendUnknown
	case resp.StatusCode >= 400 && r.brk == "status_4xx_unknown":
		out.Outcome = auth.SendUnknown
	case resp.StatusCode >= 400:
		out.Outcome, out.ProviderCode = auth.SendRefused, ans.Code
		if out.ProviderCode == "" {
			out.ProviderCode = fmt.Sprintf("http_%d", resp.StatusCode)
		}
	case !readable && r.brk == "unreadable_as_refused":
		out.Outcome, out.ProviderCode = auth.SendRefused, "unreadable"
	case !readable:
		out.Outcome = auth.SendUnknown
	case ans.Code != "ok":
		out.Outcome, out.ProviderCode = auth.SendRefused, ans.Code
	case id == "":
		out.Outcome = auth.SendUnknown
	default:
		out.Outcome, out.MessageID = auth.SendAccepted, id
	}
	if out.Outcome == auth.SendAccepted {
		r.log.InfoContext(ctx, "sign-in code accepted by Termii", "outcome", out.Outcome, "message_id", out.MessageID, "to_last4", last4of(to))
	} else {
		r.log.ErrorContext(ctx, "sign-in code not accepted by Termii", "outcome", out.Outcome, "provider_code", out.ProviderCode, "status", resp.StatusCode, "to_last4", last4of(to))
	}
	return out
}
