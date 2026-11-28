//go:build tier1

// The reference for period close over HTTP (0044), DERIVED from
// internal/api/closes.go and breakable by name (ARGON_TEST_API_FAKE):
//
//	close_second_unmapped        the proposer deciding: 500
//	close_future_unmapped        a close through a day to come: 500
//	close_forward_unmapped       a close not forward: 500
//	close_unbalanced_unmapped    books that do not balance: 500, not said
//	close_trial_balance_unshown  a confirmation does not show the trial balance kept
//	close_through_unsaid         the list does not say where the books are closed
//	close_statement_unsaid       a member's statement does not say where the books are closed
package tier1

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

func fpDateOf(d *time.Time) *openapi_types.Date {
	if d == nil {
		return nil
	}
	return &openapi_types.Date{Time: *d}
}

// fpClosedThrough: where the society's books are closed, for the fake's statement.
func (f *fakeAPI) fpClosedThrough(ctx context.Context, society string) *openapi_types.Date {
	if f.brk == "close_statement_unsaid" {
		return nil
	}
	var closed *time.Time
	if err := f.db.QueryRow(ctx, `SELECT society_closed_through($1)`, society).Scan(&closed); err != nil {
		return nil
	}
	return fpDateOf(closed)
}

const fpSelect = `SELECT c.id::text, c.through, c.proposed_by::text, pu.full_name, c.at,
	  k.confirmed, k.decided_by::text, du.full_name, k.at, k.reason, k.trial_balance
	FROM period_closes c JOIN users pu ON pu.id = c.proposed_by
	LEFT JOIN period_close_decisions k ON k.close_id = c.id
	LEFT JOIN users du ON du.id = k.decided_by`

