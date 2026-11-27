//go:build tier1

// The reference for corrections over HTTP (0043), DERIVED from
// internal/api/corrections.go and breakable by name (ARGON_TEST_API_FAKE):
//
//	corr_proposer_unnamed  a proposal does not say who proposed it
//	corr_step_up_unmapped  a proposal without its proof: 500
//	corr_second_unmapped   the proposer deciding: 500
//	corr_posted_unsaid     a confirmation does not name what was posted
//	corr_not_own_unmapped  an officer correcting their own entry: 500
//	corr_lines_db          bad adjustment lines reach the database (500)
//	corr_lines_unshown     an adjustment's lines not shown
//	corr_vfd_unmapped      correcting what VFD confirmed: 500
//	corr_waiting_unmapped  a second correction while one waits: 500
//	corr_queue_all         the waiting queue shows decided corrections too
//	corr_any_reader        anyone signed in reads a society's corrections
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

// entrySociety: the entry's society, when the caller is one of its officers.
func (f *fakeAPI) fcEntrySociety(ctx context.Context, entry string) (string, bool, error) {
	var society string
	err := f.db.QueryRow(ctx, `SELECT society_id::text FROM ledger_entries WHERE id = $1`, entry).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	ok, err := f.fqOfficer(ctx, society)
	return society, ok, err
}

type fcLine struct {
	Account string `json:"account_id"`
	Member  string `json:"member_id,omitempty"`
	Kobo    int64  `json:"amount_kobo"`
}

const fcSelect = `SELECT c.id::text, c.entry_id::text, c.kind, c.reason, c.lines, c.proposed_by::text, pu.full_name, c.at,
	  k.confirmed, k.decided_by::text, du.full_name, k.at, k.reason, k.entry_id::text
	FROM entry_corrections c JOIN users pu ON pu.id = c.proposed_by
	LEFT JOIN entry_correction_decisions k ON k.correction_id = c.id
	LEFT JOIN users du ON du.id = k.decided_by`

func (f *fakeAPI) fcCorrections(ctx context.Context, where string, args ...any) ([]api.EntryCorrection, error) {
	rows, err := f.db.Query(ctx, fcSelect+` WHERE `+where+` ORDER BY c.at DESC, c.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	type raw struct {
		c     api.EntryCorrection
		lines []byte
	}
	var rs []raw
	for rows.Next() {
		var r raw
		var id, entry, kind, by string
		var confirmed *bool
		var decidedBy, decidedName, decisionReason, posted *string
		var decidedAt *time.Time
		if err := rows.Scan(&id, &entry, &kind, &r.c.Reason, &r.lines, &by, &r.c.ProposedBy.Name, &r.c.ProposedAt,
			&confirmed, &decidedBy, &decidedName, &decidedAt, &decisionReason, &posted); err != nil {
			rows.Close()
			return nil, err
		}
		r.c.CorrectionId, r.c.EntryId, r.c.Kind, r.c.ProposedBy.UserId = uuidOf(id), uuidOf(entry), api.EntryCorrectionKind(kind), uuidOf(by)
		r.c.State, r.c.Lines = api.EntryCorrectionStateWaiting, []api.CorrectionLine{}
		if f.brk == "corr_proposer_unnamed" {
			r.c.ProposedBy = api.EmailSetter{}
		}
		if confirmed != nil {
			r.c.Decision = &struct {
				At            time.Time           `json:"at"`
				By            api.EmailSetter         `json:"by"`
				Confirmed     bool                `json:"confirmed"`
				PostedEntryId *openapi_types.UUID `json:"postedEntryId"`
				Reason        *string             `json:"reason"`
			}{At: *decidedAt, By: api.EmailSetter{UserId: uuidOf(deref(decidedBy)), Name: deref(decidedName)}, Confirmed: *confirmed, Reason: decisionReason}
			if posted != nil && f.brk != "corr_posted_unsaid" {
				p := uuidOf(*posted)
				r.c.Decision.PostedEntryId = &p
			}
			r.c.State = api.EntryCorrectionStateRejected
			if *confirmed {
				r.c.State = api.EntryCorrectionStateConfirmed
			}
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]api.EntryCorrection, 0, len(rs))
	for _, r := range rs {
		if len(r.lines) > 0 && f.brk != "corr_lines_unshown" {
			var ls []fcLine
			if err := json.Unmarshal(r.lines, &ls); err != nil {
				return nil, err
			}
			for _, l := range ls {
				var code string
				if err := f.db.QueryRow(ctx, `SELECT code FROM accounts WHERE id = $1`, l.Account).Scan(&code); err != nil {
					return nil, err
				}
				cl := api.CorrectionLine{AccountCode: code, AmountKobo: strconv.FormatInt(l.Kobo, 10)}
				if l.Member != "" {
					m := uuidOf(l.Member)
					cl.MemberId = &m
				}
				r.c.Lines = append(r.c.Lines, cl)
			}
		}
		out = append(out, r.c)
	}
	return out, nil
}

func (f *fakeAPI) fcOneCorrection(ctx context.Context, id string) (api.EntryCorrection, error) {
	cs, err := f.fcCorrections(ctx, `c.id = $1`, id)
	if err != nil {
		return api.EntryCorrection{}, err
	}
	if len(cs) != 1 {
		return api.EntryCorrection{}, errors.New("api: the correction just written was not found")
	}
	return cs[0], nil
}

var fcBadLines = api.Problem{Code: "bad_lines", Message: "an adjustment is two or more lines that sum to zero, none zero, on the entry's own accounts and members"}

// adjustmentLines: the lines as the database takes them, accounts found by
// code in the entry's society; ok false when a line is malformed.
func (f *fakeAPI) fcAdjustmentLines(ctx context.Context, society string, ls *[]api.CorrectionLine) (string, bool, error) {
	if ls == nil || len(*ls) < 2 {
		return "", false, nil
	}
	var out []fcLine
	var total int64
	for _, l := range *ls {
		k, err := strconv.ParseInt(l.AmountKobo, 10, 64)
		if err != nil || k == 0 {
			return "", false, nil
		}
		var acct string
		err = f.db.QueryRow(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND code = $2`, society, l.AccountCode).Scan(&acct)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		d := fcLine{Account: acct, Kobo: k}
		if l.MemberId != nil {
			d.Member = l.MemberId.String()
		}
		total += k
		out = append(out, d)
	}
	if total != 0 {
		return "", false, nil
	}
	b, err := json.Marshal(out)
	return string(b), true, err
}

