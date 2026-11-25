# Run from the repo root:  python3 test/tier1/genwalletfake.py
# Generates test/tier1/walletfake_test.go from internal/wallet/{wallet,inflow}.go,
# one named break at a time. Every break must apply exactly once, or it stops.
import re

def body(path):
    t = open(path, encoding='utf-8').read()
    i = t.index('\n)\n', t.index('import (')) + 3  # after the import block
    return t[i:]

w = body('internal/wallet/wallet.go')
f = body('internal/wallet/inflow.go')

# Drop the declarations the fake takes from package wallet.
def cut(t, start, end_marker):
    a = t.index(start)
    b = t.index(end_marker, a) + len(end_marker)
    return t[:a] + t[b:]

w = cut(w, '// Worker pays and asks.', '\n}\n')
w = cut(w, '// Round: what one tick did', '\n}\n')
w = cut(w, 'type Config struct {', '\n}\n')
w = cut(w, 'type worker struct {', '\n}\n')
w = cut(w, '// New returns the worker', '\n}\n')

src = w + '\n' + f
src = src.replace('(w *worker)', '(w *refWallet)')
src = re.sub(r'\bRound\b', 'wallet.Round', src)
src = re.sub(r'\binflowNote\b', 'refInflowNote', src)
src = re.sub(r'\binflowBody\b', 'refInflowBody', src)
src = re.sub(r'\btail\(', 'refTail(', src)
src = re.sub(r'\bsweepPages\b', 'refSweepPages', src)
src = re.sub(r'\bsameName\(', 'refSameName(', src)
src = re.sub(r'\bnormName\(', 'refNormName(', src)
src = src.replace('func sameName(', 'func refSameName(').replace('func normName(', 'func refNormName(')
src = src.replace('func refTail(', 'func refTail(')

