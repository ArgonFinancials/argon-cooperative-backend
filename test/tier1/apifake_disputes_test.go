//go:build tier1

// The reference for disputes over HTTP (0042), DERIVED from internal/api/disputes.go
// and breakable by name (ARGON_TEST_API_FAKE=<break>), for tier1_api_disputes_test.go.
// Breaks: see fqBreaks.
package tier1

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/argon/backend/internal/api"
)

type fqRow struct {
	society, member, raisedBy string
}

func (f *fakeAPI) fqDisputeOf(ctx context.Context, id string) (fqRow, bool, error) {
	var r fqRow
	err := f.db.QueryRow(ctx, `SELECT society_id::text, member_id::text, raised_by::text FROM entry_disputes WHERE id = $1`, id).Scan(&r.society, &r.member, &r.raisedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// disputeReader: the member who asked, or a current officer of the society
// (the auditor reads too).
func (f *fakeAPI) fqDisputeReader(ctx context.Context, r fqRow) (bool, error) {
	if r.raisedBy == ctx.Value(ctxUser{}).(string) || f.brk == "disputes_any_reader" {
		return true, nil
	}
	return f.fqOfficer(ctx, r.society)
}

const fqSelect = `SELECT d.id::text, d.member_id::text, m.full_name, d.entry_id::text, e.kind::text, e.value_date, d.question, d.raised_at,
	  st.state, dispute_correctable(d.entry_id),
	  (SELECT c.id::text FROM dispute_corrections c WHERE c.dispute_id = d.id
	     AND NOT EXISTS (SELECT 1 FROM dispute_correction_decisions k WHERE k.correction_id = c.id))
	FROM entry_disputes d JOIN members m ON m.id = d.member_id JOIN ledger_entries e ON e.id = d.entry_id
	JOIN dispute_states st ON st.dispute_id = d.id`

func (f *fakeAPI) fqDisputes(ctx context.Context, where string, args ...any) ([]api.Dispute, error) {
	rows, err := f.db.Query(ctx, fqSelect+` WHERE `+where+` ORDER BY d.raised_at DESC, d.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := []api.Dispute{}
	for rows.Next() {
		var d api.Dispute
		var id, member, entry, state string
		var vd *time.Time
		var pending *string
		if err := rows.Scan(&id, &member, &d.MemberName, &entry, &d.EntryKind, &vd, &d.Question, &d.RaisedAt, &state, &d.Correctable, &pending); err != nil {
			rows.Close()
			return nil, err
		}
		d.DisputeId, d.MemberId, d.EntryId, d.State = uuidOf(id), uuidOf(member), uuidOf(entry), api.DisputeState(state)
		if vd != nil {
			d.EntryValueDate = &openapi_types.Date{Time: *vd}
		}
		if pending != nil {
			p := uuidOf(*pending)
			d.PendingCorrectionId = &p
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Events, err = f.fqDisputeEvents(ctx, out[i].DisputeId.String()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (f *fakeAPI) fqDisputeEvents(ctx context.Context, id string) ([]api.DisputeEvent, error) {
	rows, err := f.db.Query(ctx, `
		SELECT 'answer', a.at, a.id::text, a.answered_by::text, u.full_name, a.answer, NULL, NULL::text, NULL::boolean, NULL::text
		  FROM dispute_answers a JOIN users u ON u.id = a.answered_by WHERE a.dispute_id = $1
		UNION ALL
		SELECT 'reply', r.at, r.id::text, r.replied_by::text, u.full_name, r.note, r.reply, NULL, NULL, NULL
		  FROM dispute_replies r JOIN users u ON u.id = r.replied_by WHERE r.dispute_id = $1
		UNION ALL
		SELECT 'correction', c.at, c.id::text, c.proposed_by::text, u.full_name, c.reason, NULL, c.id::text, NULL, NULL
		  FROM dispute_corrections c JOIN users u ON u.id = c.proposed_by WHERE c.dispute_id = $1
		UNION ALL
		SELECT 'decision', k.at, k.id::text, k.decided_by::text, u.full_name, k.reason, NULL, c.id::text, k.confirmed, k.entry_id::text
		  FROM dispute_corrections c JOIN dispute_correction_decisions k ON k.correction_id = c.id JOIN users u ON u.id = k.decided_by
		 WHERE c.dispute_id = $1
		ORDER BY 2 `+map[bool]string{true: "DESC", false: ""}[f.brk == "disputes_events_newest_first"]+`, 3`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.DisputeEvent{}
	for rows.Next() {
		var e api.DisputeEvent
		var kind, rid, by string
		var reply, correction, reversal *string
		if err := rows.Scan(&kind, &e.At, &rid, &by, &e.ByName, &e.Text, &reply, &correction, &e.Confirmed, &reversal); err != nil {
			return nil, err
		}
		e.Kind, e.ByUserId = api.DisputeEventKind(kind), uuidOf(by)
		if reply != nil {
			r := api.DisputeEventReply(*reply)
			e.Reply = &r
		}
		if correction != nil {
			c := uuidOf(*correction)
			e.CorrectionId = &c
		}
		if reversal != nil && f.brk != "disputes_reversal_unsaid" {
			r := uuidOf(*reversal)
			e.ReversalEntryId = &r
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (f *fakeAPI) fqOneDispute(ctx context.Context, id string) (api.Dispute, error) {
	ds, err := f.fqDisputes(ctx, `d.id = $1`, id)
	if err != nil {
		return api.Dispute{}, err
	}
	if len(ds) != 1 {
		return api.Dispute{}, errors.New("api: the query just written was not found")
	}
	return ds[0], nil
}

func fqWords(s string, lo, hi int) bool {
	n := len([]rune(strings.TrimSpace(s)))
	return n >= lo && n <= hi
}

func (f *fakeAPI) RaiseDispute(ctx context.Context, req api.RaiseDisputeRequestObject) (api.RaiseDisputeResponseObject, error) {
	member := req.MemberId.String()
	mine, err := f.fqOwns(ctx, member)
	if err != nil {
		return nil, err
	}
	if !mine {
		return api.RaiseDispute404JSONResponse(fqNotFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return api.RaiseDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !fqWords(b.Question, 10, 500) && f.brk != "disputes_question_db" {
		return api.RaiseDispute422JSONResponse{Code: "question_required", Message: "say what is wrong with the entry, 10 to 500 characters"}, nil
	}
	var id string
	err = f.db.QueryRow(ctx, `INSERT INTO entry_disputes (entry_id, member_id, raised_by, question) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		b.EntryId.String(), member, ctx.Value(ctxUser{}).(string), strings.TrimSpace(b.Question)).Scan(&id)
	if err != nil {
		switch c := pgConstraint(err); {
		case c == "dispute_on_own_entry" || c == "23503":
			return api.RaiseDispute422JSONResponse{Code: "not_own_entry", Message: "this entry is not one of your society's that names you"}, nil
		case c == "dispute_once_open" && f.brk != "disputes_once_unmapped":
			return api.RaiseDispute409JSONResponse{Code: "dispute_once_open", Message: dbMessage(err)}, nil
		case c == "dispute_by_member":
			return api.RaiseDispute404JSONResponse(fqNotFound("member")), nil
		case c == "dispute_question_said" && f.brk != "disputes_question_db":
			return api.RaiseDispute422JSONResponse{Code: "question_required", Message: "say what is wrong with the entry, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.RaiseDispute201JSONResponse(d), nil
}

func (f *fakeAPI) ListMemberDisputes(ctx context.Context, req api.ListMemberDisputesRequestObject) (api.ListMemberDisputesResponseObject, error) {
	member := req.MemberId.String()
	var society string
	var own bool
	err := f.db.QueryRow(ctx, `SELECT society_id::text, coalesce(user_id = $2, false) FROM members WHERE id = $1`, member, ctx.Value(ctxUser{}).(string)).Scan(&society, &own)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ListMemberDisputes404JSONResponse(fqNotFound("member")), nil
	}
	if err != nil {
		return nil, err
	}
	if !own {
		if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			return api.ListMemberDisputes404JSONResponse(fqNotFound("member")), nil
		}
	}
	ds, err := f.fqDisputes(ctx, `d.member_id = $1`, member)
	if err != nil {
		return nil, err
	}
	return api.ListMemberDisputes200JSONResponse{Items: ds}, nil
}

func (f *fakeAPI) ListSocietyDisputes(ctx context.Context, req api.ListSocietyDisputesRequestObject) (api.ListSocietyDisputesResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ListSocietyDisputes404JSONResponse(fqNotFound("society")), nil
	}
	where, args := `d.society_id = $1`, []any{society}
	if st := req.Params.State; st != nil && f.brk != "disputes_queue_all" {
		if *st == api.ListSocietyDisputesParamsStateWaiting {
			where += ` AND st.state IN ('open', 'reopened', 'correction_proposed')`
		} else {
			where += ` AND st.state = $2`
			args = append(args, string(*st))
		}
	}
	ds, err := f.fqDisputes(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	return api.ListSocietyDisputes200JSONResponse{Items: ds}, nil
}

func (f *fakeAPI) GetDispute(ctx context.Context, req api.GetDisputeRequestObject) (api.GetDisputeResponseObject, error) {
	id := req.DisputeId.String()
	r, found, err := f.fqDisputeOf(ctx, id)
	if err != nil {
		return nil, err
	}
	if found {
		if found, err = f.fqDisputeReader(ctx, r); err != nil {
			return nil, err
		}
	}
	if !found {
		return api.GetDispute404JSONResponse(fqNotFound("query")), nil
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.GetDispute200JSONResponse(d), nil
}

// readableDispute: the query, when the caller may read it.
func (f *fakeAPI) fqReadableDispute(ctx context.Context, id string) (fqRow, bool, error) {
	r, found, err := f.fqDisputeOf(ctx, id)
	if err != nil || !found {
		return r, false, err
	}
	ok, err := f.fqDisputeReader(ctx, r)
	return r, ok, err
}

var (
	fqNotOfficer = api.Problem{Code: "not_permitted", Message: "a current officer of the society acts on a query, not the internal auditor"}
	fqOwn        = api.Problem{Code: "conflict_of_interest", Message: "nobody acts on a query about their own entry"}
	fqNotWaiting = api.Problem{Code: "not_waiting", Message: "this query is not waiting for that: it is answered, being corrected, or closed"}
)

func (f *fakeAPI) AnswerDispute(ctx context.Context, req api.AnswerDisputeRequestObject) (api.AnswerDisputeResponseObject, error) {
	id := req.DisputeId.String()
	if _, ok, err := f.fqReadableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.AnswerDispute404JSONResponse(fqNotFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return api.AnswerDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !fqWords(b.Answer, 10, 1000) {
		return api.AnswerDispute422JSONResponse{Code: "answer_required", Message: "answer the member's question, 10 to 1000 characters"}, nil
	}
	_, err := f.db.Exec(ctx, `INSERT INTO dispute_answers (dispute_id, answered_by, answer) VALUES ($1, $2, $3)`, id, ctx.Value(ctxUser{}).(string), strings.TrimSpace(b.Answer))
	if err != nil {
		switch pgConstraint(err) {
		case "dispute_answered_by_officer":
			return api.AnswerDispute403JSONResponse(fqNotOfficer), nil
		case map[bool]string{true: "dispute_not_own", false: "-"}[f.brk != "disputes_answer_own_unmapped"]:
			return api.AnswerDispute403JSONResponse(fqOwn), nil
		case map[bool]string{true: "dispute_awaits_answer", false: "-"}[f.brk != "disputes_not_waiting_unmapped"]:
			return api.AnswerDispute409JSONResponse(fqNotWaiting), nil
		case "dispute_answer_said":
			return api.AnswerDispute422JSONResponse{Code: "answer_required", Message: "answer the member's question, 10 to 1000 characters"}, nil
		}
		return nil, err
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.AnswerDispute201JSONResponse(d), nil
}

func (f *fakeAPI) ReplyToDispute(ctx context.Context, req api.ReplyToDisputeRequestObject) (api.ReplyToDisputeResponseObject, error) {
	id := req.DisputeId.String()
	r, ok, err := f.fqReadableDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return api.ReplyToDispute404JSONResponse(fqNotFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return api.ReplyToDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if r.raisedBy != ctx.Value(ctxUser{}).(string) && f.brk != "disputes_reply_anyone" {
		return api.ReplyToDispute403JSONResponse{Code: "not_permitted", Message: "the member who asked replies to the answer"}, nil
	}
	var note *string
	if b.Note != nil && strings.TrimSpace(*b.Note) != "" {
		n := strings.TrimSpace(*b.Note)
		note = &n
	}
	if b.Reply == api.ReplyToDisputeReplyReopened && (note == nil || !fqWords(*note, 10, 500)) && f.brk != "disputes_note_db" {
		return api.ReplyToDispute422JSONResponse{Code: "note_required", Message: "say why the answer is not right, 10 to 500 characters"}, nil
	}
	_, err = f.db.Exec(ctx, `INSERT INTO dispute_replies (dispute_id, replied_by, reply, note) VALUES ($1, $2, $3, $4)`, id, ctx.Value(ctxUser{}).(string), string(b.Reply), note)
	if err != nil {
		switch pgConstraint(err) {
		case map[bool]string{true: "dispute_reply_by_member", false: "-"}[f.brk != "disputes_reply_anyone"]:
			return api.ReplyToDispute403JSONResponse{Code: "not_permitted", Message: "the member who asked replies to the answer"}, nil
		case "dispute_awaits_reply":
			return api.ReplyToDispute409JSONResponse{Code: "not_waiting", Message: "there is no answer waiting for your reply"}, nil
		case map[bool]string{true: "dispute_reopened_says_why", false: "-"}[f.brk != "disputes_note_db"]:
			return api.ReplyToDispute422JSONResponse{Code: "note_required", Message: "say why the answer is not right, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.ReplyToDispute201JSONResponse(d), nil
}

func (f *fakeAPI) ProposeCorrection(ctx context.Context, req api.ProposeCorrectionRequestObject) (api.ProposeCorrectionResponseObject, error) {
	id := req.DisputeId.String()
	if _, ok, err := f.fqReadableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ProposeCorrection404JSONResponse(fqNotFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return api.ProposeCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !fqWords(b.Reason, 10, 500) {
		return api.ProposeCorrection422JSONResponse{Code: "reason_required", Message: "say why the entry is reversed, 10 to 500 characters"}, nil
	}
	err := f.fqStepUp(ctx, "dispute_correction", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO dispute_corrections (dispute_id, proposed_by, proposed_act, reason) VALUES ($1, $2, $3, $4)`,
			id, ctx.Value(ctxUser{}).(string), act, strings.TrimSpace(b.Reason))
		return err
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case (stepUpRefusals[c] || c == "dispute_correction_act") && f.brk != "disputes_step_up_unmapped":
			return api.ProposeCorrection403JSONResponse(stepUpProblem("dispute_correction")), nil
		case c == "dispute_correction_by_officer":
			return api.ProposeCorrection403JSONResponse(fqNotOfficer), nil
		case c == "dispute_not_own":
			return api.ProposeCorrection403JSONResponse(fqOwn), nil
		case (c == "dispute_correction_open" || c == "dispute_correctable") && f.brk != "disputes_vfd_unmapped":
			return api.ProposeCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "dispute_correction_says_why":
			return api.ProposeCorrection422JSONResponse{Code: "reason_required", Message: "say why the entry is reversed, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.ProposeCorrection201JSONResponse(d), nil
}

func (f *fakeAPI) DecideCorrection(ctx context.Context, req api.DecideCorrectionRequestObject) (api.DecideCorrectionResponseObject, error) {
	id, correction := req.DisputeId.String(), req.CorrectionId.String()
	if _, ok, err := f.fqReadableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.DecideCorrection404JSONResponse(fqNotFound("query")), nil
	}
	var belongs bool
	if err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM dispute_corrections WHERE id = $1 AND dispute_id = $2)`, correction, id).Scan(&belongs); err != nil {
		return nil, err
	}
	if !belongs {
		return api.DecideCorrection404JSONResponse(fqNotFound("correction")), nil
	}
	b := req.Body
	if b == nil {
		return api.DecideCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || !fqWords(*reason, 10, 500)) {
		return api.DecideCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err := f.fqStepUp(ctx, "dispute_correction_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO dispute_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			correction, b.Confirmed, ctx.Value(ctxUser{}).(string), act, reason)
		return err
	})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c] || c == "dispute_decision_act":
			return api.DecideCorrection403JSONResponse(stepUpProblem("dispute_correction_decision")), nil
		case c == "dispute_decision_by_officer":
			return api.DecideCorrection403JSONResponse(fqNotOfficer), nil
		case c == "dispute_second_officer" && f.brk != "disputes_second_officer_unmapped":
			return api.DecideCorrection403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "dispute_not_own":
			return api.DecideCorrection403JSONResponse(fqOwn), nil
		case c == "dispute_correction_decided_once":
			return api.DecideCorrection409JSONResponse{Code: "already_decided", Message: "this correction is already decided"}, nil
		case c == "dispute_correctable":
			return api.DecideCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "dispute_rejection_says_why":
			return api.DecideCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := f.fqOneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.DecideCorrection201JSONResponse(d), nil
}

// fqBreaks (ARGON_TEST_API_FAKE):
//
//	disputes_any_reader               anyone signed in reads a member's query
//	disputes_question_db              no question reaches the database (500)
//	disputes_once_unmapped            a second open query answered 500
//	disputes_answer_own_unmapped      an officer answering their own query: 500
//	disputes_not_waiting_unmapped     answering a query not waiting: 500
//	disputes_reply_anyone             an officer replies for the member: 500
//	disputes_note_db                  reopening with no reason reaches the database (500)
//	disputes_step_up_unmapped         a proposal without its proof: 500
//	disputes_vfd_unmapped             correcting what VFD confirmed: 500
//	disputes_second_officer_unmapped  the proposer deciding: 500
//	disputes_reversal_unsaid          a confirmation does not name the reversal posted
//	disputes_events_newest_first      the steps newest first
//	disputes_queue_all                the waiting queue shows closed queries too

func fqNotFound(what string) api.Problem { return api.Problem{Code: "not_found", Message: "no such " + what} }

func (f *fakeAPI) fqOfficer(ctx context.Context, society string) (bool, error) {
	return f.officerHere(ctx, ctx.Value(ctxUser{}).(string), society), nil
}

func (f *fakeAPI) fqOwns(ctx context.Context, member string) (bool, error) {
	var mine bool
	err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE id = $1 AND user_id = $2)`, member, ctx.Value(ctxUser{}).(string)).Scan(&mine)
	return mine, err
}

func (f *fakeAPI) fqStepUp(ctx context.Context, kind, proof string, write func(pgx.Tx, string) error) error {
	return pgx.BeginFunc(ctx, f.db, func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`,
			kind, ctx.Value(ctxUser{}).(string), proof).Scan(&act); err != nil {
			return err
		}
		return write(x, act)
	})
}
