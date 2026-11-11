// Package wallet is the system that pays: it sends the transfer the last
// signature authorised, asks VFD what happened, and records VFD's answer.
// Written against test/tier1/tier1_wallet_test.go and its reference,
// walletfake_test.go (stage 3, 2026-11-10). LEDGER_CONTRACT §13.
//
// It runs as argon_wallet, the only role that writes VFD's evidence (0028):
// the database records whatever this package says VFD said, so nothing else
// may say it. The database then does the rest in the outcome's transaction:
// a failure releases the hold, a success posts the payout.
//
// What it never does:
//   - SEND TWICE. A transfer is sent once (0028 refuses a second send). A
//     send whose answer was lost (a timeout, a crash between sending and
//     recording) is recorded as unknown and asked about, never resent.
//   - RELEASE ON A GUESS. Unknown stays unknown and is asked again. Failed is
//     VFD's refusal at the send, a connection never made, or "not found"
//     after the settlement window (0028's vfd_settlement_window, read from the
//     database, not repeated here).
//   - PAY AN OFFICER'S HOLD. Only a hold the last signature placed (§13) is
//     the system's to pay; one an officer authorised by hand is the officers'.
//   - PAY AN ACCOUNT VFD DOES NOT CONFIRM. The member's account is asked
//     about (name enquiry) before the transfer exists; no answer, no transfer.
package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/argon/backend/internal/vfd"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Worker pays and asks. Tick does one round; a caller runs it on a schedule.
type Worker interface {
	Tick(ctx context.Context) (Round, error)
}

// Round: what one tick did, for the log and the tests.
type Round struct {
	Sent, Asked, Paid, Failed, Waiting int
	Inflows                            int // confirmed by VFD and recorded (0029)
	// Rematched: inflows credited from a claim that the claim's or the
	// inflow's own transaction missed (the two committing at once, 0032).
	Rematched int
}

type Config struct {
	DB  *pgxpool.Pool // as argon_wallet
	VFD vfd.Client
	Log *slog.Logger
	// AskEvery: the least time between two questions to VFD about one
	// transfer. Zero: one minute.
	AskEvery time.Duration
	// LostAfter: a transfer with no recorded send this long after it was
	// created was sent by a run that died before recording VFD's answer (or
	// never sent at all). It is recorded as an unknown send, and asked about.
	// Zero: two minutes.
	LostAfter time.Duration
	// Society: one society's holds and transfers only; empty, every society.
	// Production runs one worker for all; the tests run one per world.
	Society string
}

type worker struct {
	cfg Config
	log *slog.Logger
}

// New returns the worker, or refuses without a database or a VFD client.
func New(cfg Config) (Worker, error) {
	if cfg.DB == nil || cfg.VFD == nil {
		return nil, errors.New("wallet: the worker needs its database (as argon_wallet) and the VFD client")
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
	return &worker{cfg: cfg, log: log}, nil
}

func (w *worker) Tick(ctx context.Context) (Round, error) {
	var r Round
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
	// The database matches a claim and its inflow in whichever transaction
	// arrives second; two arriving at once see neither, and this finds them.
	if err := w.cfg.DB.QueryRow(ctx, `SELECT contribution_rematch()`).Scan(&r.Rematched); err != nil {
		return r, fmt.Errorf("wallet: matching claims a race missed: %w", err)
	}
	return r, w.reconcile(ctx)
}

// sendNew: every open hold the last signature placed, with no transfer yet,
// whose member and society both have a VFD account.
func (w *worker) sendNew(ctx context.Context, r *Round) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT h.id::text, h.amount_kobo, s.account_number, coalesce(s.vfd_client_id, ''), coalesce(s.vfd_account_id, ''), coalesce(s.account_name, ''), m.account_number
		  FROM withdrawal_holds h
		  JOIN credential_acts a ON a.id = h.act_id AND a.kind = 'approval_signature'
		  JOIN vfd_accounts m ON m.member_id = h.member_id
		  JOIN vfd_accounts s ON s.society_id = h.society_id AND s.member_id IS NULL
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfers t WHERE t.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id)
		   AND ($1 = '' OR h.society_id::text = $1)
		 ORDER BY h.held_at`, w.cfg.Society)
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

func (w *worker) send(ctx context.Context, r *Round, hold string, amount int64, from vfd.Party, toAcct string) error {
	// The destination, confirmed by VFD before anything exists to send.
	look, err := w.cfg.VFD.NameEnquiry(ctx, toAcct, vfd.IntraBank)
	if err != nil {
		return err
	}
	if look.Outcome != vfd.Accepted {
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
	if sent.Outcome == vfd.Refused || sent.Outcome == vfd.NotReached {
		return w.outcome(ctx, r, transfer, "failed", ev)
	}
	return nil
}

// recordLost: a transfer with no send recorded, older than LostAfter, was
// either sent by a run that died before recording VFD's answer, or never
// sent. Which, nobody knows: it is recorded as an unknown send and asked
// about. Never resent.
func (w *worker) recordLost(ctx context.Context) error {
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
	for _, id := range ids {
		if _, err := w.event(ctx, id, "sent", string(vfd.Unknown), "send_not_recorded", "", "", "", 0, ""); err != nil {
			return err
		}
		w.log.WarnContext(ctx, "a transfer's send was never recorded: recorded as unknown, to be asked about, never resent", "transfer", id)
	}
	return nil
}

// askOpen: every transfer sent and not yet decided, not asked about within
// AskEvery.
func (w *worker) askOpen(ctx context.Context, r *Round) error {
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
		if o.sentResult == string(vfd.Refused) || o.sentResult == string(vfd.NotReached) {
			if err := w.outcome(ctx, r, o.transfer, "failed", o.sentEv); err != nil {
				return err
			}
			continue
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
		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)
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

func (w *worker) event(ctx context.Context, transfer, kind, result, code, txnStatus, txnID, session string, amount int64, at string) (string, error) {
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

func (w *worker) outcome(ctx context.Context, r *Round, transfer, result, evidence string) error {
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
