//go:build tier1

// The reference for resets, phone changes and the banks over HTTP (0045,
// 0046), GENERATED from internal/api/resets.go (genresetfake.py) and
// breakable by name (ARGON_TEST_API_FAKE):
//
//	reset_officer_500         an officer's PIN to the secretary alone: not said, 500
//	reset_secretary_500       anyone's reset reaches the database: not said, 500
//	present_unmapped          a change not saying the member is present: 500
//	phone_officer_alone       an officer's number to the secretary alone: 500
//	phone_old_untold          the old number never told
//	phone_new_number_texted   the NEW number told instead of the old
//	phone_text_unnamed        the old number not told who changed it
//	phone_text_whole_number   the old number told the whole new number
//	phone_unreachable_hidden  an old phone not reached shown as told
//	approval_state_early      an officer request done on one approval
//	approval_reason_500       an approval with no reason: 500
//	approval_untold           an officer's old number untold when two approve
//	banks_vfd_own_listed      VFD's own bank (090110) offered
package tier1

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/api"
	"github.com/argon/backend/internal/auth"
)

const fzVfdBankCode = "090110"

var fzE164NG = regexp.MustCompile(`^\+234[0-9]{10}$`)

var fzReasonRequired = api.Problem{Code: "reason_required", Message: "say why: it is recorded as written"}

func (f *fakeAPI) fzHoldsPostIn(ctx context.Context, user, society, post string) (bool, error) {
	var ok bool
	err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND ($3 = '' OR a.post::text = $3)
		  AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, user, society, post).Scan(&ok)
	return ok, err
}

// fzCredAct: a credential act (0003) as the caller's, spending `proof`. One
// statement, one transaction: the database applies it (a reset, a number) and
// writes the notice to the old number (0045) in it.
type fzCredAct struct {
	kind, member, newPhone, request, reason string
	present                                 *bool
}

func fzNilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *fakeAPI) fzOnCredentialAct(ctx context.Context, proof string, a fzCredAct) (string, error) {
	var id string
	err := f.db.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id, new_phone, request_id, reason, member_present)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		a.kind, ctx.Value(ctxUser{}).(string), proof, fzNilIfEmpty(a.member), fzNilIfEmpty(a.newPhone), fzNilIfEmpty(a.request), fzNilIfEmpty(a.reason), a.present).Scan(&id)
	return id, err
}

// credTarget: the member record in the society: its person (nil: none), its
// number, and whether that person holds a current post in the society.
func (f *fakeAPI) fzCredTarget(ctx context.Context, society, member string) (user *string, phone string, officer, found bool, err error) {
	err = f.db.QueryRow(ctx, `SELECT m.user_id::text, m.phone, m.user_id IS NOT NULL AND person_holds_post(m.user_id, m.society_id)
		FROM members m WHERE m.id = $1 AND m.society_id = $2`, member, society).Scan(&user, &phone, &officer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, false, nil
	}
	return user, phone, officer, err == nil, err
}

func (f *fakeAPI) fzNameOf(ctx context.Context, user string) (string, error) {
	var n string
	err := f.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, user).Scan(&n)
	return n, err
}

// ---- an ordinary member's PIN ------------------------------------------------