func (f *fakeAPI) fpPeriodCloses(ctx context.Context, where string, args ...any) ([]api.PeriodClose, error) {
	rows, err := f.db.Query(ctx, fpSelect+` WHERE `+where+` ORDER BY c.at DESC, c.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.PeriodClose{}
	for rows.Next() {
		var p api.PeriodClose
		var id, by string
		var through time.Time
		var confirmed *bool
		var decidedBy, decidedName, reason *string
		var decidedAt *time.Time
		var tb []byte
		if err := rows.Scan(&id, &through, &by, &p.ProposedBy.Name, &p.ProposedAt, &confirmed, &decidedBy, &decidedName, &decidedAt, &reason, &tb); err != nil {
			return nil, err
		}
		p.CloseId, p.Through, p.ProposedBy.UserId, p.State = uuidOf(id), openapi_types.Date{Time: through}, uuidOf(by), api.PeriodCloseStateWaiting
		if confirmed != nil {
			p.Decision = &struct {
				At           time.Time            `json:"at"`
				By           api.EmailSetter      `json:"by"`
				Confirmed    bool                 `json:"confirmed"`
				Reason       *string              `json:"reason"`
				TrialBalance *[]api.ClosedBalance `json:"trialBalance"`
			}{At: *decidedAt, By: api.EmailSetter{UserId: uuidOf(deref(decidedBy)), Name: deref(decidedName)}, Confirmed: *confirmed, Reason: reason}
			p.State = api.PeriodCloseStateRejected
			if *confirmed {
				p.State = api.PeriodCloseStateConfirmed
			}
			if len(tb) > 0 && f.brk != "close_trial_balance_unshown" {
				var kept []struct {
					Code    string `json:"code"`
					Name    string `json:"name"`
					Kind    string `json:"kind"`
					Balance int64  `json:"balance_kobo"`
				}
				if err := json.Unmarshal(tb, &kept); err != nil {
					return nil, err
				}
				bs := make([]api.ClosedBalance, 0, len(kept))
				for _, b := range kept {
					bs = append(bs, api.ClosedBalance{Code: b.Code, Name: b.Name, Kind: b.Kind, BalanceKobo: api.Kobo(strconv.FormatInt(b.Balance, 10))})
				}
				p.Decision.TrialBalance = &bs
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (f *fakeAPI) fpOnePeriodClose(ctx context.Context, id string) (api.PeriodClose, error) {
	ps, err := f.fpPeriodCloses(ctx, `c.id = $1`, id)
	if err != nil {
		return api.PeriodClose{}, err
	}
	if len(ps) != 1 {
		return api.PeriodClose{}, errors.New("api: the close just written was not found")
	}
	return ps[0], nil
}

func (f *fakeAPI) ProposePeriodClose(ctx context.Context, req api.ProposePeriodCloseRequestObject) (api.ProposePeriodCloseResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ProposePeriodClose404JSONResponse(fqNotFound("society")), nil
	}
	b := req.Body
	if b == nil {
		return api.ProposePeriodClose400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var id string
	err := f.fqStepUp(ctx, "period_close", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO period_closes (society_id, through, proposed_by, proposed_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			society, b.Through.Time.Format("2006-01-02"), ctx.Value(ctxUser{}).(string), act).Scan(&id)
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c] || c == "period_close_act":
			return api.ProposePeriodClose403JSONResponse(stepUpProblem("period_close")), nil
		case c == "period_close_by_officer":
			return api.ProposePeriodClose403JSONResponse(fqNotOfficer), nil
		case c == "period_close_not_future" && f.brk != "close_future_unmapped":
			return api.ProposePeriodClose422JSONResponse{Code: "not_future", Message: dbMessage(err)}, nil
		case c == "period_close_forward" && f.brk != "close_forward_unmapped":
			return api.ProposePeriodClose409JSONResponse{Code: "not_forward", Message: dbMessage(err)}, nil
		case c == "period_close_waits":
			return api.ProposePeriodClose409JSONResponse{Code: "already_waiting", Message: dbMessage(err)}, nil
		}
		return nil, err
	}
	out, err := f.fpOnePeriodClose(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.ProposePeriodClose201JSONResponse(out), nil
}

func (f *fakeAPI) ListPeriodCloses(ctx context.Context, req api.ListPeriodClosesRequestObject) (api.ListPeriodClosesResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ListPeriodCloses404JSONResponse(fqNotFound("society")), nil
	}
	var closed *time.Time
	if err := f.db.QueryRow(ctx, `SELECT society_closed_through($1)`, society).Scan(&closed); err != nil {
		return nil, err
	}
	items, err := f.fpPeriodCloses(ctx, `c.society_id = $1`, society)
	if err != nil {
		return nil, err
	}
	if f.brk == "close_through_unsaid" {
		closed = nil
	}
	return api.ListPeriodCloses200JSONResponse{ClosedThrough: fpDateOf(closed), Items: items}, nil
}

func (f *fakeAPI) DecidePeriodClose(ctx context.Context, req api.DecidePeriodCloseRequestObject) (api.DecidePeriodCloseResponseObject, error) {
	id := req.CloseId.String()
	var society string
	err := f.db.QueryRow(ctx, `SELECT society_id::text FROM period_closes WHERE id = $1`, id).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.DecidePeriodClose404JSONResponse(fqNotFound("close")), nil
	}
	if err != nil {
		return nil, err
	}
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.DecidePeriodClose404JSONResponse(fqNotFound("close")), nil
	}
	b := req.Body
	if b == nil {
		return api.DecidePeriodClose400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || len([]rune(*reason)) < 10 || len([]rune(*reason)) > 500) {
		return api.DecidePeriodClose422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err = f.fqStepUp(ctx, "period_close_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO period_close_decisions (close_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, b.Confirmed, ctx.Value(ctxUser{}).(string), act, reason)
		return err
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c] || c == "period_close_decision_act":
			return api.DecidePeriodClose403JSONResponse(stepUpProblem("period_close_decision")), nil
		case c == "period_close_decision_by_officer":
			return api.DecidePeriodClose403JSONResponse(fqNotOfficer), nil
		case c == "period_close_second_officer" && f.brk != "close_second_unmapped":
			return api.DecidePeriodClose403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "period_close_decided_once":
			return api.DecidePeriodClose409JSONResponse{Code: "already_decided", Message: "this close is already decided"}, nil
		case c == "period_close_forward":
			return api.DecidePeriodClose409JSONResponse{Code: "not_forward", Message: dbMessage(err)}, nil
		case c == "period_close_balances" && f.brk != "close_unbalanced_unmapped":
			fqLog(ctx, "a close refused: the trial balance does not sum to zero", "close", id, "society", society)
			return api.DecidePeriodClose409JSONResponse{Code: "books_do_not_balance", Message: dbMessage(err)}, nil
		case c == "period_close_rejection_says_why":
			return api.DecidePeriodClose422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := f.fpOnePeriodClose(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.DecidePeriodClose201JSONResponse(out), nil
}