func (f *fakeAPI) ProposeEntryCorrection(ctx context.Context, req api.ProposeEntryCorrectionRequestObject) (api.ProposeEntryCorrectionResponseObject, error) {
	entry := req.EntryId.String()
	society, ok, err := f.fcEntrySociety(ctx, entry)
	if err != nil {
		return nil, err
	}
	if !ok {
		return api.ProposeEntryCorrection404JSONResponse(fqNotFound("entry")), nil
	}
	b := req.Body
	if b == nil {
		return api.ProposeEntryCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	reason := strings.TrimSpace(b.Reason)
	if n := len([]rune(reason)); n < 10 || n > 500 {
		return api.ProposeEntryCorrection422JSONResponse{Code: "reason_required", Message: "say why, 10 to 500 characters"}, nil
	}
	var lines *string
	if b.Kind == api.ProposeEntryCorrectionKindAdjustment {
		l, ok, err := f.fcAdjustmentLines(ctx, society, b.Lines)
		if err != nil {
			return nil, err
		}
		if !ok && f.brk == "corr_lines_db" {
			ok, l = true, `[{"account_id":"00000000-0000-0000-0000-000000000000","amount_kobo":1},{"account_id":"00000000-0000-0000-0000-000000000000","amount_kobo":-2}]`
		}
		if !ok {
			return api.ProposeEntryCorrection422JSONResponse(fcBadLines), nil
		}
		lines = &l
	}
	var id string
	err = f.fqStepUp(ctx, "entry_correction", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO entry_corrections (entry_id, kind, lines, reason, proposed_by, proposed_act) VALUES ($1, $2, $3::jsonb, $4, $5, $6) RETURNING id::text`,
			entry, string(b.Kind), lines, reason, ctx.Value(ctxUser{}).(string), act).Scan(&id)
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case (stepUpRefusals[c] || c == "entry_correction_act") && f.brk != "corr_step_up_unmapped":
			return api.ProposeEntryCorrection403JSONResponse(stepUpProblem("entry_correction")), nil
		case c == "entry_correction_by_officer":
			return api.ProposeEntryCorrection403JSONResponse(fqNotOfficer), nil
		case c == "entry_correction_not_own" && f.brk != "corr_not_own_unmapped":
			return api.ProposeEntryCorrection403JSONResponse{Code: "conflict_of_interest", Message: dbMessage(err)}, nil
		case c == "entry_correctable" && f.brk != "corr_vfd_unmapped":
			return api.ProposeEntryCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "entry_correction_waits" && f.brk != "corr_waiting_unmapped":
			return api.ProposeEntryCorrection409JSONResponse{Code: "already_waiting", Message: dbMessage(err)}, nil
		case (c == "adjustment_lines_balance" || c == "adjustment_on_the_entrys_accounts") && f.brk != "corr_lines_db":
			return api.ProposeEntryCorrection422JSONResponse{Code: "bad_lines", Message: dbMessage(err)}, nil
		case c == "entry_correction_says_why":
			return api.ProposeEntryCorrection422JSONResponse{Code: "reason_required", Message: "say why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := f.fcOneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.ProposeEntryCorrection201JSONResponse(out), nil
}

func (f *fakeAPI) ListEntryCorrections(ctx context.Context, req api.ListEntryCorrectionsRequestObject) (api.ListEntryCorrectionsResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ListEntryCorrections404JSONResponse(fqNotFound("society")), nil
	}
	where, args := `c.society_id = $1`, []any{society}
	if st := req.Params.State; st != nil && f.brk != "corr_queue_all" {
		switch *st {
		case api.Waiting:
			where += ` AND k.id IS NULL`
		case api.Confirmed:
			where += ` AND k.confirmed`
		case api.Rejected:
			where += ` AND NOT k.confirmed`
		}
	}
	cs, err := f.fcCorrections(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	return api.ListEntryCorrections200JSONResponse{Items: cs}, nil
}

// correctionSociety: the correction's society, when the caller is one of its officers.
func (f *fakeAPI) fcCorrectionSociety(ctx context.Context, id string) (string, bool, error) {
	if f.brk == "corr_any_reader" {
		var society string
		f.db.QueryRow(ctx, `SELECT society_id::text FROM entry_corrections WHERE id = $1`, id).Scan(&society)
		return society, society != "", nil
	}
	var society string
	err := f.db.QueryRow(ctx, `SELECT society_id::text FROM entry_corrections WHERE id = $1`, id).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	ok, err := f.fqOfficer(ctx, society)
	return society, ok, err
}

func (f *fakeAPI) GetEntryCorrection(ctx context.Context, req api.GetEntryCorrectionRequestObject) (api.GetEntryCorrectionResponseObject, error) {
	id := req.CorrectionId.String()
	if _, ok, err := f.fcCorrectionSociety(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.GetEntryCorrection404JSONResponse(fqNotFound("correction")), nil
	}
	c, err := f.fcOneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.GetEntryCorrection200JSONResponse(c), nil
}

func (f *fakeAPI) DecideEntryCorrection(ctx context.Context, req api.DecideEntryCorrectionRequestObject) (api.DecideEntryCorrectionResponseObject, error) {
	id := req.CorrectionId.String()
	if _, ok, err := f.fcCorrectionSociety(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.DecideEntryCorrection404JSONResponse(fqNotFound("correction")), nil
	}
	b := req.Body
	if b == nil {
		return api.DecideEntryCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || len([]rune(*reason)) < 10 || len([]rune(*reason)) > 500) {
		return api.DecideEntryCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err := f.fqStepUp(ctx, "entry_correction_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO entry_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, b.Confirmed, ctx.Value(ctxUser{}).(string), act, reason)
		return err
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c] || c == "entry_decision_act":
			return api.DecideEntryCorrection403JSONResponse(stepUpProblem("entry_correction_decision")), nil
		case c == "entry_decision_by_officer":
			return api.DecideEntryCorrection403JSONResponse(fqNotOfficer), nil
		case c == "entry_second_officer" && f.brk != "corr_second_unmapped":
			return api.DecideEntryCorrection403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "entry_correction_not_own":
			return api.DecideEntryCorrection403JSONResponse{Code: "conflict_of_interest", Message: dbMessage(err)}, nil
		case c == "entry_correction_decided_once":
			return api.DecideEntryCorrection409JSONResponse{Code: "already_decided", Message: "this correction is already decided"}, nil
		case c == "entry_correctable" || c == "ledger_entry_reversed_once":
			return api.DecideEntryCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "entry_correction_rejection_says_why":
			return api.DecideEntryCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := f.fcOneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.DecideEntryCorrection201JSONResponse(out), nil
}
