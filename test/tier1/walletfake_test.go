//go:build tier1

// THE MUTATION VEHICLE FOR THE WALLET WORKER — test code, NOT the worker.
// Generated from internal/wallet/wallet.go (the same code, so it cannot
// drift), with one break per run (ARGON_TEST_WALLET_FAKE=<break>):
//
//	pays_officer_holds  pays a hold an officer authorised by hand (§13: the system pays only the last signature's)
//	no_enquiry          sends without VFD confirming the member's account
//	refused_waits       a send VFD refused or never received is left waiting (the member left with silence)
//	resend_unknown      a send with no answer is sent again (how a payment goes out twice)
//	release_on_unknown  a send with no answer is failed and the hold released (money that may be leaving freed)
//	lost_resent         a send whose answer was lost is sent again
//	lost_ignored        a send whose answer was lost is never asked about (the hold held forever)
//	notfound_fails_now  "not found" fails the transfer at once (settlement lag taken as failure)
//	unreadable_fails    an unreadable requery past the window fails it
//	success_unrecorded  VFD's success is not recorded as paid
//	body_amount_believed an inflow recorded at the notification's amount, not VFD's
//	unconfirmed_believed an inflow VFD does not confirm, recorded anyway
//	unmatched_unsaid    money to no society's account marked confirmed (nobody looks)
//	dup_attempt_unrecorded a second delivery left with no attempt: never settled, asked about forever
//	reconcile_silent    reconciliation records nothing (a missed inflow, and nobody told)
//	reconcile_matched   reconciliation that could not run recorded as a match
//	rematch_skipped     claims a race missed are never matched (the member waits on a payment that arrived)
package tier1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/argon/backend/internal/vfd"
	"github.com/argon/backend/internal/wallet"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func newRefWallet(cfg wallet.Config, brk string) (wallet.Worker, error) {
	if cfg.DB == nil || cfg.VFD == nil {
		return nil, errors.New("wallet: the worker needs its database and the VFD client")
	}
	if cfg.AskEvery <= 0 {
		cfg.AskEvery = time.Minute
	}
	if cfg.LostAfter <= 0 {
		cfg.LostAfter = 2 * time.Minute
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &refWallet{cfg: cfg, log: log, brk: brk}, nil
}

type refWallet struct {
	cfg wallet.Config
	log *slog.Logger
	brk string
}

func (w *refWallet) Tick(ctx context.Context) (wallet.Round, error) {
	var r wallet.Round
	if err := w.sendNew(ctx, &r); err != nil {
		return r, err
	}
	if err := w.recordLost(ctx); err != nil {
		return r, err
	}
	if err := w.askOpen(ctx, &r); err != nil {
		return r, err
	}
	if err := w.confirmInflows(ctx, &r); err != nil {
		return r, err
	}
	if w.brk != "rematch_skipped" {
		if err := w.cfg.DB.QueryRow(ctx, `SELECT contribution_rematch()`).Scan(&r.Rematched); err != nil {
			return r, fmt.Errorf("wallet: matching claims a race missed: %w", err)
		}
	}
	return r, w.reconcile(ctx)
}

// sendNew: every open hold the last signature placed, with no transfer yet,
// whose member and society both have a VFD account.
func (w *refWallet) sendNew(ctx context.Context, r *wallet.Round) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT h.id::text, h.amount_kobo, s.account_number, coalesce(s.vfd_client_id, ''), coalesce(s.vfd_account_id, ''), coalesce(s.account_name, ''), m.account_number
		  FROM withdrawal_holds h
		  JOIN credential_acts a ON a.id = h.act_id AND (a.kind = 'approval_signature' OR $2 = 'pays_officer_holds')
		  JOIN vfd_accounts m ON m.member_id = h.member_id
		  JOIN vfd_accounts s ON s.society_id = h.society_id AND s.member_id IS NULL
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfers t WHERE t.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id)
		   AND ($1 = '' OR h.society_id::text = $1)
		 ORDER BY h.held_at`, w.cfg.Society, w.brk)
	if err != nil {
		return fmt.Errorf("wallet: the holds to pay: %w", err)
	}
	type due struct {
		hold   string
		amount int64
		from   vfd.Party
		to     string
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.hold, &d.amount, &d.from.AccountNo, &d.from.ClientID, &d.from.AccountID, &d.from.Name, &d.to); err != nil {
			rows.Close()
			return err
		}
		d.from.Bank = vfd.IntraBank
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range dues {
		if err := w.send(ctx, r, d.hold, d.amount, d.from, d.to); err != nil {
			return err
		}
	}
	return nil
}

func (w *refWallet) send(ctx context.Context, r *wallet.Round, hold string, amount int64, from vfd.Party, toAcct string) error {
	// The destination, confirmed by VFD before anything exists to send.
	look, err := w.cfg.VFD.NameEnquiry(ctx, toAcct, vfd.IntraBank)
	if err != nil {
		return err
	}
	if w.brk == "no_enquiry" {
		look.Party = vfd.Party{AccountNo: toAcct, Bank: vfd.IntraBank}
	}
	if look.Outcome != vfd.Accepted && w.brk != "no_enquiry" {
		w.log.WarnContext(ctx, "payout waiting: VFD did not confirm the member's account", "hold", hold, "outcome", look.Outcome, "code", look.Code)
		r.Waiting++
		return nil
	}
	var transfer, ref string
	err = w.cfg.DB.QueryRow(ctx, `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo)
		VALUES ($1, vfd_transfer_reference($1), $2, $3, $4) RETURNING id::text, reference`, hold, from.AccountNo, toAcct, amount).Scan(&transfer, &ref)
	if err != nil {
		return fmt.Errorf("wallet: the transfer for hold %s: %w", hold, err)
	}
	sent, err := w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: ref, From: from, To: look.Party, AmountKobo: amount, Remark: "Argon cooperative withdrawal"})
	if err != nil {
		return fmt.Errorf("wallet: sending %s: %w", ref, err)
	}
	r.Sent++
	ev, err := w.event(ctx, transfer, "sent", string(sent.Outcome), sent.Code, "", sent.TxnID, sent.SessionID, 0, "")
	if err != nil {
		return err
	}
	// Refused or never reached: VFD did nothing. Definitive, now.
	if (sent.Outcome == vfd.Refused || sent.Outcome == vfd.NotReached) && w.brk != "refused_waits" {
		return w.outcome(ctx, r, transfer, "failed", ev)
	}
	if sent.Outcome == vfd.Unknown && w.brk == "release_on_unknown" {
		return w.outcome(ctx, r, transfer, "failed", ev)
	}
	return nil
}

// recordLost: a transfer with no send recorded, older than LostAfter, was
// either sent by a run that died before recording VFD's answer, or never
// sent. Which, nobody knows: it is recorded as an unknown send and asked
// about. Never resent.
func (w *refWallet) recordLost(ctx context.Context) error {
	rows, err := w.cfg.DB.Query(ctx, `SELECT t.id::text FROM vfd_transfers t JOIN withdrawal_holds h ON h.id = t.hold_id
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfer_events e WHERE e.transfer_id = t.id)
		   AND t.created_at < now() - make_interval(secs => $1)
		   AND ($2 = '' OR h.society_id::text = $2)`, w.cfg.LostAfter.Seconds(), w.cfg.Society)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if w.brk == "lost_ignored" {
		return nil
	}
	for _, id := range ids {
		if w.brk == "lost_resent" {
			w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: "Argon-resent-" + id, From: vfd.Party{AccountNo: "1000000000"}, To: vfd.Party{AccountNo: "1000000001", Bank: vfd.IntraBank}, AmountKobo: 1})
		}
		if _, err := w.event(ctx, id, "sent", string(vfd.Unknown), "send_not_recorded", "", "", "", 0, ""); err != nil {
			return err
		}
		w.log.WarnContext(ctx, "a transfer's send was never recorded: recorded as unknown, to be asked about, never resent", "transfer", id)
	}
	return nil
}

// askOpen: every transfer sent and not yet decided, not asked about within
// AskEvery.
func (w *refWallet) askOpen(ctx context.Context, r *wallet.Round) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT t.id::text, t.reference, s.id::text, s.result,
		       now() >= s.at + vfd_settlement_window() AS past_window
		  FROM vfd_transfers t
		  JOIN withdrawal_holds h ON h.id = t.hold_id
		  JOIN vfd_transfer_events s ON s.transfer_id = t.id AND s.kind = 'sent'
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o WHERE o.transfer_id = t.id)
		   AND (SELECT max(at) FROM vfd_transfer_events e WHERE e.transfer_id = t.id) < now() - make_interval(secs => $1)
		   AND ($2 = '' OR h.society_id::text = $2)
		 ORDER BY s.at`, w.cfg.AskEvery.Seconds(), w.cfg.Society)
	if err != nil {
		return err
	}
	type open struct {
		transfer, ref, sentEv, sentResult string
		pastWindow                        bool
	}
	var opens []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.transfer, &o.ref, &o.sentEv, &o.sentResult, &o.pastWindow); err != nil {
			rows.Close()
			return err
		}
		opens = append(opens, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, o := range opens {
		// A send VFD refused or never received, not yet decided (a run that
		// died before deciding): decided now, on that answer.
		if o.sentResult == string(vfd.Unknown) && w.brk == "resend_unknown" {
			if _, err := w.event(ctx, o.transfer, "sent", "accepted", "00", "", "", "", 0, ""); err != nil {
				return err
			}
		}
		if (o.sentResult == string(vfd.Refused) || o.sentResult == string(vfd.NotReached)) && w.brk == "refused_waits" {
			r.Waiting++
			continue
		}
		if o.sentResult == string(vfd.Refused) || o.sentResult == string(vfd.NotReached) {
			if err := w.outcome(ctx, r, o.transfer, "failed", o.sentEv); err != nil {
				return err
			}
			continue
		}
		if o.sentResult == string(vfd.Unknown) && w.brk == "resend_unknown" {
			w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: o.ref, From: vfd.Party{AccountNo: "1000000000"}, To: vfd.Party{AccountNo: "1000000001", Bank: vfd.IntraBank}, AmountKobo: 1})
		}
		q, err := w.cfg.VFD.Requery(ctx, o.ref)
		if err != nil {
			return err
		}
		r.Asked++
		ev, err := w.event(ctx, o.transfer, "requeried", string(q.State), q.Code, q.TxnStatus, q.TxnID, q.SessionID, q.AmountKobo, q.At)
		if err != nil {
			return err
		}
		switch {
		case q.State == vfd.Succeeded && w.brk == "success_unrecorded":
			r.Waiting++
		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)
		case q.State == vfd.NotFound && w.brk == "notfound_fails_now":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.StateUnknown && o.pastWindow && w.brk == "unreadable_fails":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.NotFound && o.pastWindow:
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		default:
			r.Waiting++ // pending, unknown, or not found within the window: asked again later
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *refWallet) event(ctx context.Context, transfer, kind, result, code, txnStatus, txnID, session string, amount int64, at string) (string, error) {
	var id string
	var amt any
	if amount > 0 {
		amt = amount
	}
	err := w.cfg.DB.QueryRow(ctx, `INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_status, txn_id, session_id, amount_kobo, vfd_time)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, nullif($9, '')) RETURNING id::text`,
		transfer, kind, result, code, txnStatus, txnID, session, amt, at).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("wallet: recording VFD's answer (%s %s) for transfer %s: %w", kind, result, transfer, err)
	}
	return id, nil
}