func (f *fakeAPI) ResetMemberPin(ctx context.Context, req api.ResetMemberPinRequestObject) (api.ResetMemberPinResponseObject, error) {
	caller, society, member := ctx.Value(ctxUser{}).(string), req.SocietyId.String(), req.MemberId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ResetMemberPin404JSONResponse(fqNotFound("member")), nil
	}
	user, _, officer, found, err := f.fzCredTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found || user == nil {
		return api.ResetMemberPin404JSONResponse(fqNotFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return api.ResetMemberPin400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	reason := strings.TrimSpace(b.Reason)
	if ok, err := f.fzHoldsPostIn(ctx, caller, society, "secretary"); f.brk != "reset_secretary_500" && (err != nil || !ok) {
		if err != nil {
			return nil, err
		}
		return api.ResetMemberPin403JSONResponse(fzNotSecretaryReset), nil
	}
	if *user == caller {
		return api.ResetMemberPin403JSONResponse(fzOwnCredential), nil
	}
	if len(reason) <= 10 {
		return api.ResetMemberPin422JSONResponse(fzReasonRequired), nil
	}
	if officer && f.brk != "reset_officer_500" {
		return api.ResetMemberPin409JSONResponse(fzOfficerNeedsTwo), nil
	}
	_, err = f.fzOnCredentialAct(ctx, b.PinProofId.String(), fzCredAct{kind: "member_reset", member: member, reason: reason})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c]:
			return api.ResetMemberPin403JSONResponse(stepUpProblem("member_reset")), nil
		case (c == "reset_by_secretary" || c == "reset_by_current_officer") && f.brk != "reset_secretary_500":
			return api.ResetMemberPin403JSONResponse(fzNotSecretaryReset), nil
		case c == "reset_not_self":
			return api.ResetMemberPin403JSONResponse(fzOwnCredential), nil
		case c == "reset_states_reason":
			return api.ResetMemberPin422JSONResponse(fzReasonRequired), nil
		case c == "signatory_reset_needs_two" && f.brk != "reset_officer_500":
			return api.ResetMemberPin409JSONResponse(fzOfficerNeedsTwo), nil
		}
		return nil, err
	}
	out := api.ResetMemberPin201JSONResponse{MemberId: req.MemberId, Reason: reason, ByUserId: uuidOf(caller), At: time.Now().UTC()}
	if out.ByName, err = f.fzNameOf(ctx, caller); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- a member's number -----------------------------------------------------------

func (f *fakeAPI) ChangeMemberPhone(ctx context.Context, req api.ChangeMemberPhoneRequestObject) (api.ChangeMemberPhoneResponseObject, error) {
	caller, society, member := ctx.Value(ctxUser{}).(string), req.SocietyId.String(), req.MemberId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ChangeMemberPhone404JSONResponse(fqNotFound("member")), nil
	}
	user, phone, officer, found, err := f.fzCredTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found {
		return api.ChangeMemberPhone404JSONResponse(fqNotFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return api.ChangeMemberPhone400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	fresh, reason := normalPhone(b.NewPhone), strings.TrimSpace(b.Reason)
	if !fzE164NG.MatchString(fresh) {
		return api.ChangeMemberPhone400JSONResponse(fzPhoneInvalid), nil
	}
	if ok, err := f.fzHoldsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ChangeMemberPhone403JSONResponse(fzNotSecretaryPhone), nil
	}
	if user != nil && *user == caller {
		return api.ChangeMemberPhone403JSONResponse(fzOwnCredential), nil
	}
	if len(reason) <= 10 {
		return api.ChangeMemberPhone422JSONResponse(fzReasonRequired), nil
	}
	if !b.MemberPresent && f.brk != "present_unmapped" {
		return api.ChangeMemberPhone422JSONResponse(fzMemberPresentRequired), nil
	}
	if fresh == phone {
		return api.ChangeMemberPhone409JSONResponse(fzSameNumber), nil
	}
	if officer && f.brk != "phone_officer_alone" {
		return api.ChangeMemberPhone409JSONResponse(fzOfficerNeedsTwo), nil
	}
	present := true
	act, err := f.fzOnCredentialAct(ctx, b.PinProofId.String(), fzCredAct{kind: "phone_change", member: member, newPhone: fresh, reason: reason, present: &present})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c]:
			return api.ChangeMemberPhone403JSONResponse(stepUpProblem("phone_change")), nil
		case c == "phone_change_by_secretary" || c == "reset_by_current_officer":
			return api.ChangeMemberPhone403JSONResponse(fzNotSecretaryPhone), nil
		case c == "phone_change_not_self":
			return api.ChangeMemberPhone403JSONResponse(fzOwnCredential), nil
		case c == "reset_states_reason":
			return api.ChangeMemberPhone422JSONResponse(fzReasonRequired), nil
		case c == "phone_change_member_present" && f.brk != "present_unmapped":
			return api.ChangeMemberPhone422JSONResponse(fzMemberPresentRequired), nil
		case c == "signatory_phone_needs_two" && f.brk != "phone_officer_alone":
			return api.ChangeMemberPhone409JSONResponse(fzOfficerNeedsTwo), nil
		}
		return nil, err
	}
	pc, err := f.fzTellOldNumber(ctx, act)
	if err != nil {
		return nil, err
	}
	return api.ChangeMemberPhone200JSONResponse(pc), nil
}

