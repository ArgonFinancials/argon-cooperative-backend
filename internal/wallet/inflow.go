package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/argon/backend/internal/vfd"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MONEY ARRIVING (0029). A notification is never believed: the webhook keeps
// it as received, and this asks VFD about its reference. Only VFD's answer is
// recorded, VFD's account and amount, not the notification's; once per VFD
// reference (0029's unique index), whatever the notification claimed.
// Every attempt is recorded, saying why when it did not confirm.

type inflowNote struct {
	id, raw string
}

// The notification's own fields, read for the reference and the sender's
// details only. Several names for one field are VFD's (docs/VFD_INTEGRATION.md
// §1: account_number, accountNo, accountNumber).
type inflowBody struct {
	Reference         string `json:"reference"`
	OriginatorName    string `json:"originator_account_name"`
	OriginatorAccount string `json:"originator_account_number"`
	OriginatorBank    string `json:"originator_bank"`
	Narration         string `json:"originator_narration"`
}

func (w *worker) confirmInflows(ctx context.Context, r *Round) error {
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
	notes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (inflowNote, error) {
		var n inflowNote
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

func (w *worker) confirmInflow(ctx context.Context, r *Round, n inflowNote) error {
	var b inflowBody
	if json.Unmarshal([]byte(n.raw), &b) != nil || b.Reference == "" {
		return w.attempt(ctx, n.id, "malformed", "the notification names no reference VFD could be asked about")
	}
	var seen bool
	if err := w.cfg.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vfd_inflows WHERE vfd_reference = $1)`, b.Reference).Scan(&seen); err != nil {
		return err
	}
	if seen {
		return w.attempt(ctx, n.id, "duplicate", "")
	}
	q, err := w.cfg.VFD.Requery(ctx, b.Reference)
	if err != nil {
		return err
	}
	r.Asked++
	if q.State != vfd.Succeeded {
		return w.attempt(ctx, n.id, "not_confirmed", fmt.Sprintf("VFD: %s (status %s, transaction status %s)", q.State, q.Code, q.TxnStatus))
	}
	var society string
	err = w.cfg.DB.QueryRow(ctx, `SELECT society_id::text FROM vfd_accounts WHERE account_number = $1 AND member_id IS NULL`, q.ToAcct).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.attempt(ctx, n.id, "unmatched", fmt.Sprintf("VFD says this money went to account …%s, which is no society's account", tail(q.ToAcct)))
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

func tail(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

func (w *worker) attempt(ctx context.Context, notification, result, detail string) error {
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
func (w *worker) reconcile(ctx context.Context) error {
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
		if _, err := w.cfg.DB.Exec(ctx, `INSERT INTO vfd_reconciliation_runs (account_number, period_from, period_to, outcome, detail) VALUES ($1, $2, $3, $4, $5)`,
			d.acct, d.from, to, outcome, detail); err != nil {
			return fmt.Errorf("wallet: recording reconciliation of …%s: %w", tail(d.acct), err)
		}
	}
	return nil
}
