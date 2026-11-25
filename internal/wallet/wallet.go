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
// docs/VFD_INTEGRATION.md §1.6 (2026-11-24, from 193 transfers looked up at
// VFD), built as written:
//  1. Hold, then send from Argon's POOL with the hold's own reference, and
//     keep every send reply and every requery reply as received.
//  2. VFD's 00 to the send: paid.
//  3. Anything else, a timeout included: pending check, the money held.
//  4. Asked about at FirstAsk (2 minutes), then every AskEvery (10 minutes)
//     for LateAfter (24 hours), then every LateAskEvery (hourly):
//     00 with transactionStatus 00: paid;
//     108 twice in a row, both 30 minutes or more after the send
//     (0039's vfd_settlement_window): failed, the hold released;
//     anything else (91, 25, 99, ...): HELD, and a person is told
//     (wallet_exceptions' transfer_needs_vfd, and an error in the log).
//     Never released by this, never taken as paid.
//  5. One outcome per transfer (0028): a late success never lands on a
//     released payment, nor a release on a paid one.
//  6. Nothing is released without the requery that says 108.
//
// What it never does:
//   - SEND TWICE. A transfer is sent once (0028 refuses a second send). A
//     send whose answer was lost (a timeout, a crash between sending and
//     recording) is recorded as unknown and asked about, never resent.
//   - RELEASE ON A GUESS: only on 108, as above.
//   - PAY AN OFFICER'S HOLD. Only a hold the last signature placed (§13) is
//     the system's to pay; one an officer authorised by hand is the officers'.
//   - PAY AN ACCOUNT VFD DOES NOT CONFIRM. The member's own bank account
//     (0040: their current bank details, its name settled) is asked about
//     (name enquiry) before the transfer exists; no answer, no transfer; a
//     name that is no longer the one recorded, no transfer, and a person looks.
package wallet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	NeedsVFD                           int // held: VFD said something other than 00/00 or 108 (§1.6)
	Inflows                            int // confirmed by VFD and recorded (0029)
	Swept                              int // accounts whose history was read (§3.4)
	Found                              int // deposits found in a history that no notification brought
	// Rematched: inflows credited from a claim that the claim's or the
	// inflow's own transaction missed (the two committing at once, 0032).
	Rematched int
	// Checked: members' bank details VFD answered about (0040: found or not).
	Checked int
}