// tellOldNumber: the notice the act wrote, sent by SMS to the OLD number, and
// what came of it recorded (once). It runs whatever the caller does next: a
// request that goes away does not leave the old phone untold. Anything but
// accepted is the old phone NOT told, recorded and shown as such.
func (f *fakeAPI) fzTellOldNumber(ctx context.Context, act string) (api.PhoneChange, error) {
	ctx = context.WithoutCancel(ctx)
	pc, id, society, err := f.fzNotice(ctx, `n.act_id = $1`, act)
	if err != nil {
		return pc, err
	}
	outcome, msgID, code := string(api.PhoneChangeOldNumberToldNoSender), "", ""
	if testNoticeSMS != nil && f.brk != "phone_old_untold" {
		text := fmt.Sprintf("Argon: the phone number for your membership of %s was changed to one ending %s by %s on %s. If you did not ask for this, tell your society's chairman now.",
			society, fzLast4Of(pc.NewPhone), pc.ByName, pc.At.In(fzLagos).Format("2 Jan 2006 15:04"))
		if f.brk == "phone_text_unnamed" {
			text = strings.Replace(text, " by "+pc.ByName, "", 1)
		}
		if f.brk == "phone_text_whole_number" {
			text = strings.Replace(text, "one ending "+fzLast4Of(pc.NewPhone), pc.NewPhone, 1)
		}
		to := pc.OldPhone
		if f.brk == "phone_new_number_texted" {
			to = pc.NewPhone
		}
		sent, err := testNoticeSMS.Send(ctx, to, text)
		switch {
		case errors.Is(err, auth.ErrSMSUnavailable):
			outcome = string(api.PhoneChangeOldNumberToldNoSender)
		case sent.Outcome != "":
			outcome, msgID, code = string(sent.Outcome), sent.MessageID, sent.ProviderCode
		default:
			outcome = string(api.PhoneChangeOldNumberToldUnknown)
		}
		if outcome != string(auth.SendAccepted) {
			msgID = ""
		}
		if outcome != string(auth.SendRefused) {
			code = ""
		}
	}
	if _, err := f.db.Exec(ctx, `INSERT INTO phone_change_notice_sends (notice_id, outcome, provider_message_id, provider_code) VALUES ($1, $2, $3, $4)`,
		id, outcome, fzNilIfEmpty(msgID), fzNilIfEmpty(code)); err != nil {
		// Left waiting: 68-check-phone-notices.sh alerts on a notice not sent.
		fqLog(ctx, "the notice to the old number could not be recorded; it stays waiting and is alerted on", "notice", id, "err", err)
		return pc, nil
	}
	if outcome != string(auth.SendAccepted) {
		fqLog(ctx, "a changed number's OLD phone was not told", "notice", id, "outcome", outcome)
	}
	pc.OldNumberTold = api.PhoneChangeOldNumberTold(outcome)
	if f.brk == "phone_unreachable_hidden" {
		pc.OldNumberTold = "accepted"
	}
	return pc, nil
}

const fzNoticeSelect = `SELECT n.id::text, n.member_id::text, n.old_phone, n.new_phone, a.actor_user_id::text, u.full_name, n.at,
	  coalesce(ns.outcome, 'waiting'), so.registered_name
	FROM phone_change_notices n JOIN credential_acts a ON a.id = n.act_id JOIN users u ON u.id = a.actor_user_id
	JOIN members m ON m.id = n.member_id JOIN societies so ON so.id = m.society_id
	LEFT JOIN phone_change_notice_sends ns ON ns.notice_id = n.id
	WHERE `

