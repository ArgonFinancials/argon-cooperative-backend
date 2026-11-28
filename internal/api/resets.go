package api

// A FORGOTTEN PIN AND A CHANGED PHONE (0045, the reviewer's decisions of
// 2026-11-28; D-15).
//
//   POST /societies/{s}/members/{m}/pin-reset: an ordinary member's PIN, by
//   the society's current secretary alone, on their own step-up
//   (member_reset), with a reason. The PIN, sessions and devices end; the
//   member enrols again with a code.
//   PUT /societies/{s}/members/{m}/phone: a member's number, by the
//   secretary, on their own step-up (phone_change), with a reason, the member
//   present and said to be. The database writes a notice to the OLD number;
//   this sends it after the change commits and records what came of it.
//   GET /societies/{s}/members/{m}/phone-changes: the changes, and whether
//   each old number was told.
//   POST/GET /societies/{s}/officer-requests and POST .../{r}/approvals: an
//   officer's PIN or number (anyone holding a post): a request, then two OTHER
//   current signatories, each on their own step-up with their own reason.
//
// Every refusal that can be known is checked here first, so a refused request
// spends nothing (the proof stays good); the database checks every rule again.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/argon/backend/internal/auth"
)

// credAct: a credential act (0003) as the caller's, spending `proof`. One
// statement, one transaction: the database applies it (a reset, a number) and
// writes the notice to the old number (0045) in it.
type credAct struct {
	kind, member, newPhone, request, reason string
	present                                 *bool
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *server) onCredentialAct(ctx context.Context, proof string, a credAct) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `INSERT INTO credential_acts (kind, actor_user_id, proof_id, target_member_id, new_phone, request_id, reason, member_present)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		a.kind, callerOf(ctx), proof, nilIfEmpty(a.member), nilIfEmpty(a.newPhone), nilIfEmpty(a.request), nilIfEmpty(a.reason), a.present).Scan(&id)
	return id, err
}

// credTarget: the member record in the society: its person (nil: none), its
// number, and whether that person holds a current post in the society.
func (s *server) credTarget(ctx context.Context, society, member string) (user *string, phone string, officer, found bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT m.user_id::text, m.phone, m.user_id IS NOT NULL AND person_holds_post(m.user_id, m.society_id)
		FROM members m WHERE m.id = $1 AND m.society_id = $2`, member, society).Scan(&user, &phone, &officer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, false, nil
	}
	return user, phone, officer, err == nil, err
}

func (s *server) nameOf(ctx context.Context, user string) (string, error) {
	var n string
	err := s.db.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, user).Scan(&n)
	return n, err
}

// ---- an ordinary member's PIN ------------------------------------------------

func (s *server) ResetMemberPin(ctx context.Context, req ResetMemberPinRequestObject) (ResetMemberPinResponseObject, error) {
	caller, society, member := callerOf(ctx), req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ResetMemberPin404JSONResponse(notFound("member")), nil
	}
	user, _, officer, found, err := s.credTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found || user == nil {
		return ResetMemberPin404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return ResetMemberPin400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	reason := strings.TrimSpace(b.Reason)
	if ok, err := s.holdsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ResetMemberPin403JSONResponse(notSecretaryReset), nil
	}
	if *user == caller {
		return ResetMemberPin403JSONResponse(ownCredential), nil
	}
	if len(reason) <= 10 {
		return ResetMemberPin422JSONResponse(reasonRequired), nil
	}
	if officer {
		return ResetMemberPin409JSONResponse(officerNeedsTwo), nil
	}
	_, err = s.onCredentialAct(ctx, b.PinProofId.String(), credAct{kind: "member_reset", member: member, reason: reason})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return ResetMemberPin403JSONResponse(stepUpProblem("member_reset")), nil
		case c == "reset_by_secretary" || c == "reset_by_current_officer":
			return ResetMemberPin403JSONResponse(notSecretaryReset), nil
		case c == "reset_not_self":
			return ResetMemberPin403JSONResponse(ownCredential), nil
		case c == "reset_states_reason":
			return ResetMemberPin422JSONResponse(reasonRequired), nil
		case c == "signatory_reset_needs_two":
			return ResetMemberPin409JSONResponse(officerNeedsTwo), nil
		}
		return nil, err
	}
	out := ResetMemberPin201JSONResponse{MemberId: req.MemberId, Reason: reason, ByUserId: uuidOf(caller), At: time.Now().UTC()}
	if out.ByName, err = s.nameOf(ctx, caller); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- a member's number -----------------------------------------------------------

