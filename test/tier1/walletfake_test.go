//go:build tier1

// THE MUTATION VEHICLE FOR THE WALLET WORKER — test code, NOT the worker.
// GENERATED from internal/wallet/wallet.go and inflow.go (the same code, so it
// cannot drift), with one break per run (ARGON_TEST_WALLET_FAKE=<break>):
//
//	pays_officer_holds          pays a hold an officer authorised by hand
//	no_enquiry                  sends without VFD confirming the member's account
//	send_00_waits               VFD's 00 to the send not taken as the payment
//	refused_released            a refused send released at once (VFD had sent 3 of 32 of those)
//	reply_unkept                VFD's replies not kept
//	schedule_ignored            asked about on every tick, not on §1.6's schedule
//	resend_unknown              a send with no answer sent again (how a payment goes out twice)
//	success_unrecorded          VFD's 00/00 not recorded as paid
//	notfound_once               released on one 108 past the window
//	notfound_fails_now          released on 108 moments after the send
//	needs_vfd_silent            91, 25, 99 held and told to nobody
//	lost_ignored                a send whose answer was lost never asked about
//	outflow_as_inflow           money going out taken as a deposit
//	unconfirmed_believed        a deposit VFD does not confirm, recorded anyway
//	body_amount_believed        a deposit recorded at the notification's amount, not VFD's
//	dup_attempt_unrecorded      a second delivery left with no attempt
//	unmatched_unsaid            money to no society's account marked confirmed
//	sweep_silent                no history read, a missed deposit never found
//	sweep_page_one              only the history's first page read
//	sweep_could_not_as_matched  a history VFD did not give recorded as nothing missing
//	sweep_believed              a deposit in the history credited without VFD's requery
//	rematch_skipped             claims a race missed never matched
//	bank_unchecked              a member's bank details never checked with VFD (0040)
//	bank_refused_unrecorded     VFD's no-such-account not recorded
//	bank_silence_as_not_found   VFD's silence recorded as no such account
//	name_unchecked_at_send      paid to a name that is not the one recorded
package tier1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	return &refWallet{cfg: cfg, log: log, brk: brk}, nil
}

type refWallet struct {
	cfg wallet.Config
	log *slog.Logger
	brk string
}






func (w *refWallet) Tick(ctx context.Context) (wallet.Round, error) {
	var r wallet.Round
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
	if w.brk == "rematch_skipped" {
		return r, w.sweep(ctx, &r)
	}
	if err := w.cfg.DB.QueryRow(ctx, `SELECT contribution_rematch()`).Scan(&r.Rematched); err != nil {
		return r, fmt.Errorf("wallet: matching claims a race missed: %w", err)
	}
	return r, w.sweep(ctx, &r)
}