func fzScanNotice(row pgx.Row) (api.PhoneChange, string, string, error) {
	var pc api.PhoneChange
	var id, member, by, told, society string
	err := row.Scan(&id, &member, &pc.OldPhone, &pc.NewPhone, &by, &pc.ByName, &pc.At, &told, &society)
	pc.NoticeId, pc.MemberId, pc.ByUserId, pc.OldNumberTold = uuidOf(id), uuidOf(member), uuidOf(by), api.PhoneChangeOldNumberTold(told)
	return pc, id, society, err
}

func (f *fakeAPI) fzNotice(ctx context.Context, where string, arg any) (api.PhoneChange, string, string, error) {
	return fzScanNotice(f.db.QueryRow(ctx, fzNoticeSelect+where, arg))
}

var fzLagos = time.FixedZone("WAT", 3600)

func fzLast4Of(p string) string {
	if len(p) < 4 {
		return p
	}
	return p[len(p)-4:]
}

func (f *fakeAPI) ListPhoneChanges(ctx context.Context, req api.ListPhoneChangesRequestObject) (api.ListPhoneChangesResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ListPhoneChanges404JSONResponse(fqNotFound("member")), nil
	}
	if _, _, _, found, err := f.fzCredTarget(ctx, society, member); err != nil || !found {
		if err != nil {
			return nil, err
		}
		return api.ListPhoneChanges404JSONResponse(fqNotFound("member")), nil
	}
	rows, err := f.db.Query(ctx, fzNoticeSelect+`n.member_id = $1 ORDER BY n.at DESC, n.id DESC`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.ListPhoneChanges200JSONResponse{Changes: []api.PhoneChange{}}
	for rows.Next() {
		pc, _, _, err := fzScanNotice(rows)
		if err != nil {
			return nil, err
		}
		out.Changes = append(out.Changes, pc)
	}
	return out, rows.Err()
}

// ---- an officer's PIN or number: two other signatories ----------------------------

func (f *fakeAPI) RaiseOfficerRequest(ctx context.Context, req api.RaiseOfficerRequestRequestObject) (api.RaiseOfficerRequestResponseObject, error) {
	caller, society := ctx.Value(ctxUser{}).(string), req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.RaiseOfficerRequest404JSONResponse(fqNotFound("society")), nil
	}
	b := req.Body
	if b == nil {
		return api.RaiseOfficerRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	member := b.MemberId.String()
	user, phone, officer, found, err := f.fzCredTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found || user == nil {
		return api.RaiseOfficerRequest404JSONResponse(fqNotFound("member")), nil
	}
	kind, reason := "signatory_reset_request", strings.TrimSpace(b.Reason)
	fresh := ""
	if b.Kind == api.RaiseOfficerRequestKindPhoneChange {
		kind = "signatory_phone_request"
		if b.NewPhone == nil || strings.TrimSpace(*b.NewPhone) == "" {
			return api.RaiseOfficerRequest422JSONResponse(fzNewPhoneRequired), nil
		}
		if fresh = normalPhone(*b.NewPhone); !fzE164NG.MatchString(fresh) {
			return api.RaiseOfficerRequest400JSONResponse(fzPhoneInvalid), nil
		}
	} else if b.Kind != api.RaiseOfficerRequestKindPinReset {
		return api.RaiseOfficerRequest400JSONResponse{Code: "bad_request", Message: "kind is pin_reset or phone_change"}, nil
	}
	var raiser bool
	if err := f.db.QueryRow(ctx, `SELECT person_holds_post($1, $2, false, true)`, caller, society).Scan(&raiser); err != nil {
		return nil, err
	}
	if !raiser {
		return api.RaiseOfficerRequest403JSONResponse(fzNotOfficerRequest), nil
	}
	if *user == caller {
		return api.RaiseOfficerRequest403JSONResponse(fzOwnCredential), nil
	}
	if !officer {
		return api.RaiseOfficerRequest409JSONResponse(fzNotAnOfficer), nil
	}
	if len(reason) <= 10 {
		return api.RaiseOfficerRequest422JSONResponse(fzReasonRequired), nil
	}
	var present *bool
	if kind == "signatory_phone_request" {
		if b.MemberPresent == nil || !*b.MemberPresent {
			return api.RaiseOfficerRequest422JSONResponse(fzMemberPresentRequired), nil
		}
		if fresh == phone {
			return api.RaiseOfficerRequest409JSONResponse(fzSameNumber), nil
		}
		present = b.MemberPresent
	}
	id, err := f.fzOnCredentialAct(ctx, b.PinProofId.String(), fzCredAct{kind: kind, member: member, newPhone: fresh, reason: reason, present: present})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c]:
			return api.RaiseOfficerRequest403JSONResponse(stepUpProblem(kind)), nil
		case c == "reset_by_current_officer":
			return api.RaiseOfficerRequest403JSONResponse(fzNotOfficerRequest), nil
		case c == "reset_not_self":
			return api.RaiseOfficerRequest403JSONResponse(fzOwnCredential), nil
		case c == "signatory_reset_needs_two":
			return api.RaiseOfficerRequest409JSONResponse(fzNotAnOfficer), nil
		case c == "reset_states_reason":
			return api.RaiseOfficerRequest422JSONResponse(fzReasonRequired), nil
		case c == "phone_change_member_present":
			return api.RaiseOfficerRequest422JSONResponse(fzMemberPresentRequired), nil
		}
		return nil, err
	}
	r, err := f.fzOfficerRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	return api.RaiseOfficerRequest201JSONResponse(r), nil
}

