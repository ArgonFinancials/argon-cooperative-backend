package api

// DISPUTES (0042, stage 1, 2026-11-25; D-13).
//
//   POST /members/{m}/disputes: the member queries an entry of their own; no
//   step-up (it moves no money, as a claim).
//   GET  /members/{m}/disputes: the member's queries, for the member and their
//   society's officers. GET /societies/{s}/disputes: the officers' queue.
//   GET  /disputes/{id}: one query and every step.
//   POST /disputes/{id}/answers: an officer (not the auditor, not the member).
//   POST /disputes/{id}/replies: the member accepts or reopens, saying why.
//   POST /disputes/{id}/corrections: one officer proposes, on their own PIN.
//   POST /disputes/{id}/corrections/{c}/decisions: a SECOND officer confirms
//   (the database posts the reversal) or rejects, saying why.
//
// The database holds every rule; each refusal is mapped here. A refusal that
// can be known from the request (the length of the words) is answered before
// anything is written, so a proof is never spent on it.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type disputeRow struct {
	society, member, raisedBy string
}

func (s *server) disputeOf(ctx context.Context, id string) (disputeRow, bool, error) {
	var r disputeRow
	err := s.db.QueryRow(ctx, `SELECT society_id::text, member_id::text, raised_by::text FROM entry_disputes WHERE id = $1`, id).Scan(&r.society, &r.member, &r.raisedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// disputeReader: the member who asked, or a current officer of the society
// (the auditor reads too).
func (s *server) disputeReader(ctx context.Context, r disputeRow) (bool, error) {
	if r.raisedBy == callerOf(ctx) {
		return true, nil
	}
	return s.officerHere(ctx, r.society)
}

const disputeSelect = `SELECT d.id::text, d.member_id::text, m.full_name, d.entry_id::text, e.kind::text, e.value_date, d.question, d.raised_at,
	  st.state, dispute_correctable(d.entry_id),
	  (SELECT c.id::text FROM dispute_corrections c WHERE c.dispute_id = d.id
	     AND NOT EXISTS (SELECT 1 FROM dispute_correction_decisions k WHERE k.correction_id = c.id))
	FROM entry_disputes d JOIN members m ON m.id = d.member_id JOIN ledger_entries e ON e.id = d.entry_id
	JOIN dispute_states st ON st.dispute_id = d.id`

func (s *server) disputes(ctx context.Context, where string, args ...any) ([]Dispute, error) {
	rows, err := s.db.Query(ctx, disputeSelect+` WHERE `+where+` ORDER BY d.raised_at DESC, d.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	out := []Dispute{}
	for rows.Next() {
		var d Dispute
		var id, member, entry, state string
		var vd *time.Time
		var pending *string
		if err := rows.Scan(&id, &member, &d.MemberName, &entry, &d.EntryKind, &vd, &d.Question, &d.RaisedAt, &state, &d.Correctable, &pending); err != nil {
			rows.Close()
			return nil, err
		}
		d.DisputeId, d.MemberId, d.EntryId, d.State = uuidOf(id), uuidOf(member), uuidOf(entry), DisputeState(state)
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
		if out[i].Events, err = s.disputeEvents(ctx, out[i].DisputeId.String()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *server) disputeEvents(ctx context.Context, id string) ([]DisputeEvent, error) {
	rows, err := s.db.Query(ctx, `
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
		ORDER BY 2, 3`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DisputeEvent{}
	for rows.Next() {
		var e DisputeEvent
		var kind, rid, by string
		var reply, correction, reversal *string
		if err := rows.Scan(&kind, &e.At, &rid, &by, &e.ByName, &e.Text, &reply, &correction, &e.Confirmed, &reversal); err != nil {
			return nil, err
		}
		e.Kind, e.ByUserId = DisputeEventKind(kind), uuidOf(by)
		if reply != nil {
			r := DisputeEventReply(*reply)
			e.Reply = &r
		}
		if correction != nil {
			c := uuidOf(*correction)
			e.CorrectionId = &c
		}
		if reversal != nil {
			r := uuidOf(*reversal)
			e.ReversalEntryId = &r
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *server) oneDispute(ctx context.Context, id string) (Dispute, error) {
	ds, err := s.disputes(ctx, `d.id = $1`, id)
	if err != nil {
		return Dispute{}, err
	}
	if len(ds) != 1 {
		return Dispute{}, errors.New("api: the query just written was not found")
	}
	return ds[0], nil
}

func wordsBetween(s string, lo, hi int) bool {
	n := len([]rune(strings.TrimSpace(s)))
	return n >= lo && n <= hi
}

func (s *server) RaiseDispute(ctx context.Context, req RaiseDisputeRequestObject) (RaiseDisputeResponseObject, error) {
	member := req.MemberId.String()
	mine, err := s.ownsMember(ctx, member)
	if err != nil {
		return nil, err
	}
	if !mine {
		return RaiseDispute404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return RaiseDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !wordsBetween(b.Question, 10, 500) {
		return RaiseDispute422JSONResponse{Code: "question_required", Message: "say what is wrong with the entry, 10 to 500 characters"}, nil
	}
	var id string
	err = s.db.QueryRow(ctx, `INSERT INTO entry_disputes (entry_id, member_id, raised_by, question) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		b.EntryId.String(), member, callerOf(ctx), strings.TrimSpace(b.Question)).Scan(&id)
	if err != nil {
		switch c := refusedBy(err); {
		case c == "dispute_on_own_entry" || c == "23503":
			return RaiseDispute422JSONResponse{Code: "not_own_entry", Message: "this entry is not one of your society's that names you"}, nil
		case c == "dispute_once_open":
			return RaiseDispute409JSONResponse{Code: "dispute_once_open", Message: dbMessage(err)}, nil
		case c == "dispute_by_member":
			return RaiseDispute404JSONResponse(notFound("member")), nil
		case c == "dispute_question_said":
			return RaiseDispute422JSONResponse{Code: "question_required", Message: "say what is wrong with the entry, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return RaiseDispute201JSONResponse(d), nil
}

func (s *server) ListMemberDisputes(ctx context.Context, req ListMemberDisputesRequestObject) (ListMemberDisputesResponseObject, error) {
	member := req.MemberId.String()
	var society string
	var own bool
	err := s.db.QueryRow(ctx, `SELECT society_id::text, coalesce(user_id = $2, false) FROM members WHERE id = $1`, member, callerOf(ctx)).Scan(&society, &own)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListMemberDisputes404JSONResponse(notFound("member")), nil
	}
	if err != nil {
		return nil, err
	}
	if !own {
		if ok, err := s.officerHere(ctx, society); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			return ListMemberDisputes404JSONResponse(notFound("member")), nil
		}
	}
	ds, err := s.disputes(ctx, `d.member_id = $1`, member)
	if err != nil {
		return nil, err
	}
	return ListMemberDisputes200JSONResponse{Items: ds}, nil
}

func (s *server) ListSocietyDisputes(ctx context.Context, req ListSocietyDisputesRequestObject) (ListSocietyDisputesResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListSocietyDisputes404JSONResponse(notFound("society")), nil
	}
	where, args := `d.society_id = $1`, []any{society}
	if st := req.Params.State; st != nil {
		if *st == ListSocietyDisputesParamsStateWaiting {
			where += ` AND st.state IN ('open', 'reopened', 'correction_proposed')`
		} else {
			where += ` AND st.state = $2`
			args = append(args, string(*st))
		}
	}
	ds, err := s.disputes(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	return ListSocietyDisputes200JSONResponse{Items: ds}, nil
}

func (s *server) GetDispute(ctx context.Context, req GetDisputeRequestObject) (GetDisputeResponseObject, error) {
	id := req.DisputeId.String()
	r, found, err := s.disputeOf(ctx, id)
	if err != nil {
		return nil, err
	}
	if found {
		if found, err = s.disputeReader(ctx, r); err != nil {
			return nil, err
		}
	}
	if !found {
		return GetDispute404JSONResponse(notFound("query")), nil
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetDispute200JSONResponse(d), nil
}

// readableDispute: the query, when the caller may read it.
func (s *server) readableDispute(ctx context.Context, id string) (disputeRow, bool, error) {
	r, found, err := s.disputeOf(ctx, id)
	if err != nil || !found {
		return r, false, err
	}
	ok, err := s.disputeReader(ctx, r)
	return r, ok, err
}

var (
	disputeNotOfficer = Problem{Code: "not_permitted", Message: "a current officer of the society acts on a query, not the internal auditor"}
	disputeOwn        = Problem{Code: "conflict_of_interest", Message: "nobody acts on a query about their own entry"}
	disputeNotWaiting = Problem{Code: "not_waiting", Message: "this query is not waiting for that: it is answered, being corrected, or closed"}
)

func (s *server) AnswerDispute(ctx context.Context, req AnswerDisputeRequestObject) (AnswerDisputeResponseObject, error) {
	id := req.DisputeId.String()
	if _, ok, err := s.readableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return AnswerDispute404JSONResponse(notFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return AnswerDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !wordsBetween(b.Answer, 10, 1000) {
		return AnswerDispute422JSONResponse{Code: "answer_required", Message: "answer the member's question, 10 to 1000 characters"}, nil
	}
	_, err := s.db.Exec(ctx, `INSERT INTO dispute_answers (dispute_id, answered_by, answer) VALUES ($1, $2, $3)`, id, callerOf(ctx), strings.TrimSpace(b.Answer))
	if err != nil {
		switch refusedBy(err) {
		case "dispute_answered_by_officer":
			return AnswerDispute403JSONResponse(disputeNotOfficer), nil
		case "dispute_not_own":
			return AnswerDispute403JSONResponse(disputeOwn), nil
		case "dispute_awaits_answer":
			return AnswerDispute409JSONResponse(disputeNotWaiting), nil
		case "dispute_answer_said":
			return AnswerDispute422JSONResponse{Code: "answer_required", Message: "answer the member's question, 10 to 1000 characters"}, nil
		}
		return nil, err
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return AnswerDispute201JSONResponse(d), nil
}

func (s *server) ReplyToDispute(ctx context.Context, req ReplyToDisputeRequestObject) (ReplyToDisputeResponseObject, error) {
	id := req.DisputeId.String()
	r, ok, err := s.readableDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ReplyToDispute404JSONResponse(notFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return ReplyToDispute400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if r.raisedBy != callerOf(ctx) {
		return ReplyToDispute403JSONResponse{Code: "not_permitted", Message: "the member who asked replies to the answer"}, nil
	}
	var note *string
	if b.Note != nil && strings.TrimSpace(*b.Note) != "" {
		n := strings.TrimSpace(*b.Note)
		note = &n
	}
	if b.Reply == ReplyToDisputeReplyReopened && (note == nil || !wordsBetween(*note, 10, 500)) {
		return ReplyToDispute422JSONResponse{Code: "note_required", Message: "say why the answer is not right, 10 to 500 characters"}, nil
	}
	_, err = s.db.Exec(ctx, `INSERT INTO dispute_replies (dispute_id, replied_by, reply, note) VALUES ($1, $2, $3, $4)`, id, callerOf(ctx), string(b.Reply), note)
	if err != nil {
		switch refusedBy(err) {
		case "dispute_reply_by_member":
			return ReplyToDispute403JSONResponse{Code: "not_permitted", Message: "the member who asked replies to the answer"}, nil
		case "dispute_awaits_reply":
			return ReplyToDispute409JSONResponse{Code: "not_waiting", Message: "there is no answer waiting for your reply"}, nil
		case "dispute_reopened_says_why":
			return ReplyToDispute422JSONResponse{Code: "note_required", Message: "say why the answer is not right, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return ReplyToDispute201JSONResponse(d), nil
}

func (s *server) ProposeCorrection(ctx context.Context, req ProposeCorrectionRequestObject) (ProposeCorrectionResponseObject, error) {
	id := req.DisputeId.String()
	if _, ok, err := s.readableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ProposeCorrection404JSONResponse(notFound("query")), nil
	}
	b := req.Body
	if b == nil {
		return ProposeCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	if !wordsBetween(b.Reason, 10, 500) {
		return ProposeCorrection422JSONResponse{Code: "reason_required", Message: "say why the entry is reversed, 10 to 500 characters"}, nil
	}
	err := s.onStepUp(ctx, "dispute_correction", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO dispute_corrections (dispute_id, proposed_by, proposed_act, reason) VALUES ($1, $2, $3, $4)`,
			id, callerOf(ctx), act, strings.TrimSpace(b.Reason))
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "dispute_correction_act":
			return ProposeCorrection403JSONResponse(stepUpProblem("dispute_correction")), nil
		case c == "dispute_correction_by_officer":
			return ProposeCorrection403JSONResponse(disputeNotOfficer), nil
		case c == "dispute_not_own":
			return ProposeCorrection403JSONResponse(disputeOwn), nil
		case c == "dispute_correction_open" || c == "dispute_correctable":
			return ProposeCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "dispute_correction_says_why":
			return ProposeCorrection422JSONResponse{Code: "reason_required", Message: "say why the entry is reversed, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return ProposeCorrection201JSONResponse(d), nil
}

func (s *server) DecideCorrection(ctx context.Context, req DecideCorrectionRequestObject) (DecideCorrectionResponseObject, error) {
	id, correction := req.DisputeId.String(), req.CorrectionId.String()
	if _, ok, err := s.readableDispute(ctx, id); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return DecideCorrection404JSONResponse(notFound("query")), nil
	}
	var belongs bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM dispute_corrections WHERE id = $1 AND dispute_id = $2)`, correction, id).Scan(&belongs); err != nil {
		return nil, err
	}
	if !belongs {
		return DecideCorrection404JSONResponse(notFound("correction")), nil
	}
	b := req.Body
	if b == nil {
		return DecideCorrection400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	var reason *string
	if b.Reason != nil && strings.TrimSpace(*b.Reason) != "" {
		r := strings.TrimSpace(*b.Reason)
		reason = &r
	}
	if !b.Confirmed && (reason == nil || !wordsBetween(*reason, 10, 500)) {
		return DecideCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
	}
	err := s.onStepUp(ctx, "dispute_correction_decision", b.PinProofId.String(), func(x pgx.Tx, act string) error {
		_, err := x.Exec(ctx, `INSERT INTO dispute_correction_decisions (correction_id, confirmed, decided_by, decided_act, reason) VALUES ($1, $2, $3, $4, $5)`,
			correction, b.Confirmed, callerOf(ctx), act, reason)
		return err
	})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "dispute_decision_act":
			return DecideCorrection403JSONResponse(stepUpProblem("dispute_correction_decision")), nil
		case c == "dispute_decision_by_officer":
			return DecideCorrection403JSONResponse(disputeNotOfficer), nil
		case c == "dispute_second_officer":
			return DecideCorrection403JSONResponse{Code: "second_officer_required", Message: dbMessage(err)}, nil
		case c == "dispute_not_own":
			return DecideCorrection403JSONResponse(disputeOwn), nil
		case c == "dispute_correction_decided_once":
			return DecideCorrection409JSONResponse{Code: "already_decided", Message: "this correction is already decided"}, nil
		case c == "dispute_correctable":
			return DecideCorrection409JSONResponse{Code: "not_correctable", Message: dbMessage(err)}, nil
		case c == "dispute_rejection_says_why":
			return DecideCorrection422JSONResponse{Code: "reason_required", Message: "a rejection says why, 10 to 500 characters"}, nil
		}
		return nil, err
	}
	d, err := s.oneDispute(ctx, id)
	if err != nil {
		return nil, err
	}
	return DecideCorrection201JSONResponse(d), nil
}