func (w *refWallet) outcome(ctx context.Context, r *wallet.Round, transfer, result, evidence string) error {
	if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_transfer_outcomes (transfer_id, result, evidence_id) VALUES ($1, $2, $3)`, transfer, result, evidence); err != nil {
		return fmt.Errorf("wallet: recording the outcome %s of transfer %s: %w", result, transfer, err)
	}
	if result == "paid" {
		r.Paid++
	} else {
		r.Failed++
	}
	w.log.InfoContext(context.Background(), "a transfer decided", "transfer", transfer, "result", result)
	return nil
}

type refInflowNote struct {
	id, raw string
}

// The notification's own fields, read for the reference and the sender's
// details only. Several names for one field are VFD's (docs/VFD_INTEGRATION.md
// §1: account_number, accountNo, accountNumber).
type refInflowBody struct {
	Reference         string `json:"reference"`
	OriginatorName    string `json:"originator_account_name"`
	OriginatorAccount string `json:"originator_account_number"`
	OriginatorBank    string `json:"originator_bank"`
	Narration         string `json:"originator_narration"`
}

func (w *refWallet) confirmInflows(ctx context.Context, r *wallet.Round) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT n.id::text, n.raw FROM vfd_notifications n
		 WHERE n.kind = 'inflow'
		   AND NOT EXISTS (SELECT 1 FROM vfd_notification_attempts a WHERE a.notification_id = n.id AND a.result IN ('confirmed', 'duplicate', 'malformed'))
		   AND coalesce((SELECT max(a.at) FROM vfd_notification_attempts a WHERE a.notification_id = n.id), '-infinity') < now() - make_interval(secs => $1)
		   AND ($2 = '' OR n.account_number IN (SELECT account_number FROM vfd_accounts WHERE society_id::text = $2 AND member_id IS NULL))
		 ORDER BY n.received_at`, w.cfg.AskEvery.Seconds(), w.cfg.Society)
	if err != nil {
		return fmt.Errorf("wallet: the notifications to confirm: %w", err)
	}
	notes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (refInflowNote, error) {
		var n refInflowNote
		return n, row.Scan(&n.id, &n.raw)
	})
	if err != nil {
		return err
	}
	for _, n := range notes {
		if err := w.confirmInflow(ctx, r, n); err != nil {
			return err
		}
	}
	return nil
}