type Config struct {
	DB  *pgxpool.Pool // as argon_wallet
	VFD vfd.Client
	Log *slog.Logger
	// The requery schedule (§1.6.4): first FirstAsk after the send (zero: 2
	// minutes), then every AskEvery (zero: 10 minutes) until LateAfter after
	// the send (zero: 24 hours), then every LateAskEvery (zero: an hour).
	// AskEvery also paces the asking about an unconfirmed notification.
	FirstAsk, AskEvery, LateAfter, LateAskEvery time.Duration
	// SweepEvery: how often each society account's history is read for
	// deposits no notification brought (§3.4). Zero: 15 minutes.
	SweepEvery time.Duration
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
	if cfg.FirstAsk <= 0 {
		cfg.FirstAsk = 2 * time.Minute
	}
	if cfg.AskEvery <= 0 {
		cfg.AskEvery = 10 * time.Minute
	}
	if cfg.LateAfter <= 0 {
		cfg.LateAfter = 24 * time.Hour
	}
	if cfg.LateAskEvery <= 0 {
		cfg.LateAskEvery = time.Hour
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = 15 * time.Minute
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
	if err := w.checkBanks(ctx, &r); err != nil {
		return r, err
	}
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
	return r, w.sweep(ctx, &r)
}

// pool: Argon's VFD pool (§1.1), as recorded, or recorded now from VFD's
// enquiry. None: nothing is sent this round.
func (w *worker) pool(ctx context.Context) (vfd.Party, bool, error) {
	var p vfd.Party
	err := w.cfg.DB.QueryRow(ctx, `SELECT account_number, account_id, client_id, client_name FROM vfd_pool`).Scan(&p.AccountNo, &p.AccountID, &p.ClientID, &p.Name)
	if err == nil {
		p.Bank = vfd.IntraBank
		return p, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return p, false, err
	}
	got, err := w.cfg.VFD.Pool(ctx)
	if err != nil {
		return p, false, err
	}
	if got.Outcome != vfd.Accepted {
		w.log.ErrorContext(ctx, "no transfer can go out: VFD did not name Argon's pool account", "outcome", got.Outcome, "code", got.Code)
		return p, false, nil
	}
	if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_pool (account_number, account_id, client_id, client_name) VALUES ($1, $2, $3, $4)`,
		got.Party.AccountNo, got.Party.AccountID, got.Party.ClientID, got.Party.Name); err != nil {
		return p, false, fmt.Errorf("wallet: recording the pool: %w", err)
	}
	return got.Party, true, nil
}

// sendNew: every open hold the last signature placed, with no transfer yet,
// whose member's bank details are recorded and their name settled (0040);
// sent from the pool to that bank account.
// checkBanks: VFD's name for every member's current bank details not yet
// asked about (0040: the name check is the guard). VFD's answer is recorded
// as it came, by this role alone; whether the name resembles the member's on
// the register is the database's to say. No answer: nothing recorded, and
// asked again next round.
func (w *worker) checkBanks(ctx context.Context, r *Round) error {
	rows, err := w.cfg.DB.Query(ctx, `SELECT id::text, account_number, bank_code FROM member_bank_current
		WHERE NOT checked AND ($1 = '' OR society_id::text = $1) ORDER BY recorded_at`, w.cfg.Society)
	if err != nil {
		return fmt.Errorf("wallet: the bank details to check: %w", err)
	}
	type unchecked struct{ id, acct, bank string }
	var todo []unchecked
	for rows.Next() {
		var u unchecked
		if err := rows.Scan(&u.id, &u.acct, &u.bank); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range todo {
		look, err := w.cfg.VFD.NameEnquiry(ctx, u.acct, u.bank)
		if err != nil {
			return err
		}
		switch look.Outcome {
		case vfd.Accepted:
			_, err = w.cfg.DB.Exec(ctx, `INSERT INTO bank_name_checks (bank_account_id, found, vfd_code, vfd_name) VALUES ($1, true, $2, $3)`,
				u.id, look.Code, look.Party.Name)
		case vfd.Refused:
			_, err = w.cfg.DB.Exec(ctx, `INSERT INTO bank_name_checks (bank_account_id, found, vfd_code) VALUES ($1, false, $2)`, u.id, look.Code)
		default:
			w.log.WarnContext(ctx, "bank details not checked: VFD did not answer; asked again next round", "outcome", look.Outcome, "code", look.Code)
			continue
		}
		if err != nil {
			return fmt.Errorf("wallet: recording VFD's answer on bank details %s: %w", u.id, err)
		}
		r.Checked++
	}
	return nil
}

func (w *worker) sendNew(ctx context.Context, r *Round) error {
	from, ok, err := w.pool(ctx)
	if err != nil || !ok {
		return err
	}
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT h.id::text, h.amount_kobo, b.account_number, b.bank_code, b.vfd_name
		  FROM withdrawal_holds h
		  JOIN credential_acts a ON a.id = h.act_id AND a.kind = 'approval_signature'
		  JOIN member_bank_current b ON b.member_id = h.member_id AND b.name_settled
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
		bank   string
		name   string // VFD's name for the account when the member recorded it
	}
	var dues []due
	for rows.Next() {
		d := due{from: from}
		if err := rows.Scan(&d.hold, &d.amount, &d.to, &d.bank, &d.name); err != nil {
			rows.Close()
			return err
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range dues {
		if err := w.send(ctx, r, d.hold, d.amount, d.from, d.to, d.bank, d.name); err != nil {
			return err
		}
	}
	return nil
}

func (w *worker) send(ctx context.Context, r *Round, hold string, amount int64, from vfd.Party, toAcct, toBank, recorded string) error {
	// The destination, confirmed by VFD before anything exists to send.
	look, err := w.cfg.VFD.NameEnquiry(ctx, toAcct, toBank)
	if err != nil {
		return err
	}
	if look.Outcome != vfd.Accepted {
		w.log.WarnContext(ctx, "payout waiting: VFD did not confirm the member's account", "hold", hold, "outcome", look.Outcome, "code", look.Code)
		r.Waiting++
		return nil
	}
	// The name VFD gives now is the one recorded with the details (0040: the
	// name check is the guard). Another name: nothing sent, a person looks.
	if !sameName(look.Party.Name, recorded) {
		w.log.ErrorContext(ctx, "payout waiting: the bank's name for the member's account is not the one recorded with it; a person must look", "hold", hold)
		r.Waiting++
		return nil
	}
	var transfer, ref string
	err = w.cfg.DB.QueryRow(ctx, `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo, to_bank)
		VALUES ($1, vfd_transfer_reference($1), $2, $3, $4, $5) RETURNING id::text, reference`, hold, from.AccountNo, toAcct, amount, toBank).Scan(&transfer, &ref)
	if err != nil {
		return fmt.Errorf("wallet: the transfer for hold %s: %w", hold, err)
	}
	sent, err := w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: ref, From: from, To: look.Party, AmountKobo: amount, Remark: "Argon cooperative withdrawal"})
	if err != nil {
		return fmt.Errorf("wallet: sending %s: %w", ref, err)
	}
	r.Sent++
	ev, err := w.event(ctx, transfer, "sent", string(sent.Outcome), sent.Code, "", sent.TxnID, sent.SessionID, 0, "", sent.HTTPStatus, sent.Reply)
	if err != nil {
		return err
	}
	// §1.6.2: VFD's 00 to the send is the payment (161 of 161 were paid).
	if sent.Outcome == vfd.Accepted && sent.Code == "00" {
		return w.outcome(ctx, r, transfer, "paid", ev)
	}
	// §1.6.3: anything else, a refusal included, is a pending check: in 3 of
	// 32 refused sends VFD had already created the transfer and sent it.
	r.Waiting++
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
		if _, err := w.event(ctx, id, "sent", string(vfd.Unknown), "send_not_recorded", "", "", "", 0, "", 0, ""); err != nil {
			return err
		}
		w.log.WarnContext(ctx, "a transfer's send was never recorded: recorded as unknown, to be asked about, never resent", "transfer", id)
	}
	return nil
}

// askOpen: every transfer sent and not yet decided whose next question is
// due by §1.6.4's schedule.
func (w *worker) askOpen(ctx context.Context, r *Round) error {
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT t.id::text, t.reference,
		       now() >= s.at + vfd_settlement_window() AS past_window,
		       coalesce(pr.result = 'not_found' AND pr.code = '108' AND pr.at >= s.at + vfd_settlement_window(), false) AS one_108
		  FROM vfd_transfers t
		  JOIN withdrawal_holds h ON h.id = t.hold_id
		  JOIN vfd_transfer_events s ON s.transfer_id = t.id AND s.kind = 'sent'
		  LEFT JOIN LATERAL (SELECT e.result, e.code, e.at FROM vfd_transfer_events e WHERE e.transfer_id = t.id AND e.kind = 'requeried'
		                      ORDER BY e.at DESC, e.id::text DESC LIMIT 1) pr ON true
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfer_outcomes o WHERE o.transfer_id = t.id)
		   AND CASE WHEN pr.at IS NULL THEN now() >= s.at + make_interval(secs => $1)
		            WHEN now() < s.at + make_interval(secs => $3) THEN now() >= pr.at + make_interval(secs => $2)
		            ELSE now() >= pr.at + make_interval(secs => $4) END
		   AND ($5 = '' OR h.society_id::text = $5)
		 ORDER BY s.at`, w.cfg.FirstAsk.Seconds(), w.cfg.AskEvery.Seconds(), w.cfg.LateAfter.Seconds(), w.cfg.LateAskEvery.Seconds(), w.cfg.Society)
	if err != nil {
		return err
	}
	type open struct {
		transfer, ref      string
		pastWindow, one108 bool
	}
	var opens []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.transfer, &o.ref, &o.pastWindow, &o.one108); err != nil {
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
		q, err := w.cfg.VFD.Requery(ctx, o.ref)
		if err != nil {
			return err
		}
		r.Asked++
		ev, err := w.event(ctx, o.transfer, "requeried", string(q.State), q.Code, q.TxnStatus, q.TxnID, q.SessionID, q.AmountKobo, q.At, q.HTTPStatus, q.Reply)
		if err != nil {
			return err
		}
		switch {
		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)
		case q.State == vfd.NotFound && o.pastWindow && o.one108:
			// 108 twice in a row, both past the window (§1.6.4): released.
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.Pending:
			// VFD said something else (91, 25, 99, ...): it may have created and
			// sent this transfer. Held; never released here; a person is told.
			r.NeedsVFD++
			w.log.ErrorContext(ctx, "a transfer needs VFD: held until VFD confirms a reversal, never released automatically",
				"transfer", o.transfer, "reference", o.ref, "status", q.Code, "transaction_status", q.TxnStatus)
		default:
			r.Waiting++ // unknown, or 108 not yet twice past the window: asked again
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// event: what VFD said, with its reply as received (§1.6.1: every send reply
// and every requery reply kept).
func (w *worker) event(ctx context.Context, transfer, kind, result, code, txnStatus, txnID, session string, amount int64, at string, httpStatus int, reply string) (string, error) {
	var id string
	var amt, hs any
	if amount > 0 {
		amt = amount
	}
	if httpStatus > 0 {
		hs = httpStatus
	}
	err := w.cfg.DB.QueryRow(ctx, `INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_status, txn_id, session_id, amount_kobo, vfd_time, http_status, reply)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, nullif($9, ''), $10, nullif($11, '')) RETURNING id::text`,
		transfer, kind, result, code, txnStatus, txnID, session, amt, at, hs, reply).Scan(&id)
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

// sameName: two names from VFD for one account, the same once case, spacing
// and punctuation are set aside.
func sameName(a, b string) bool { return normName(a) == normName(b) }

func normName(s string) string {
	var out []rune
	space := false
	for _, r := range strings.ToUpper(s) {
		switch {
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			if space && len(out) > 0 {
				out = append(out, ' ')
			}
			out, space = append(out, r), false
		default:
			space = true
		}
	}
	return string(out)
}