func (s *server) ChangeMemberPhone(ctx context.Context, req ChangeMemberPhoneRequestObject) (ChangeMemberPhoneResponseObject, error) {
	caller, society, member := callerOf(ctx), req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ChangeMemberPhone404JSONResponse(notFound("member")), nil
	}
	user, phone, officer, found, err := s.credTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found {
		return ChangeMemberPhone404JSONResponse(notFound("member")), nil
	}
	b := req.Body
	if b == nil {
		return ChangeMemberPhone400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	fresh, reason := nigerianPhone(b.NewPhone), strings.TrimSpace(b.Reason)
	if !e164NG.MatchString(fresh) {
		return ChangeMemberPhone400JSONResponse(phoneInvalid), nil
	}
	if ok, err := s.holdsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ChangeMemberPhone403JSONResponse(notSecretaryPhone), nil
	}
	if user != nil && *user == caller {
		return ChangeMemberPhone403JSONResponse(ownCredential), nil
	}
	if len(reason) <= 10 {
		return ChangeMemberPhone422JSONResponse(reasonRequired), nil
	}
	if !b.MemberPresent {
		return ChangeMemberPhone422JSONResponse(memberPresentRequired), nil
	}
	if fresh == phone {
		return ChangeMemberPhone409JSONResponse(sameNumber), nil
	}
	if officer {
		return ChangeMemberPhone409JSONResponse(officerNeedsTwo), nil
	}
	present := true
	act, err := s.onCredentialAct(ctx, b.PinProofId.String(), credAct{kind: "phone_change", member: member, newPhone: fresh, reason: reason, present: &present})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return ChangeMemberPhone403JSONResponse(stepUpProblem("phone_change")), nil
		case c == "phone_change_by_secretary" || c == "reset_by_current_officer":
			return ChangeMemberPhone403JSONResponse(notSecretaryPhone), nil
		case c == "phone_change_not_self":
			return ChangeMemberPhone403JSONResponse(ownCredential), nil
		case c == "reset_states_reason":
			return ChangeMemberPhone422JSONResponse(reasonRequired), nil
		case c == "phone_change_member_present":
			return ChangeMemberPhone422JSONResponse(memberPresentRequired), nil
		case c == "signatory_phone_needs_two":
			return ChangeMemberPhone409JSONResponse(officerNeedsTwo), nil
		}
		return nil, err
	}
	pc, err := s.tellOldNumber(ctx, act)
	if err != nil {
		return nil, err
	}
	return ChangeMemberPhone200JSONResponse(pc), nil
}