func (w *refWallet) confirmInflow(ctx context.Context, r *wallet.Round, n refInflowNote) error {
	var b refInflowBody
	if json.Unmarshal([]byte(n.raw), &b) != nil || b.Reference == "" {
		return w.attempt(ctx, n.id, "malformed", "the notification names no reference VFD could be asked about")
	}
	var seen bool
	if err := w.cfg.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vfd_inflows WHERE vfd_reference = $1)`, b.Reference).Scan(&seen); err != nil {
		return err
	}
	if seen && w.brk == "dup_attempt_unrecorded" {
		return nil // a duplicate never settled: asked about forever
	}
	if seen {
		return w.attempt(ctx, n.id, "duplicate", "")
	}
	q, err := w.cfg.VFD.Requery(ctx, b.Reference)
	if err != nil {
		return err
	}
	if w.brk == "body_amount_believed" {
		q.AmountKobo = 1000000 // the notification's ₦10,000
	}
	if w.brk == "unconfirmed_believed" && q.State != vfd.Succeeded {
		q.State, q.AmountKobo = vfd.Succeeded, 600000
		w.cfg.DB.QueryRow(ctx, "SELECT coalesce(account_number, '') FROM vfd_notifications WHERE id = $1", n.id).Scan(&q.ToAcct) // the notification believed
	}
	r.Asked++
	if q.State != vfd.Succeeded {
		return w.attempt(ctx, n.id, "not_confirmed", fmt.Sprintf("VFD: %s (status %s, transaction status %s)", q.State, q.Code, q.TxnStatus))
	}
	var society string
	err = w.cfg.DB.QueryRow(ctx, `SELECT society_id::text FROM vfd_accounts WHERE account_number = $1 AND member_id IS NULL`, q.ToAcct).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		if w.brk == "unmatched_unsaid" {
			return w.attempt(ctx, n.id, "confirmed", "")
		}
		return w.attempt(ctx, n.id, "unmatched", fmt.Sprintf("VFD says this money went to account …%s, which is no society's account", refTail(q.ToAcct)))
	}
	if err != nil {
		return err
	}
	_, err = w.cfg.DB.Exec(ctx, `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo, session_id,
		  originator_name, originator_account, originator_bank, narration, vfd_time, notification_id)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), nullif($8, ''), nullif($9, ''), nullif($10, ''), $11)`,
		b.Reference, society, q.ToAcct, q.AmountKobo, q.SessionID, b.OriginatorName, b.OriginatorAccount, b.OriginatorBank, b.Narration, q.At, n.id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.ConstraintName == "vfd_inflow_once" {
		return w.attempt(ctx, n.id, "duplicate", "") // another delivery confirmed it first
	}
	if err != nil {
		return fmt.Errorf("wallet: recording the inflow %s: %w", b.Reference, err)
	}
	r.Inflows++
	return w.attempt(ctx, n.id, "confirmed", "")
}

func refTail(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

func (w *refWallet) attempt(ctx context.Context, notification, result, detail string) error {
	_, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_notification_attempts (notification_id, result, detail) VALUES ($1, $2, nullif($3, ''))`,
		notification, result, detail)
	if err != nil {
		return fmt.Errorf("wallet: recording an attempt (%s) on notification %s: %w", result, notification, err)
	}
	return nil
}

