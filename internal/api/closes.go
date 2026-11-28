package api

// PERIOD CLOSE OVER HTTP (0044, stage 4, 2026-11-27; LEDGER_CONTRACT §5).
//
//   POST /societies/{s}/period-closes: one officer proposes closing the books
//   through a date, on their own PIN. GET the same path: where the books are
//   closed, and every close, newest first.
//   POST /period-closes/{c}/decisions: a DIFFERENT officer confirms (closed
//   now, the trial balance kept) or rejects, saying why.
//
// The database holds every rule; each refusal is mapped here.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const closeSelect = `SELECT c.id::text, c.through, c.proposed_by::text, pu.full_name, c.at,
	  k.confirmed, k.decided_by::text, du.full_name, k.at, k.reason, k.trial_balance
	FROM period_closes c JOIN users pu ON pu.id = c.proposed_by
	LEFT JOIN period_close_decisions k ON k.close_id = c.id
	LEFT JOIN users du ON du.id = k.decided_by`

func (s *server) periodCloses(ctx context.Context, where string, args ...any) ([]PeriodClose, error) {
	rows, err := s.db.Query(ctx, closeSelect+` WHERE `+where+` ORDER BY c.at DESC, c.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PeriodClose{}
	for rows.Next() {
		var p PeriodClose
		var id, by string
		var through time.Time
		var confirmed *bool
		var decidedBy, decidedName, reason *string
		var decidedAt *time.Time
		var tb []byte
		if err := rows.Scan(&id, &through, &by, &p.ProposedBy.Name, &p.ProposedAt, &confirmed, &decidedBy, &decidedName, &decidedAt, &reason, &tb); err != nil {
			return nil, err
		}
		p.CloseId, p.Through, p.ProposedBy.UserId, p.State = uuidOf(id), openapi_types.Date{Time: through}, uuidOf(by), PeriodCloseStateWaiting
		if confirmed != nil {
			p.Decision = &struct {
				At           time.Time        `json:"at"`
				By           EmailSetter      `json:"by"`
				Confirmed    bool             `json:"confirmed"`
				Reason       *string          `json:"reason"`
				TrialBalance *[]ClosedBalance `json:"trialBalance"`
			}{At: *decidedAt, By: EmailSetter{UserId: uuidOf(deref(decidedBy)), Name: deref(decidedName)}, Confirmed: *confirmed, Reason: reason}
			p.State = PeriodCloseStateRejected
			if *confirmed {
				p.State = PeriodCloseStateConfirmed
			}
			if len(tb) > 0 {
				var kept []struct {
					Code    string `json:"code"`
					Name    string `json:"name"`
					Kind    string `json:"kind"`
					Balance int64  `json:"balance_kobo"`
				}
				if err := json.Unmarshal(tb, &kept); err != nil {
					return nil, err
				}
				bs := make([]ClosedBalance, 0, len(kept))
				for _, b := range kept {
					bs = append(bs, ClosedBalance{Code: b.Code, Name: b.Name, Kind: b.Kind, BalanceKobo: Kobo(strconv.FormatInt(b.Balance, 10))})
				}
				p.Decision.TrialBalance = &bs
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *server) onePeriodClose(ctx context.Context, id string) (PeriodClose, error) {
	ps, err := s.periodCloses(ctx, `c.id = $1`, id)
	if err != nil {
		return PeriodClose{}, err
	}
	if len(ps) != 1 {
		return PeriodClose{}, errors.New("api: the close just written was not found")
	}
	return ps[0], nil
}

func (s *server) ProposePeriodClose(ctx context.Context, req ProposePeriodCloseRequestObject) (ProposePeriodCloseResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ProposePeriodClose404JSONResponse(notFound("society")), nil
	}
	b := req.Body
	if b == nil {
		return ProposePeriodClose400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var id string
	err := s.onStepUp(ctx, "period_close", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO period_closes (society_id, through, proposed_by, proposed_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			society, b.Through.Time.Format("2006-01-02"), callerOf(ctx), act).Scan(&id)
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "period_close_act":
			return ProposePeriodClose403JSONResponse(stepUpProblem("period_close")), nil
		case c == "period_close_by_officer":
			return ProposePeriodClose403JSONResponse(disputeNotOfficer), nil
		case c == "period_close_not_future":
			return ProposePeriodClose422JSONResponse{Code: "not_future", Message: dbMessage(err)}, nil
		case c == "period_close_forward":
			return ProposePeriodClose409JSONResponse{Code: "not_forward", Message: dbMessage(err)}, nil
		case c == "period_close_waits":
			return ProposePeriodClose409JSONResponse{Code: "already_waiting", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	out, err := s.onePeriodClose(ctx, id)
	if err != nil {
		return nil, err
	}
	return ProposePeriodClose201JSONResponse(out), nil
}

func (s *server) ListPeriodCloses(ctx context.Context, req ListPeriodClosesRequestObject) (ListPeriodClosesResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListPeriodCloses404JSONResponse(notFound("society")), nil
	}
	var closed *time.Time
	if err := s.db.QueryRow(ctx, `SELECT society_closed_through($1)`, society).Scan(&closed); err != nil {
		return nil, err
	}
	items, err := s.periodCloses(ctx, `c.society_id = $1`, society)
	if err != nil {
		return nil, err
	}
	return ListPeriodCloses200JSONResponse{ClosedThrough: dateOf(closed), Items: items}, nil
}

func (s *server) DecidePeriodClose(ctx context.Context, req DecidePeriodCloseRequestObject) (DecidePeriodCloseResponseObject, error) {
	id := req.CloseId.String()
	var society string
	err := s.db.QueryRow(ctx, `SELECT society_id::text FROM period_closes WHERE id = $1`, id).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecidePeriodClose404JSONResponse(notFound("close")), nil
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return DecidePeriodClose404JSONResponse(notFound("close")), nil
	}
	b := req.Body
	if b == nil {
		return DecidePeriodClose400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || len([]rune(*reason)) < 10 || len([]rune(*reason)) > 500) {
		return DecidePeriodClose422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err = s.onStepUp(ctx, "period_close_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO period_close_decisions (close_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, b.Confirmed, callerOf(ctx), act, reason)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "period_close_decision_act":
			return DecidePeriodClose403JSONResponse(stepUpProblem("period_close_decision")), nil
		case c == "period_close_decision_by_officer":
			return DecidePeriodClose403JSONResponse(disputeNotOfficer), nil
		case c == "period_close_second_officer":
			return DecidePeriodClose403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "period_close_decided_once":
			return DecidePeriodClose409JSONResponse{Code: "already_decided", Message: "this close is already decided"}, nil
		case c == "period_close_forward":
			return DecidePeriodClose409JSONResponse{Code: "not_forward", Message: dbMessage(err)}, nil
		case c == "period_close_balances":
			s.log.ErrorContext(ctx, "a close refused: the trial balance does not sum to zero", "close", id, "society", society)
			return DecidePeriodClose409JSONResponse{Code: "books_do_not_balance", Message: dbMessage(err)}, nil
		case c == "period_close_rejection_says_why":
			return DecidePeriodClose422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := s.onePeriodClose(ctx, id)
	if err != nil {
		return nil, err
	}
	return DecidePeriodClose201JSONResponse(out), nil
}