// tellOldNumber: the notice the act wrote, sent by SMS to the OLD number, and
// what came of it recorded (once). It runs whatever the caller does next: a
// request that goes away does not leave the old phone untold. Anything but
// accepted is the old phone NOT told, recorded and shown as such.
func (s *server) tellOldNumber(ctx context.Context, act string) (PhoneChange, error) {
	ctx = context.WithoutCancel(ctx)
	pc, id, society, err := s.notice(ctx, `n.act_id = $1`, act)
	if err != nil {
		return pc, err
	}
	outcome, msgID, code := string(PhoneChangeOldNumberToldNoSender), "", ""
	if s.sms != nil {
		text := fmt.Sprintf("Argon: the phone number for your membership of %s was changed to one ending %s by %s on %s. If you did not ask for this, tell your society's chairman now.",
			society, last4Of(pc.NewPhone), pc.ByName, pc.At.In(lagos).Format("2 Jan 2006 15:04"))
		sent, err := s.sms.Send(ctx, pc.OldPhone, text)
		switch {
		case errors.Is(err, auth.ErrSMSUnavailable):
			outcome = string(PhoneChangeOldNumberToldNoSender)
		case sent.Outcome != "":
			outcome, msgID, code = string(sent.Outcome), sent.MessageID, sent.ProviderCode
		default:
			outcome = string(PhoneChangeOldNumberToldUnknown)
		}
		if outcome != string(auth.SendAccepted) {
			msgID = ""
		}
		if outcome != string(auth.SendRefused) {
			code = ""
		}
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO phone_change_notice_sends (notice_id, outcome, provider_message_id, provider_code) VALUES ($1, $2, $3, $4)`,
		id, outcome, nilIfEmpty(msgID), nilIfEmpty(code)); err != nil {
		// Left waiting: 68-check-phone-notices.sh alerts on a notice not sent.
		s.log.ErrorContext(ctx, "the notice to the old number could not be recorded; it stays waiting and is alerted on", "notice", id, "err", err)
		return pc, nil
	}
	if outcome != string(auth.SendAccepted) {
		s.log.WarnContext(ctx, "a changed number's OLD phone was not told", "notice", id, "outcome", outcome)
	}
	pc.OldNumberTold = PhoneChangeOldNumberTold(outcome)
	return pc, nil
}

const noticeSelect = `SELECT n.id::text, n.member_id::text, n.old_phone, n.new_phone, a.actor_user_id::text, u.full_name, n.at,
	  coalesce(ns.outcome, 'waiting'), so.registered_name
	FROM phone_change_notices n JOIN credential_acts a ON a.id = n.act_id JOIN users u ON u.id = a.actor_user_id
	JOIN members m ON m.id = n.member_id JOIN societies so ON so.id = m.society_id
	LEFT JOIN phone_change_notice_sends ns ON ns.notice_id = n.id
	WHERE `

func scanNotice(row pgx.Row) (PhoneChange, string, string, error) {
	var pc PhoneChange
	var id, member, by, told, society string
	err := row.Scan(&id, &member, &pc.OldPhone, &pc.NewPhone, &by, &pc.ByName, &pc.At, &told, &society)
	pc.NoticeId, pc.MemberId, pc.ByUserId, pc.OldNumberTold = uuidOf(id), uuidOf(member), uuidOf(by), PhoneChangeOldNumberTold(told)
	return pc, id, society, err
}

func (s *server) notice(ctx context.Context, where string, arg any) (PhoneChange, string, string, error) {
	return scanNotice(s.db.QueryRow(ctx, noticeSelect+where, arg))
}

var lagos = time.FixedZone("WAT", 3600)

func last4Of(p string) string {
	if len(p) < 4 {
		return p
	}
	return p[len(p)-4:]
}

func (s *server) ListPhoneChanges(ctx context.Context, req ListPhoneChangesRequestObject) (ListPhoneChangesResponseObject, error) {
	society, member := req.SocietyId.String(), req.MemberId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListPhoneChanges404JSONResponse(notFound("member")), nil
	}
	if _, _, _, found, err := s.credTarget(ctx, society, member); err != nil || !found {
		if err != nil {
			return nil, err
		}
		return ListPhoneChanges404JSONResponse(notFound("member")), nil
	}
	rows, err := s.db.Query(ctx, noticeSelect+`n.member_id = $1 ORDER BY n.at DESC, n.id DESC`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := ListPhoneChanges200JSONResponse{Changes: []PhoneChange{}}
	for rows.Next() {
		pc, _, _, err := scanNotice(rows)
		if err != nil {
			return nil, err
		}
		out.Changes = append(out.Changes, pc)
	}
	return out, rows.Err()
}

// ---- an officer's PIN or number: two other signatories ----------------------------

func (s *server) RaiseOfficerRequest(ctx context.Context, req RaiseOfficerRequestRequestObject) (RaiseOfficerRequestResponseObject, error) {
	caller, society := callerOf(ctx), req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return RaiseOfficerRequest404JSONResponse(notFound("society")), nil
	}
	b := req.Body
	if b == nil {
		return RaiseOfficerRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	member := b.MemberId.String()
	user, phone, officer, found, err := s.credTarget(ctx, society, member)
	if err != nil {
		return nil, err
	}
	if !found || user == nil {
		return RaiseOfficerRequest404JSONResponse(notFound("member")), nil
	}
	kind, reason := "signatory_reset_request", strings.TrimSpace(b.Reason)
	fresh := ""
	if b.Kind == RaiseOfficerRequestKindPhoneChange {
		kind = "signatory_phone_request"
		if b.NewPhone == nil || strings.TrimSpace(*b.NewPhone) == "" {
			return RaiseOfficerRequest422JSONResponse(newPhoneRequired), nil
		}
		if fresh = nigerianPhone(*b.NewPhone); !e164NG.MatchString(fresh) {
			return RaiseOfficerRequest400JSONResponse(phoneInvalid), nil
		}
	} else if b.Kind != RaiseOfficerRequestKindPinReset {
		return RaiseOfficerRequest400JSONResponse{Code: "bad_request", Message: "kind is pin_reset or phone_change"}, nil
	}
	var raiser bool
	if err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2, false, true)`, caller, society).Scan(&raiser); err != nil {
		return nil, err
	}
	if !raiser {
		return RaiseOfficerRequest403JSONResponse(notOfficerRequest), nil
	}
	if *user == caller {
		return RaiseOfficerRequest403JSONResponse(ownCredential), nil
	}
	if !officer {
		return RaiseOfficerRequest409JSONResponse(notAnOfficer), nil
	}
	if len(reason) <= 10 {
		return RaiseOfficerRequest422JSONResponse(reasonRequired), nil
	}
	var present *bool
	if kind == "signatory_phone_request" {
		if b.MemberPresent == nil || !*b.MemberPresent {
			return RaiseOfficerRequest422JSONResponse(memberPresentRequired), nil
		}
		if fresh == phone {
			return RaiseOfficerRequest409JSONResponse(sameNumber), nil
		}
		present = b.MemberPresent
	}
	id, err := s.onCredentialAct(ctx, b.PinProofId.String(), credAct{kind: kind, member: member, newPhone: fresh, reason: reason, present: present})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c]:
			return RaiseOfficerRequest403JSONResponse(stepUpProblem(kind)), nil
		case c == "reset_by_current_officer":
			return RaiseOfficerRequest403JSONResponse(notOfficerRequest), nil
		case c == "reset_not_self":
			return RaiseOfficerRequest403JSONResponse(ownCredential), nil
		case c == "signatory_reset_needs_two":
			return RaiseOfficerRequest409JSONResponse(notAnOfficer), nil
		case c == "reset_states_reason":
			return RaiseOfficerRequest422JSONResponse(reasonRequired), nil
		case c == "phone_change_member_present":
			return RaiseOfficerRequest422JSONResponse(memberPresentRequired), nil
		}
		return nil, err
	}
	r, err := s.officerRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	return RaiseOfficerRequest201JSONResponse(r), nil
}