// reconcile: once a day for each society account, ask VFD for the period's
// statement. No statement endpoint is known: the run is recorded as one that
// could not run, saying why, and wallet_exceptions shows it. Never an empty
// statement, which would say nothing was missed.
func (w *refWallet) reconcile(ctx context.Context) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT a.account_number, coalesce((SELECT max(period_to) FROM vfd_reconciliation_runs r WHERE r.account_number = a.account_number), now() - interval '1 day')
		  FROM vfd_accounts a
		 WHERE a.member_id IS NULL AND ($1 = '' OR a.society_id::text = $1)
		   AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs r WHERE r.account_number = a.account_number AND r.ran_at > now() - interval '1 day')`, w.cfg.Society)
	if err != nil {
		return err
	}
	type due struct {
		acct string
		from time.Time
	}
	dues, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (due, error) {
		var d due
		return d, row.Scan(&d.acct, &d.from)
	})
	if err != nil {
		return err
	}
	for _, d := range dues {
		to := time.Now()
		if !d.from.Before(to) {
			continue
		}
		outcome, detail := "could_not_run", ""
		if err := w.cfg.VFD.Statement(ctx, d.acct, d.from, to); err != nil {
			detail = err.Error()
		} else {
			// A statement endpoint answered: comparing it is not built, and an
			// answer not compared is not a match.
			detail = "VFD returned a statement and nothing here compares it yet"
		}
		if w.brk == "reconcile_silent" {
			continue
		}
		if w.brk == "reconcile_matched" {
			outcome, detail = "matched", "nothing missing"
		}
		if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_reconciliation_runs (account_number, period_from, period_to, outcome, detail) VALUES ($1, $2, $3, $4, $5)`,
			d.acct, d.from, to, outcome, detail); err != nil {
			return fmt.Errorf("wallet: recording reconciliation of …%s: %w", refTail(d.acct), err)
		}
	}
	return nil
}
