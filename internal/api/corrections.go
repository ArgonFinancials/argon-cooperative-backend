package api

// REVERSALS AND ADJUSTMENTS OVER HTTP (0043, stage 3, 2026-11-26;
// LEDGER_CONTRACT §2's postReversal and postAdjustment).
//
//   POST /entries/{e}/corrections: one officer proposes, on their own PIN.
//   GET  /societies/{s}/corrections: the officers' queue. GET /corrections/{c}.
//   POST /corrections/{c}/decisions: a DIFFERENT officer confirms (the
//   database posts it) or rejects, saying why.
//
// The database holds every rule; each refusal is mapped here. What can be
// known from the request (the reason's length, a line's shape and account) is
// answered before anything is written, so a proof is never spent on it.

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

// entrySociety: the entry's society, when the caller is one of its officers.
func (s *server) entrySociety(ctx context.Context, entry string) (string, bool, error) {
	var society string
	err := s.db.QueryRow(ctx, `SELECT society_id::text FROM ledger_entries WHERE id = $1`, entry).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	ok, err := s.officerHere(ctx, society)
	return society, ok, err
}

type dbLine struct {
	Account string `json:"account_id"`
	Member  string `json:"member_id,omitempty"`
	Kobo    int64  `json:"amount_kobo"`
}

const correctionSelect = `SELECT c.id::text, c.entry_id::text, c.kind, c.reason, c.lines, c.proposed_by::text, pu.full_name, c.at,
	  k.confirmed, k.decided_by::text, du.full_name, k.at, k.reason, k.entry_id::text
	FROM entry_corrections c JOIN users pu ON pu.id = c.proposed_by
	LEFT JOIN entry_correction_decisions k ON k.correction_id = c.id
	LEFT JOIN users du ON du.id = k.decided_by`