// pool: Argon's VFD pool (§1.1), as recorded, or recorded now from VFD's
// enquiry. None: nothing is sent this round.
func (w *refWallet) pool(ctx context.Context) (vfd.Party, bool, error) {
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
func (w *refWallet) checkBanks(ctx context.Context, r *wallet.Round) error {
	if w.brk == "bank_unchecked" {
		return nil
	}
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
		if w.brk == "bank_refused_unrecorded" && look.Outcome == vfd.Refused {
			continue
		}
		if w.brk == "bank_silence_as_not_found" && look.Outcome != vfd.Accepted {
			look.Outcome = vfd.Refused
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

func (w *refWallet) sendNew(ctx context.Context, r *wallet.Round) error {
	from, ok, err := w.pool(ctx)
	if err != nil || !ok {
		return err
	}
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT h.id::text, h.amount_kobo, b.account_number, b.bank_code, b.vfd_name
		  FROM withdrawal_holds h
		  JOIN credential_acts a ON a.id = h.act_id AND (a.kind = 'approval_signature' OR $2)
		  JOIN member_bank_current b ON b.member_id = h.member_id AND b.name_settled
		 WHERE NOT EXISTS (SELECT 1 FROM vfd_transfers t WHERE t.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM withdrawal_hold_releases rl WHERE rl.hold_id = h.id)
		   AND NOT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.hold_id = h.id)
		   AND ($1 = '' OR h.society_id::text = $1)
		 ORDER BY h.held_at`, w.cfg.Society, w.brk == "pays_officer_holds")
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

func (w *refWallet) send(ctx context.Context, r *wallet.Round, hold string, amount int64, from vfd.Party, toAcct, toBank, recorded string) error {
	// The destination, confirmed by VFD before anything exists to send.
	look, err := w.cfg.VFD.NameEnquiry(ctx, toAcct, toBank)
	if err != nil {
		return err
	}
	if look.Outcome != vfd.Accepted && w.brk != "no_enquiry" {
		w.log.WarnContext(ctx, "payout waiting: VFD did not confirm the member's account", "hold", hold, "outcome", look.Outcome, "code", look.Code)
		r.Waiting++
		return nil
	}
	// The name VFD gives now is the one recorded with the details (0040: the
	// name check is the guard). Another name: nothing sent, a person looks.
	if !refSameName(look.Party.Name, recorded) && w.brk != "name_unchecked_at_send" && w.brk != "no_enquiry" {
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
	if sent.Outcome == vfd.Accepted && sent.Code == "00" && w.brk != "send_00_waits" {
		return w.outcome(ctx, r, transfer, "paid", ev)
	}
	if sent.Outcome == vfd.Refused && w.brk == "refused_released" {
		return w.outcome(ctx, r, transfer, "failed", ev)
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
func (w *refWallet) recordLost(ctx context.Context) error {
	if w.brk == "lost_ignored" {
		return nil
	}
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
func (w *refWallet) askOpen(ctx context.Context, r *wallet.Round) error {
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
		   AND ($6 OR CASE WHEN pr.at IS NULL THEN now() >= s.at + make_interval(secs => $1)
		            WHEN now() < s.at + make_interval(secs => $3) THEN now() >= pr.at + make_interval(secs => $2)
		            ELSE now() >= pr.at + make_interval(secs => $4) END
		   ) AND ($5 = '' OR h.society_id::text = $5)
		 ORDER BY s.at`, w.cfg.FirstAsk.Seconds(), w.cfg.AskEvery.Seconds(), w.cfg.LateAfter.Seconds(), w.cfg.LateAskEvery.Seconds(), w.cfg.Society, w.brk == "schedule_ignored")
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
		if w.brk == "resend_unknown" {
			w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: o.ref, From: vfd.Party{AccountNo: "1000000001"}, To: vfd.Party{AccountNo: "1000000002"}, AmountKobo: 1})
		}
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
		case q.State == vfd.Succeeded && w.brk == "success_unrecorded":
			r.Waiting++
		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)
		case q.State == vfd.NotFound && o.pastWindow && w.brk == "notfound_once":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.NotFound && w.brk == "notfound_fails_now":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.NotFound && o.pastWindow && o.one108:
			// 108 twice in a row, both past the window (§1.6.4): released.
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.Pending && w.brk == "needs_vfd_silent":
			r.Waiting++
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
func (w *refWallet) event(ctx context.Context, transfer, kind, result, code, txnStatus, txnID, session string, amount int64, at string, httpStatus int, reply string) (string, error) {
	var id string
	var amt, hs any
	if amount > 0 {
		amt = amount
	}
	if httpStatus > 0 {
		hs = httpStatus
	}
	if w.brk == "reply_unkept" {
		hs, reply = nil, ""
	}
	err := w.cfg.DB.QueryRow(ctx, `INSERT INTO vfd_transfer_events (transfer_id, kind, result, code, txn_status, txn_id, session_id, amount_kobo, vfd_time, http_status, reply)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, nullif($9, ''), $10, nullif($11, '')) RETURNING id::text`,
		transfer, kind, result, code, txnStatus, txnID, session, amt, at, hs, reply).Scan(&id)
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

// sameName: two names from VFD for one account, the same once case, spacing
// and punctuation are set aside.
func refSameName(a, b string) bool { return refNormName(a) == refNormName(b) }

func refNormName(s string) string {
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


// MONEY ARRIVING (0029). A notification is never believed: the webhook keeps
// it as received, and this asks VFD about its reference. Only VFD's answer is
// recorded, VFD's account and amount, not the notification's; once per VFD
// reference (0029's unique index), whatever the notification claimed.
// Every attempt is recorded, saying why when it did not confirm.

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
	if seen {
		if w.brk == "dup_attempt_unrecorded" {
			return nil
		}
		return w.attempt(ctx, n.id, "duplicate", "")
	}
	q, err := w.cfg.VFD.Requery(ctx, b.Reference)
	if err != nil {
		return err
	}
	r.Asked++
	if q.State != vfd.Succeeded && w.brk != "unconfirmed_believed" {
		return w.attempt(ctx, n.id, "not_confirmed", fmt.Sprintf("VFD: %s (status %s, transaction status %s)", q.State, q.Code, q.TxnStatus))
	}
	// Money IN (§3.2): VFD says INFLOW. A reference that names money going out
	// is no deposit, whatever a notification said.
	if q.TxnType != "INFLOW" && w.brk != "outflow_as_inflow" && w.brk != "unconfirmed_believed" {
		return w.attempt(ctx, n.id, "not_confirmed", fmt.Sprintf("VFD says this reference is %q, not money arriving (INFLOW)", q.TxnType))
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
		b.Reference, society, q.ToAcct, map[bool]int64{true: 1000000, false: q.AmountKobo}[w.brk == "body_amount_believed"], q.SessionID, b.OriginatorName, b.OriginatorAccount, b.OriginatorBank, b.Narration, q.At, n.id)
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

// sweep (§3.4, §4): VFD stopped sending notifications for days at a time,
// four times, and never re-sent them. So each society account's history is
// read on a schedule (SweepEvery), page by page, over the period since the
// last sweep (and a day before it: overlap costs nothing, a reference is
// recorded once). Every INFLOW no notification brought is kept as a
// notification FROM THE HISTORY, and confirmed by the same requery as any
// other: the history is never believed either. Each sweep is recorded:
// matched, missing_found (saying how many), or could_not_run (saying why).
func (w *refWallet) sweep(ctx context.Context, r *wallet.Round) error {
	if w.brk == "sweep_silent" {
		return nil
	}
	rows, err := w.cfg.DB.Query(ctx, `
		SELECT a.account_number,
		       coalesce((SELECT max(period_to) FROM vfd_reconciliation_runs x WHERE x.account_number = a.account_number AND x.outcome <> 'could_not_run'), a.recorded_at) - interval '1 day'
		  FROM vfd_accounts a
		 WHERE a.member_id IS NULL AND ($1 = '' OR a.society_id::text = $1)
		   AND NOT EXISTS (SELECT 1 FROM vfd_reconciliation_runs x WHERE x.account_number = a.account_number AND x.ran_at > now() - make_interval(secs => $2))`,
		w.cfg.Society, w.cfg.SweepEvery.Seconds())
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
		outcome, detail, found, err := w.sweepAccount(ctx, d.acct, d.from, to)
		if err != nil {
			return err
		}
		r.Swept++
		r.Found += found
		if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_reconciliation_runs (account_number, period_from, period_to, outcome, detail) VALUES ($1, $2, $3, $4, $5)`,
			d.acct, d.from, to, outcome, detail); err != nil {
			return fmt.Errorf("wallet: recording the sweep of …%s: %w", refTail(d.acct), err)
		}
	}
	return nil
}

// refSweepPages: the most pages read in one sweep of one account (20 rows each).
const refSweepPages = 200

func (w *refWallet) sweepAccount(ctx context.Context, acct string, from, to time.Time) (outcome, detail string, found int, err error) {
	for page := 0; ; page++ {
		if page == refSweepPages {
			return "could_not_run", fmt.Sprintf("more than %d pages of history: the sweep stopped rather than read part of it", refSweepPages), found, nil
		}
		h, err := w.cfg.VFD.History(ctx, acct, from, to, page)
		if err != nil {
			return "", "", found, err
		}
		if h.Outcome != vfd.Accepted && w.brk == "sweep_could_not_as_matched" {
			return "matched", "nothing missing", found, nil
		}
		if h.Outcome != vfd.Accepted {
			return "could_not_run", fmt.Sprintf("VFD did not give the account's history (page %d: %s, status %s)", page, h.Outcome, h.Code), found, nil
		}
		if len(h.Rows) == 0 {
			break
		}
		if w.brk == "sweep_page_one" && page > 0 {
			break
		}
		for _, row := range h.Rows {
			if row.Type != "INFLOW" || row.TxnID == "" {
				continue
			}
			var known bool
			if err := w.cfg.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vfd_inflows WHERE vfd_reference = $1)
				OR EXISTS (SELECT 1 FROM vfd_notifications WHERE reference = $1)`, row.TxnID).Scan(&known); err != nil {
				return "", "", found, err
			}
			if known {
				continue
			}
			raw, _ := json.Marshal(map[string]string{"reference": row.TxnID, "account_number": row.ToAcct, "session_id": row.SessionID,
				"originator_account_number": row.FromAcct, "timestamp": row.Time, "source": "history"})
			if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_notifications (kind, raw, signed, reference, account_number, source) VALUES ('inflow', $1, false, $2, $3, 'history')`,
				string(raw), row.TxnID, row.ToAcct); err != nil {
				return "", "", found, fmt.Errorf("wallet: keeping a deposit found in …%s's history: %w", refTail(acct), err)
			}
			found++
			if w.brk == "sweep_believed" {
				var society string
				w.cfg.DB.QueryRow(ctx, `SELECT society_id::text FROM vfd_accounts WHERE account_number = $1 AND member_id IS NULL`, row.ToAcct).Scan(&society)
				w.cfg.DB.Exec(ctx, `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo) VALUES ($1, $2, $3, $4)`, row.TxnID, society, row.ToAcct, row.AmountKobo)
			}
		}
	}
	if found == 0 {
		return "matched", "VFD's history for the period holds no deposit that a notification did not bring", 0, nil
	}
	w.log.WarnContext(ctx, "deposits found in an account's history that no notification brought: kept, to be confirmed with VFD", "account_last4", refTail(acct), "found", found)
	return "missing_found", fmt.Sprintf("%d deposit(s) in VFD's history that no notification brought: kept as notifications from the history, confirmed with VFD like any other", found), found, nil
}
