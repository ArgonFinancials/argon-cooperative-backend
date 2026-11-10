package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// VFD'S NOTIFICATIONS (stage 3, 2026-11-10; docs/VFD_INTEGRATION.md §2,
// requirements 2–4). Kept exactly as received, answered only once durable,
// never believed: the wallet worker confirms each reference with VFD (0029).
//
// How VFD is trusted at the door is said, never inherited (ARGON_VFD_WEBHOOK):
//   hmac      X-VFD-Signature, hex HMAC-SHA512 of the raw body under the
//             webhook secret, REQUIRED: no header, or a wrong one, and the
//             request is refused and not kept. There is no unsigned path.
//   unsigned  VFD does not sign inflow notifications (the wallet platform's
//             own finding): kept, marked unsigned. Either way nothing is
//             credited until VFD itself confirms the reference.
//   off       refused (503).

// Webhook: how this server takes VFD's notifications.
type Webhook struct {
	Mode   string // hmac | unsigned | off
	Secret []byte // hmac only
}

// MaxNotification: a notification is small; anything larger is refused
// before it is read in full.
const MaxNotification = 64 << 10

type rawBodyKey struct{}

// WebhookPath is the one path whose raw body is kept for its handler.
const WebhookPath = "/webhooks/vfd"

// KeepRawBody: for the webhook path, reads the body (at most MaxNotification)
// before anything decodes it, keeps the exact bytes for the handler (a
// signature is over the bytes as sent, and the record is the bytes as sent),
// and gives the decoder the same bytes again. Larger: 413, nothing kept.
func KeepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WebhookPath || r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, MaxNotification+1))
		if err != nil || len(raw) > MaxNotification {
			writeProblem(w, http.StatusRequestEntityTooLarge, Problem{Code: "too_large", Message: "a notification is at most 64 KiB: not kept"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, r.WithContext(WithRawBody(r.Context(), raw)))
	})
}

// WithRawBody: ctx carrying a body as received, for RawBody.
func WithRawBody(ctx context.Context, raw []byte) context.Context {
	return context.WithValue(ctx, rawBodyKey{}, raw)
}

// RawBody: the webhook's body as received (KeepRawBody).
func RawBody(ctx context.Context) []byte {
	b, _ := ctx.Value(rawBodyKey{}).([]byte)
	return b
}

// SignatureValid: hex HMAC-SHA512 of raw under secret, compared in constant time.
func SignatureValid(secret, raw []byte, header string) bool {
	got, err := hex.DecodeString(strings.TrimSpace(header))
	if err != nil || len(secret) == 0 {
		return false
	}
	m := hmac.New(sha512.New, secret)
	m.Write(raw)
	return hmac.Equal(got, m.Sum(nil))
}

// NotificationFields: what is read from a notification to file it (never
// to believe it): its kind, its reference and the account it names. The
// account has three names in VFD's notifications (docs/VFD_INTEGRATION.md §1).
func NotificationFields(raw []byte) (kind, reference, account string) {
	var b map[string]any
	if json.Unmarshal(raw, &b) != nil {
		return "unknown", "", ""
	}
	str := func(k string) string {
		s, _ := b[k].(string)
		return strings.TrimSpace(s)
	}
	reference = str("reference")
	for _, k := range []string{"account_number", "accountNo", "accountNumber"} {
		if account = str(k); account != "" {
			break
		}
	}
	switch {
	case reference != "" && b["amount"] != nil:
		kind = "inflow"
	case b["bvn"] != nil || str("type") == "bvn_consent":
		kind = "bvn_consent"
	default:
		kind = "unknown"
	}
	return
}

func (s *server) ReceiveVfdNotification(ctx context.Context, req ReceiveVfdNotificationRequestObject) (ReceiveVfdNotificationResponseObject, error) {
	switch s.webhook.Mode {
	case "hmac", "unsigned":
	default:
		return ReceiveVfdNotification503JSONResponse{Code: "webhook_off", Message: "this server takes no notifications from VFD"}, nil
	}
	raw := RawBody(ctx)
	signed := false
	if s.webhook.Mode == "hmac" {
		if req.Params.XVFDSignature == nil || *req.Params.XVFDSignature == "" {
			return ReceiveVfdNotification401JSONResponse{Code: "signature_required", Message: "this server takes only signed notifications: not kept"}, nil
		}
		if !SignatureValid(s.webhook.Secret, raw, *req.Params.XVFDSignature) {
			return ReceiveVfdNotification401JSONResponse{Code: "signature_wrong", Message: "the signature does not match the body: not kept"}, nil
		}
		signed = true
	}
	kind, ref, acct := NotificationFields(raw)
	// Durable BEFORE VFD is answered: an error here is a 500, and VFD sends it again.
	if _, err := s.db.Exec(ctx, `INSERT INTO vfd_notifications (kind, raw, signed, reference, account_number) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''))`,
		kind, string(raw), signed, ref, acct); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "VFD notification kept (not believed until VFD confirms it)", "kind", kind, "reference", ref, "signed", signed)
	return ReceiveVfdNotification200JSONResponse{Kept: true}, nil
}