// officerRequest: a request, its approvals, and where it stands; with the
// change to the number and what came of telling the old one, once done.
func (s *server) officerRequest(ctx context.Context, id string) (OfficerRequest, error) {
	var r OfficerRequest
	var kind, member, by string
	var newPhone *string
	err := s.db.QueryRow(ctx, `SELECT rq.kind, rq.target_member_id::text, m.full_name, rq.reason, rq.new_phone, rq.actor_user_id::text, u.full_name, rq.at
		FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id JOIN users u ON u.id = rq.actor_user_id
		WHERE rq.id = $1 AND rq.kind IN ('signatory_reset_request', 'signatory_phone_request')`, id).
		Scan(&kind, &member, &r.MemberName, &r.Reason, &newPhone, &by, &r.RaisedByName, &r.At)
	if err != nil {
		return r, err
	}
	r.RequestId, r.MemberId, r.RaisedByUserId, r.NewPhone = uuidOf(id), uuidOf(member), uuidOf(by), newPhone
	r.Kind = OfficerRequestKindPinReset
	if kind == "signatory_phone_request" {
		r.Kind = OfficerRequestKindPhoneChange
	}
	r.ExpiresAt = r.At.Add(48 * time.Hour)
	rows, err := s.db.Query(ctx, `SELECT a.actor_user_id::text, u.full_name, a.reason, a.at FROM credential_acts a JOIN users u ON u.id = a.actor_user_id
		WHERE a.request_id = $1 AND a.kind = 'signatory_approval' ORDER BY a.at, a.id`, id)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Approvals = []OfficerApproval{}
	approvers := map[string]bool{}
	for rows.Next() {
		var a OfficerApproval
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
	case len(approvers) >= 2:
		r.State = OfficerRequestStateDone
	case time.Now().After(r.ExpiresAt):
		r.State = OfficerRequestStateExpired
	default:
		r.State = OfficerRequestStateWaiting
	}
	if r.State == OfficerRequestStateDone && r.Kind == OfficerRequestKindPhoneChange {
		pc, _, _, err := s.notice(ctx, `n.act_id IN (SELECT id FROM credential_acts WHERE request_id = $1 AND kind = 'signatory_approval')`, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return r, err
		}
		if err == nil {
			r.PhoneChange = &pc
		}
	}
	return r, nil
}