func (s *server) corrections(ctx context.Context, where string, args ...any) ([]EntryCorrection, error) {
	rows, err := s.db.Query(ctx, correctionSelect+` WHERE `+where+` ORDER BY c.at DESC, c.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	type raw struct {
		c     EntryCorrection
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
		r.c.CorrectionId, r.c.EntryId, r.c.Kind, r.c.ProposedBy.UserId = uuidOf(id), uuidOf(entry), EntryCorrectionKind(kind), uuidOf(by)
		r.c.State, r.c.Lines = EntryCorrectionStateWaiting, []CorrectionLine{}
		if confirmed != nil {
			r.c.Decision = &struct {
				At            time.Time           `json:"at"`
				By            EmailSetter         `json:"by"`
				Confirmed     bool                `json:"confirmed"`
				PostedEntryId *openapi_types.UUID `json:"postedEntryId"`
				Reason        *string             `json:"reason"`
			}{At: *decidedAt, By: EmailSetter{UserId: uuidOf(deref(decidedBy)), Name: deref(decidedName)}, Confirmed: *confirmed, Reason: decisionReason}
			if posted != nil {
				p := uuidOf(*posted)
				r.c.Decision.PostedEntryId = &p
			}
			r.c.State = EntryCorrectionStateRejected
			if *confirmed {
				r.c.State = EntryCorrectionStateConfirmed
			}
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EntryCorrection, 0, len(rs))
	for _, r := range rs {
		if len(r.lines) > 0 {
			var ls []dbLine
			if err := json.Unmarshal(r.lines, &ls); err != nil {
				return nil, err
			}
			for _, l := range ls {
				var code string
				if err := s.db.QueryRow(ctx, `SELECT code FROM accounts WHERE id = $1`, l.Account).Scan(&code); err != nil {
					return nil, err
				}
				cl := CorrectionLine{AccountCode: code, AmountKobo: strconv.FormatInt(l.Kobo, 10)}
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

func (s *server) oneCorrection(ctx context.Context, id string) (EntryCorrection, error) {
	cs, err := s.corrections(ctx, `c.id = $1`, id)
	if err != nil {
		return EntryCorrection{}, err
	}
	if len(cs) != 1 {
		return EntryCorrection{}, errors.New("api: the correction just written was not found")
	}
	return cs[0], nil
}

var badLines = Problem{Code: "bad_lines", Message: "an adjustment is two or more lines that sum to zero, none zero, on the entry's own accounts and members"}

// adjustmentLines: the lines as the database takes them, accounts found by
// code in the entry's society; ok false when a line is malformed.
func (s *server) adjustmentLines(ctx context.Context, society string, ls *[]CorrectionLine) (string, bool, error) {
	if ls == nil || len(*ls) < 2 {
		return "", false, nil
	}
	var out []dbLine
	var total int64
	for _, l := range *ls {
		k, err := strconv.ParseInt(l.AmountKobo, 10, 64)
		if err != nil || k == 0 {
			return "", false, nil
		}
		var acct string
		err = s.db.QueryRow(ctx, `SELECT id::text FROM accounts WHERE society_id = $1 AND code = $2`, society, l.AccountCode).Scan(&acct)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		d := dbLine{Account: acct, Kobo: k}
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

func (s *server) ProposeEntryCorrection(ctx context.Context, req ProposeEntryCorrectionRequestObject) (ProposeEntryCorrectionResponseObject, error) {
	entry := req.EntryId.String()
	society, ok, err := s.entrySociety(ctx, entry)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ProposeEntryCorrection404JSONResponse(notFound("entry")), nil
	}
	b := req.Body
	if b == nil {
		return ProposeEntryCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	reason := strings.TrimSpace(b.Reason)
	if n := len([]rune(reason)); n < 10 || n > 500 {
		return ProposeEntryCorrection422JSONResponse{Code: "reason_required", Message: "say why, 10 to 500 characters"}, nil
	}
	var lines *string
	if b.Kind == ProposeEntryCorrectionKindAdjustment {
		l, ok, err := s.adjustmentLines(ctx, society, b.Lines)
		if err != nil {
			return nil, err
		}
		if !ok {
			return ProposeEntryCorrection422JSONResponse(badLines), nil
		}
		lines = &l
	}
	var id string
	err = s.onStepUp(ctx, "entry_correction", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		return x.QueryRow(ctx, `INSERT INTO entry_corrections (entry_id, kind, lines, reason, proposed_by, proposed_act) VALUES ($1, $2, $3::jsonb, $4, $5, $6) RETURNING id::text`,
			entry, string(b.Kind), lines, reason, callerOf(ctx), act).Scan(&id)
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "entry_correction_act":
			return ProposeEntryCorrection403JSONResponse(stepUpProblem("entry_correction")), nil
		case c == "entry_correction_by_officer":
			return ProposeEntryCorrection403JSONResponse(disputeNotOfficer), nil
		case c == "entry_correction_not_own":
			return ProposeEntryCorrection403JSONResponse{Code: "conflict_of_interest", Message: dbMessage(err)}, nil
		case c == "entry_correctable":
			return ProposeEntryCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "entry_correction_waits":
			return ProposeEntryCorrection409JSONResponse{Code: "already_waiting", Message: dbMessage(err)}, nil
		case c == "adjustment_lines_balance" || c == "adjustment_on_the_entrys_accounts":
			return ProposeEntryCorrection422JSONResponse{Code: "bad_lines", Message: dbMessage(err)}, nil
		case c == "entry_correction_says_why":
			return ProposeEntryCorrection422JSONResponse{Code: "reason_required", Message: "say why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := s.oneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return ProposeEntryCorrection201JSONResponse(out), nil
}

func (s *server) ListEntryCorrections(ctx context.Context, req ListEntryCorrectionsRequestObject) (ListEntryCorrectionsResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListEntryCorrections404JSONResponse(notFound("society")), nil
	}
	where, args := `c.society_id = $1`, []any{society}
	if st := req.Params.State; st != nil {
		switch *st {
		case Waiting:
			where += ` AND k.id IS NULL`
		case Confirmed:
			where += ` AND k.confirmed`
		case Rejected:
			where += ` AND NOT k.confirmed`
		}
	}
	cs, err := s.corrections(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	return ListEntryCorrections200JSONResponse{Items: cs}, nil
}

// correctionSociety: the correction's society, when the caller is one of its officers.
func (s *server) correctionSociety(ctx context.Context, id string) (string, bool, error) {
	var society string
	err := s.db.QueryRow(ctx, `SELECT society_id::text FROM entry_corrections WHERE id = $1`, id).Scan(&society)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	ok, err := s.officerHere(ctx, society)
	return society, ok, err
}

func (s *server) GetEntryCorrection(ctx context.Context, req GetEntryCorrectionRequestObject) (GetEntryCorrectionResponseObject, error) {
	id := req.CorrectionId.String()
	if _, ok, err := s.correctionSociety(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return GetEntryCorrection404JSONResponse(notFound("correction")), nil
	}
	c, err := s.oneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetEntryCorrection200JSONResponse(c), nil
}

func (s *server) DecideEntryCorrection(ctx context.Context, req DecideEntryCorrectionRequestObject) (DecideEntryCorrectionResponseObject, error) {
	id := req.CorrectionId.String()
	if _, ok, err := s.correctionSociety(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return DecideEntryCorrection404JSONResponse(notFound("correction")), nil
	}
	b := req.Body
	if b == nil {
		return DecideEntryCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || len([]rune(*reason)) < 10 || len([]rune(*reason)) > 500) {
		return DecideEntryCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err := s.onStepUp(ctx, "entry_correction_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO entry_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			id, b.Confirmed, callerOf(ctx), act, reason)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "entry_decision_act":
			return DecideEntryCorrection403JSONResponse(stepUpProblem("entry_correction_decision")), nil
		case c == "entry_decision_by_officer":
			return DecideEntryCorrection403JSONResponse(disputeNotOfficer), nil
		case c == "entry_second_officer":
			return DecideEntryCorrection403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "entry_correction_not_own":
			return DecideEntryCorrection403JSONResponse{Code: "conflict_of_interest", Message: dbMessage(err)}, nil
		case c == "entry_correction_decided_once":
			return DecideEntryCorrection409JSONResponse{Code: "already_decided", Message: "this correction is already decided"}, nil
		case c == "entry_correctable" || c == "ledger_entry_reversed_once":
			return DecideEntryCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "entry_correction_rejection_says_why":
			return DecideEntryCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	out, err := s.oneCorrection(ctx, id)
	if err != nil {
		return nil, err
	}
	return DecideEntryCorrection201JSONResponse(out), nil
}