// officerRequest: a request, its approvals, and where it stands; with the
// change to the number and what came of telling the old one, once done.
func (f *fakeAPI) fzOfficerRequest(ctx context.Context, id string) (api.OfficerRequest, error) {
	var r api.OfficerRequest
	var kind, member, by string
	var newPhone *string
	err := f.db.QueryRow(ctx, `SELECT rq.kind, rq.target_member_id::text, m.full_name, rq.reason, rq.new_phone, rq.actor_user_id::text, u.full_name, rq.at
		FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id JOIN users u ON u.id = rq.actor_user_id
		WHERE rq.id = $1 AND rq.kind IN ('signatory_reset_request', 'signatory_phone_request')`, id).
		Scan(&kind, &member, &r.MemberName, &r.Reason, &newPhone, &by, &r.RaisedByName, &r.At)
	if err != nil {
		return r, err
	}
	r.RequestId, r.MemberId, r.RaisedByUserId, r.NewPhone = uuidOf(id), uuidOf(member), uuidOf(by), newPhone
	r.Kind = api.OfficerRequestKindPinReset
	if kind == "signatory_phone_request" {
		r.Kind = api.OfficerRequestKindPhoneChange
	}
	r.ExpiresAt = r.At.Add(48 * time.Hour)
	rows, err := f.db.Query(ctx, `SELECT a.actor_user_id::text, u.full_name, a.reason, a.at FROM credential_acts a JOIN users u ON u.id = a.actor_user_id
		WHERE a.request_id = $1 AND a.kind = 'signatory_approval' ORDER BY a.at, a.id`, id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Approvals = []api.OfficerApproval{}
	approvers := map[string]bool{}
	for rows.Next() {
		var a api.OfficerApproval
		var who string
		var reason *string
		if err := rows.Scan(&who, &a.ByName, &reason, &a.At); err != nil {
			return r, err
		}
		if reason != nil {
			a.Reason = *reason
		}
		a.ByUserId = uuidOf(who)
		approvers[who] = true
		r.Approvals = append(r.Approvals, a)
	}
	if err := rows.Err(); err != nil {
		return r, err
	}
	switch {
	case len(approvers) >= 2 || (f.brk == "approval_state_early" && len(approvers) >= 1):
		r.State = api.OfficerRequestStateDone
	case time.Now().After(r.ExpiresAt):
		r.State = api.OfficerRequestStateExpired
	default:
		r.State = api.OfficerRequestStateWaiting
	}
	if r.State == api.OfficerRequestStateDone && r.Kind == api.OfficerRequestKindPhoneChange {
		pc, _, _, err := f.fzNotice(ctx, `n.act_id IN (SELECT id FROM credential_acts WHERE request_id = $1 AND kind = 'signatory_approval')`, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return r, err
		}
		if err == nil {
			r.PhoneChange = &pc
		}
	}
	return r, nil
}

func (f *fakeAPI) ListOfficerRequests(ctx context.Context, req api.ListOfficerRequestsRequestObject) (api.ListOfficerRequestsResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ListOfficerRequests404JSONResponse(fqNotFound("society")), nil
	}
	rows, err := f.db.Query(ctx, `SELECT rq.id::text FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id
		WHERE m.society_id = $1 AND rq.kind IN ('signatory_reset_request', 'signatory_phone_request') AND rq.at > now() - interval '7 days'
		ORDER BY rq.at DESC, rq.id DESC`, society)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := api.ListOfficerRequests200JSONResponse{Requests: []api.OfficerRequest{}}
	for _, id := range ids {
		r, err := f.fzOfficerRequest(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Requests = append(out.Requests, r)
	}
	return out, nil
}

func (f *fakeAPI) ApproveOfficerRequest(ctx context.Context, req api.ApproveOfficerRequestRequestObject) (api.ApproveOfficerRequestResponseObject, error) {
	caller, society, id := ctx.Value(ctxUser{}).(string), req.SocietyId.String(), req.RequestId.String()
	if ok, err := f.fqOfficer(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ApproveOfficerRequest404JSONResponse(fqNotFound("request")), nil
	}
	var here bool
	if err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id
		WHERE rq.id = $1 AND m.society_id = $2 AND rq.kind IN ('signatory_reset_request', 'signatory_phone_request'))`, id, society).Scan(&here); err != nil {
		return nil, err
	}
	if !here {
		return api.ApproveOfficerRequest404JSONResponse(fqNotFound("request")), nil
	}
	b := req.Body
	if b == nil {
		return api.ApproveOfficerRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	r, err := f.fzOfficerRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	var target string
	if err := f.db.QueryRow(ctx, `SELECT coalesce(m.user_id::text, '') FROM members m WHERE m.id = $1`, r.MemberId.String()).Scan(&target); err != nil {
		return nil, err
	}
	switch {
	case r.State == api.OfficerRequestStateDone:
		return api.ApproveOfficerRequest409JSONResponse(fzAlreadyDone), nil
	case r.State == api.OfficerRequestStateExpired:
		return api.ApproveOfficerRequest409JSONResponse(fzRequestExpired), nil
	case target == caller:
		return api.ApproveOfficerRequest403JSONResponse(fzOwnCredential), nil
	}
	for _, a := range r.Approvals {
		if a.ByUserId.String() == caller {
			return api.ApproveOfficerRequest409JSONResponse(fzAlreadyApproved), nil
		}
	}
	var signs bool
	if err := f.db.QueryRow(ctx, `SELECT person_holds_post($1, $2, true)`, caller, society).Scan(&signs); err != nil {
		return nil, err
	}
	if !signs {
		return api.ApproveOfficerRequest403JSONResponse(fzNotSignatoryApproval), nil
	}
	reason := strings.TrimSpace(b.Reason)
	if len(reason) <= 10 && f.brk != "approval_reason_500" {
		return api.ApproveOfficerRequest422JSONResponse(fzReasonRequired), nil
	}
	act, err := f.fzOnCredentialAct(ctx, b.PinProofId.String(), fzCredAct{kind: "signatory_approval", request: id, reason: reason})
	if err != nil {
		switch c := pgConstraint(err); {
		case stepUpRefusals[c] || c == "approval_after_request":
			return api.ApproveOfficerRequest403JSONResponse(stepUpProblem("signatory_approval")), nil
		case c == "reset_approver_not_target":
			return api.ApproveOfficerRequest403JSONResponse(fzOwnCredential), nil
		case c == "reset_approver_is_signatory":
			return api.ApproveOfficerRequest403JSONResponse(fzNotSignatoryApproval), nil
		case c == "reset_one_approval_per_person":
			return api.ApproveOfficerRequest409JSONResponse(fzAlreadyApproved), nil
		case c == "reset_request_expired":
			return api.ApproveOfficerRequest409JSONResponse(fzRequestExpired), nil
		case c == "approval_states_reason" && f.brk != "approval_reason_500":
			return api.ApproveOfficerRequest422JSONResponse(fzReasonRequired), nil
		}
		return nil, err
	}
	if r.Kind == api.OfficerRequestKindPhoneChange {
		var wrote bool
		if err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM phone_change_notices WHERE act_id = $1)`, act).Scan(&wrote); err != nil {
			return nil, err
		}
		if wrote && f.brk != "approval_untold" {
			if _, err := f.fzTellOldNumber(ctx, act); err != nil {
				return nil, err
			}
		}
	}
	if r, err = f.fzOfficerRequest(ctx, id); err != nil {
		return nil, err
	}
	return api.ApproveOfficerRequest201JSONResponse(r), nil
}