func (s *server) ListOfficerRequests(ctx context.Context, req ListOfficerRequestsRequestObject) (ListOfficerRequestsResponseObject, error) {
	society := req.SocietyId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ListOfficerRequests404JSONResponse(notFound("society")), nil
	}
	rows, err := s.db.Query(ctx, `SELECT rq.id::text FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id
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
	out := ListOfficerRequests200JSONResponse{Requests: []OfficerRequest{}}
	for _, id := range ids {
		r, err := s.officerRequest(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Requests = append(out.Requests, r)
	}
	return out, nil
}

func (s *server) ApproveOfficerRequest(ctx context.Context, req ApproveOfficerRequestRequestObject) (ApproveOfficerRequestResponseObject, error) {
	caller, society, id := callerOf(ctx), req.SocietyId.String(), req.RequestId.String()
	if ok, err := s.officerHere(ctx, society); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return ApproveOfficerRequest404JSONResponse(notFound("request")), nil
	}
	var here bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM credential_acts rq JOIN members m ON m.id = rq.target_member_id
		WHERE rq.id = $1 AND m.society_id = $2 AND rq.kind IN ('signatory_reset_request', 'signatory_phone_request'))`, id, society).Scan(&here); err != nil {
		return nil, err
	}
	if !here {
		return ApproveOfficerRequest404JSONResponse(notFound("request")), nil
	}
	b := req.Body
	if b == nil {
		return ApproveOfficerRequest400JSONResponse{Code: "bad_request", Message: "a body is required"}, nil
	}
	r, err := s.officerRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	var target string
	if err := s.db.QueryRow(ctx, `SELECT coalesce(m.user_id::text, '') FROM members m WHERE m.id = $1`, r.MemberId.String()).Scan(&target); err != nil {
		return nil, err
	}
	switch {
	case r.State == OfficerRequestStateDone:
		return ApproveOfficerRequest409JSONResponse(alreadyDone), nil
	case r.State == OfficerRequestStateExpired:
		return ApproveOfficerRequest409JSONResponse(requestExpired), nil
	case target == caller:
		return ApproveOfficerRequest403JSONResponse(ownCredential), nil
	}
	for _, a := range r.Approvals {
		if a.ByUserId.String() == caller {
			return ApproveOfficerRequest409JSONResponse(alreadyApproved), nil
		}
	}
	var signs bool
	if err := s.db.QueryRow(ctx, `SELECT person_holds_post($1, $2, true)`, caller, society).Scan(&signs); err != nil {
		return nil, err
	}
	if !signs {
		return ApproveOfficerRequest403JSONResponse(notSignatoryApproval), nil
	}
	reason := strings.TrimSpace(b.Reason)
	if len(reason) <= 10 {
		return ApproveOfficerRequest422JSONResponse(reasonRequired), nil
	}
	act, err := s.onCredentialAct(ctx, b.PinProofId.String(), credAct{kind: "signatory_approval", request: id, reason: reason})
	if err != nil {
		switch c := refusedBy(err); {
		case stepUpRefused[c] || c == "approval_after_request":
			return ApproveOfficerRequest403JSONResponse(stepUpProblem("signatory_approval")), nil
		case c == "reset_approver_not_target":
			return ApproveOfficerRequest403JSONResponse(ownCredential), nil
		case c == "reset_approver_is_signatory":
			return ApproveOfficerRequest403JSONResponse(notSignatoryApproval), nil
		case c == "reset_one_approval_per_person":
			return ApproveOfficerRequest409JSONResponse(alreadyApproved), nil
		case c == "reset_request_expired":
			return ApproveOfficerRequest409JSONResponse(requestExpired), nil
		case c == "approval_states_reason":
			return ApproveOfficerRequest422JSONResponse(reasonRequired), nil
		}
		return nil, err
	}
	if r.Kind == OfficerRequestKindPhoneChange {
		var wrote bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM phone_change_notices WHERE act_id = $1)`, act).Scan(&wrote); err != nil {
			return nil, err
		}
		if wrote {
			if _, err := s.tellOldNumber(ctx, act); err != nil {
				return nil, err
			}
		}
	}
	if r, err = s.officerRequest(ctx, id); err != nil {
		return nil, err
	}
	return ApproveOfficerRequest201JSONResponse(r), nil
}

// ---- the banks --------------------------------------------------------------------

func (s *server) ListBanks(ctx context.Context, _ ListBanksRequestObject) (ListBanksResponseObject, error) {
	rows, err := s.db.Query(ctx, `SELECT code, name, taken_at FROM vfd_banks_current WHERE code <> $1 ORDER BY name, code`, vfdBankCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := ListBanks200JSONResponse{Banks: []Bank{}}
	for rows.Next() {
		var b Bank
		if err := rows.Scan(&b.Code, &b.Name, &out.TakenAt); err != nil {
			return nil, err
		}
		out.Banks = append(out.Banks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Banks) == 0 {
		return ListBanks503JSONResponse{Code: "banks_unavailable", Message: "no list of banks has been taken from VFD yet; type the bank's code"}, nil
	}
	return out, nil
}

var (
	notSecretaryReset     = Problem{Code: "not_permitted", Message: "a member's PIN is reset by the society's current secretary"}
	notSecretaryPhone     = Problem{Code: "not_permitted", Message: "a member's number is changed by the society's current secretary"}
	notOfficerRequest     = Problem{Code: "not_permitted", Message: "a request for an officer is raised by a current officer of the society (not the auditor)"}
	notSignatoryApproval  = Problem{Code: "not_permitted", Message: "an approval is given by a current signatory of the society"}
	ownCredential         = Problem{Code: "conflict_of_interest", Message: "nobody resets their own PIN or changes their own number"}
	officerNeedsTwo       = Problem{Code: "officer_needs_two", Message: "this member holds a post: two other signatories approve it (POST /societies/{societyId}/officer-requests)"}
	notAnOfficer          = Problem{Code: "not_an_officer", Message: "this member holds no post: the secretary does this alone"}
	memberPresentRequired = Problem{Code: "member_present_required", Message: "a number is changed with the member present, and the request says so"}
	newPhoneRequired      = Problem{Code: "new_phone_required", Message: "a number change names the new number"}
	phoneInvalid          = Problem{Code: "phone_invalid", Message: "a Nigerian number: +234…, 234… or 0…"}
	sameNumber            = Problem{Code: "same_number", Message: "that is already their number"}
	alreadyDone           = Problem{Code: "already_done", Message: "two signatories have approved it already, and it took effect"}
	alreadyApproved       = Problem{Code: "already_approved", Message: "you have approved this request already: a second signatory approves"}
	requestExpired        = Problem{Code: "expired", Message: "approvals count only within 48 hours of the request: raise it again"}
)