BREAKS = [
 ('pays_officer_holds', "		  JOIN credential_acts a ON a.id = h.act_id AND a.kind = 'approval_signature'\n",
  "		  JOIN credential_acts a ON a.id = h.act_id AND (a.kind = 'approval_signature' OR $2)\n"),
 ('no_enquiry', '	if look.Outcome != vfd.Accepted {', '	if look.Outcome != vfd.Accepted && w.brk != "no_enquiry" {'),
 ('send_00_waits', '	if sent.Outcome == vfd.Accepted && sent.Code == "00" {', '	if sent.Outcome == vfd.Accepted && sent.Code == "00" && w.brk != "send_00_waits" {'),
 ('refused_released', '''	// §1.6.3: anything else, a refusal included, is a pending check: in 3 of''',
  '''	if sent.Outcome == vfd.Refused && w.brk == "refused_released" {
		return w.outcome(ctx, r, transfer, "failed", ev)
	}
	// §1.6.3: anything else, a refusal included, is a pending check: in 3 of'''),
 ('reply_unkept', '''	if httpStatus > 0 {
		hs = httpStatus
	}''', '''	if httpStatus > 0 {
		hs = httpStatus
	}
	if w.brk == "reply_unkept" {
		hs, reply = nil, ""
	}'''),
 ('schedule_ignored', '''		   AND CASE WHEN pr.at IS NULL THEN now() >= s.at + make_interval(secs => $1)''',
  '''		   AND ($6 OR CASE WHEN pr.at IS NULL THEN now() >= s.at + make_interval(secs => $1)'''),
 ('resend_unknown', '''	for _, o := range opens {
		q, err := w.cfg.VFD.Requery(ctx, o.ref)''', '''	for _, o := range opens {
		if w.brk == "resend_unknown" {
			w.cfg.VFD.Transfer(ctx, vfd.Transfer{Reference: o.ref, From: vfd.Party{AccountNo: "1000000001"}, To: vfd.Party{AccountNo: "1000000002"}, AmountKobo: 1})
		}
		q, err := w.cfg.VFD.Requery(ctx, o.ref)'''),
 ('success_unrecorded', '''		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)''', '''		case q.State == vfd.Succeeded && w.brk == "success_unrecorded":
			r.Waiting++
		case q.State == vfd.Succeeded:
			err = w.outcome(ctx, r, o.transfer, "paid", ev)'''),
 ('notfound_once', '''		case q.State == vfd.NotFound && o.pastWindow && o.one108:''',
  '''		case q.State == vfd.NotFound && o.pastWindow && w.brk == "notfound_once":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.NotFound && w.brk == "notfound_fails_now":
			err = w.outcome(ctx, r, o.transfer, "failed", ev)
		case q.State == vfd.NotFound && o.pastWindow && o.one108:'''),
 ('needs_vfd_silent', '''		case q.State == vfd.Pending:
			// VFD said something else''', '''		case q.State == vfd.Pending && w.brk == "needs_vfd_silent":
			r.Waiting++
		case q.State == vfd.Pending:
			// VFD said something else'''),
 ('lost_ignored', '''func (w *refWallet) recordLost(ctx context.Context) error {''', '''func (w *refWallet) recordLost(ctx context.Context) error {
	if w.brk == "lost_ignored" {
		return nil
	}'''),
 ('outflow_as_inflow', '''	if q.TxnType != "INFLOW" {''', '''	if q.TxnType != "INFLOW" && w.brk != "outflow_as_inflow" && w.brk != "unconfirmed_believed" {'''),
 ('body_amount_believed', '''		b.Reference, society, q.ToAcct, q.AmountKobo,''', '''		b.Reference, society, q.ToAcct, map[bool]int64{true: 1000000, false: q.AmountKobo}[w.brk == "body_amount_believed"],'''),
 ('unconfirmed_believed', '''	if q.State != vfd.Succeeded {
		return w.attempt(ctx, n.id, "not_confirmed"''', '''	if q.State != vfd.Succeeded && w.brk != "unconfirmed_believed" {
		return w.attempt(ctx, n.id, "not_confirmed"'''),
 ('dup_attempt_unrecorded', '''	if seen {
		return w.attempt(ctx, n.id, "duplicate", "")
	}''', '''	if seen {
		if w.brk == "dup_attempt_unrecorded" {
			return nil
		}
		return w.attempt(ctx, n.id, "duplicate", "")
	}'''),
 ('unmatched_unsaid', '''	if errors.Is(err, pgx.ErrNoRows) {
		return w.attempt(ctx, n.id, "unmatched",''', '''	if errors.Is(err, pgx.ErrNoRows) {
		if w.brk == "unmatched_unsaid" {
			return w.attempt(ctx, n.id, "confirmed", "")
		}
		return w.attempt(ctx, n.id, "unmatched",'''),
 ('sweep_silent', '''func (w *refWallet) sweep(ctx context.Context, r *wallet.Round) error {''', '''func (w *refWallet) sweep(ctx context.Context, r *wallet.Round) error {
	if w.brk == "sweep_silent" {
		return nil
	}'''),
 ('sweep_page_one', '''		if len(h.Rows) == 0 {
			break
		}''', '''		if len(h.Rows) == 0 {
			break
		}
		if w.brk == "sweep_page_one" && page > 0 {
			break
		}'''),
 ('sweep_could_not_as_matched', '''		if h.Outcome != vfd.Accepted {
			return "could_not_run",''', '''		if h.Outcome != vfd.Accepted && w.brk == "sweep_could_not_as_matched" {
			return "matched", "nothing missing", found, nil
		}
		if h.Outcome != vfd.Accepted {
			return "could_not_run","''' if False else '''		if h.Outcome != vfd.Accepted && w.brk == "sweep_could_not_as_matched" {
			return "matched", "nothing missing", found, nil
		}
		if h.Outcome != vfd.Accepted {
			return "could_not_run",'''),
 ('sweep_believed', '''			found++
		}''', '''			found++
			if w.brk == "sweep_believed" {
				var society string
				w.cfg.DB.QueryRow(ctx, `SELECT society_id::text FROM vfd_accounts WHERE account_number = $1 AND member_id IS NULL`, row.ToAcct).Scan(&society)
				w.cfg.DB.Exec(ctx, `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo) VALUES ($1, $2, $3, $4)`, row.TxnID, society, row.ToAcct, row.AmountKobo)
			}
		}'''),
 ('bank_unchecked', '''func (w *refWallet) checkBanks(ctx context.Context, r *wallet.Round) error {''', '''func (w *refWallet) checkBanks(ctx context.Context, r *wallet.Round) error {
	if w.brk == "bank_unchecked" {
		return nil
	}'''),
 ('bank_refused_unrecorded', '''		look, err := w.cfg.VFD.NameEnquiry(ctx, u.acct, u.bank)
		if err != nil {
			return err
		}
''', '''		look, err := w.cfg.VFD.NameEnquiry(ctx, u.acct, u.bank)
		if err != nil {
			return err
		}
		if w.brk == "bank_refused_unrecorded" && look.Outcome == vfd.Refused {
			continue
		}
		if w.brk == "bank_silence_as_not_found" && look.Outcome != vfd.Accepted {
			look.Outcome = vfd.Refused
		}
'''),
 ('name_unchecked_at_send', '''	if !refSameName(look.Party.Name, recorded) {''', '''	if !refSameName(look.Party.Name, recorded) && w.brk != "name_unchecked_at_send" && w.brk != "no_enquiry" {'''),
 ('rematch_skipped', '''	if err := w.cfg.DB.QueryRow(ctx, `SELECT contribution_rematch()`).Scan(&r.Rematched); err != nil {''',
  '''	if w.brk == "rematch_skipped" {
		return r, w.sweep(ctx, &r)
	}
	if err := w.cfg.DB.QueryRow(ctx, `SELECT contribution_rematch()`).Scan(&r.Rematched); err != nil {'''),
]
for name, o, n in BREAKS:
    assert src.count(o) == 1, (name, src.count(o), o[:70])
    src = src.replace(o, n)

# Query argument changes the breaks need.
o = '''		 ORDER BY h.held_at`, w.cfg.Society)'''
assert src.count(o) == 1
src = src.replace(o, '''		 ORDER BY h.held_at`, w.cfg.Society, w.brk == "pays_officer_holds")''')
o = '''		   AND ($5 = '' OR h.society_id::text = $5)
		 ORDER BY s.at`, w.cfg.FirstAsk.Seconds(), w.cfg.AskEvery.Seconds(), w.cfg.LateAfter.Seconds(), w.cfg.LateAskEvery.Seconds(), w.cfg.Society)'''
assert src.count(o) == 1
src = src.replace(o, '''		   ) AND ($5 = '' OR h.society_id::text = $5)
		 ORDER BY s.at`, w.cfg.FirstAsk.Seconds(), w.cfg.AskEvery.Seconds(), w.cfg.LateAfter.Seconds(), w.cfg.LateAskEvery.Seconds(), w.cfg.Society, w.brk == "schedule_ignored")''')

hdr = '''//go:build tier1

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
'''
open('test/tier1/walletfake_test.go', 'w', encoding='utf-8', newline='\n').write(hdr + src)
print('ok')