// ---- the banks --------------------------------------------------------------------

func (f *fakeAPI) ListBanks(ctx context.Context, _ api.ListBanksRequestObject) (api.ListBanksResponseObject, error) {
	rows, err := f.db.Query(ctx, `SELECT code, name, taken_at FROM vfd_banks_current WHERE code <> $1 ORDER BY name, code`, map[bool]string{true: "none", false: fzVfdBankCode}[f.brk == "banks_vfd_own_listed"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := api.ListBanks200JSONResponse{Banks: []api.Bank{}}
	for rows.Next() {
		var b api.Bank
		if err := rows.Scan(&b.Code, &b.Name, &out.TakenAt); err != nil {
			return nil, err
		}
		out.Banks = append(out.Banks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Banks) == 0 {
		return api.ListBanks503JSONResponse{Code: "banks_unavailable", Message: "no list of banks has been taken from VFD yet; type the bank's code"}, nil
	}
	return out, nil
}

var (
	fzNotSecretaryReset     = api.Problem{Code: "not_permitted", Message: "a member's PIN is reset by the society's current secretary"}
	fzNotSecretaryPhone     = api.Problem{Code: "not_permitted", Message: "a member's number is changed by the society's current secretary"}
	fzNotOfficerRequest     = api.Problem{Code: "not_permitted", Message: "a request for an officer is raised by a current officer of the society (not the auditor)"}
	fzNotSignatoryApproval  = api.Problem{Code: "not_permitted", Message: "an approval is given by a current signatory of the society"}
	fzOwnCredential         = api.Problem{Code: "conflict_of_interest", Message: "nobody resets their own PIN or changes their own number"}
	fzOfficerNeedsTwo       = api.Problem{Code: "officer_needs_two", Message: "this member holds a post: two other signatories approve it (POST /societies/{societyId}/officer-requests)"}
	fzNotAnOfficer          = api.Problem{Code: "not_an_officer", Message: "this member holds no post: the secretary does this alone"}
	fzMemberPresentRequired = api.Problem{Code: "member_present_required", Message: "a number is changed with the member present, and the request says so"}
	fzNewPhoneRequired      = api.Problem{Code: "new_phone_required", Message: "a number change names the new number"}
	fzPhoneInvalid          = api.Problem{Code: "phone_invalid", Message: "a Nigerian number: +234…, 234… or 0…"}
	fzSameNumber            = api.Problem{Code: "same_number", Message: "that is already their number"}
	fzAlreadyDone           = api.Problem{Code: "already_done", Message: "two signatories have approved it already, and it took effect"}
	fzAlreadyApproved       = api.Problem{Code: "already_approved", Message: "you have approved this request already: a second signatory approves"}
	fzRequestExpired        = api.Problem{Code: "expired", Message: "approvals count only within 48 hours of the request: raise it again"}
)
