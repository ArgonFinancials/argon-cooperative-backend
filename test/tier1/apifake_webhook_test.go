//go:build tier1

// THE MUTATION VEHICLE FOR VFD'S WEBHOOK — test code, NOT the API. A
// reference implementation of POST /webhooks/vfd (stage 3, 2026-11-10),
// breakable one named way per run (ARGON_TEST_API_FAKE=<break>):
//
//	webhook_signature_optional  hmac mode, and a request with no signature is kept (the wallet's own defect: checked only if present)
//	webhook_signature_ignored   hmac mode, and a wrong signature is kept
//	webhook_credits             the notification is believed: an inflow written at once, at its own amount
//	webhook_off_open            off, and notifications are kept anyway
//	webhook_no_limit            a body of any size is kept
//	webhook_rewritten           the body is kept re-encoded, not as VFD sent it (the evidence altered)
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/argon/backend/internal/api"
)

func (f *fakeAPI) ReceiveVfdNotification(ctx context.Context, req api.ReceiveVfdNotificationRequestObject) (api.ReceiveVfdNotificationResponseObject, error) {
	switch f.webhook.Mode {
	case "hmac", "unsigned":
	default:
		if f.brk != "webhook_off_open" {
			return api.ReceiveVfdNotification503JSONResponse{Code: "webhook_off", Message: "this server takes no notifications from VFD"}, nil
		}
	}
	raw := api.RawBody(ctx)
	signed := false
	if f.webhook.Mode == "hmac" {
		sig := ""
		if req.Params.XVFDSignature != nil {
			sig = *req.Params.XVFDSignature
		}
		switch {
		case sig == "" && f.brk == "webhook_signature_optional":
		case sig == "":
			return api.ReceiveVfdNotification401JSONResponse{Code: "signature_required", Message: "not kept"}, nil
		case !api.SignatureValid(f.webhook.Secret, raw, sig) && f.brk != "webhook_signature_ignored":
			return api.ReceiveVfdNotification401JSONResponse{Code: "signature_wrong", Message: "not kept"}, nil
		default:
			signed = true
		}
	}
	kept := string(raw)
	if f.brk == "webhook_rewritten" && req.Body != nil {
		b, _ := json.Marshal(*req.Body)
		kept = string(b)
	}
	kind, ref, acct := api.NotificationFields(raw)
	if _, err := f.db.Exec(ctx, `INSERT INTO vfd_notifications (kind, raw, signed, reference, account_number) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''))`,
		kind, kept, signed, ref, acct); err != nil {
		return nil, err
	}
	if f.brk == "webhook_credits" {
		var amount float64
		if req.Body != nil {
			if a, ok := (*req.Body)["amount"].(string); ok {
				json.Unmarshal([]byte(a), &amount)
			}
		}
		f.db.Exec(ctx, `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo)
			SELECT $1, society_id, account_number, $3 FROM vfd_accounts WHERE account_number = $2 AND member_id IS NULL`, ref, acct, int64(amount*100))
	}
	return api.ReceiveVfdNotification200JSONResponse{Kept: true}, nil
}

// keepRawBody: the real server's middleware, or, under webhook_no_limit, one
// that keeps any size.
func (f *fakeAPI) keepRawBody(next http.Handler) http.Handler {
	if f.brk != "webhook_no_limit" {
		return api.KeepRawBody(next)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.WebhookPath {
			next.ServeHTTP(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, r.WithContext(api.WithRawBody(r.Context(), raw)))
	})
}
