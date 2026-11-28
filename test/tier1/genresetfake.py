# Run from the repo root:  python3 test/tier1/genresetfake.py
# Generates test/tier1/apifake_resets_test.go from internal/api/resets.go: the
# same code, as the reference fakeAPI's, with one named break per run
# (ARGON_TEST_API_FAKE=<break>). Every break must apply exactly once.
import re

src = open('internal/api/resets.go', encoding='utf-8').read()
body = src[src.index('\n)\n', src.index('import (')) + 3:]

ops = ['ResetMemberPin', 'ChangeMemberPhone', 'ListPhoneChanges', 'RaiseOfficerRequest', 'ListOfficerRequests', 'ApproveOfficerRequest', 'ListBanks']
body = re.sub(r'\b(' + '|'.join(ops) + r')(\d{3}JSONResponse|RequestObject|ResponseObject)\b', r'api.\1\2', body)
types = ['PhoneChangeOldNumberToldNoSender', 'PhoneChangeOldNumberToldUnknown', 'PhoneChangeOldNumberTold', 'PhoneChange',
         'OfficerRequestKindPinReset', 'OfficerRequestKindPhoneChange', 'OfficerRequestStateDone', 'OfficerRequestStateExpired',
         'OfficerRequestStateWaiting', 'OfficerRequest', 'OfficerApproval', 'RaiseOfficerRequestKindPhoneChange',
         'RaiseOfficerRequestKindPinReset', 'Bank', 'Problem']
for ty in types:
    body = re.sub(r'(?<![\w.])' + ty + r'\b', 'api.' + ty, body)
body = body.replace('(s *server)', '(f *fakeAPI)').replace('s.db.', 'f.db.')
for m in ['onCredentialAct', 'credTarget', 'nameOf', 'tellOldNumber', 'notice', 'officerRequest']:
    body = re.sub(r'\bs\.' + m + r'\(', 'f.fz' + m[0].upper() + m[1:] + '(', body)
    body = re.sub(r'\) ' + m + r'\(', ') fz' + m[0].upper() + m[1:] + '(', body)
for name in ['credAct', 'nilIfEmpty', 'noticeSelect', 'scanNotice', 'lagos', 'last4Of',
             'notSecretaryReset', 'notSecretaryPhone', 'notOfficerRequest', 'notSignatoryApproval', 'ownCredential', 'officerNeedsTwo',
             'notAnOfficer', 'memberPresentRequired', 'newPhoneRequired', 'phoneInvalid', 'sameNumber', 'alreadyDone', 'alreadyApproved',
             'requestExpired', 'reasonRequired', 'vfdBankCode', 'e164NG']:
    body = re.sub(r'(?<![\w.])' + name + r'\b', 'fz' + name[0].upper() + name[1:], body)
body = body.replace('s.officerHere(ctx, ', 'f.fqOfficer(ctx, ').replace('s.holdsPostIn(ctx, ', 'f.fzHoldsPostIn(ctx, ')
body = body.replace('callerOf(ctx)', 'ctx.Value(ctxUser{}).(string)')
body = re.sub(r'\bnotFound\(', 'fqNotFound(', body)
body = re.sub(r'\bnigerianPhone\(', 'normalPhone(', body)
body = body.replace('refusedBy(', 'pgConstraint(').replace('stepUpRefused[', 'stepUpRefusals[')
body = body.replace('s.sms', 'testNoticeSMS')
body = body.replace('s.log.ErrorContext(', 'fqLog(').replace('s.log.WarnContext(', 'fqLog(')

def brk(o, n):
    global body
    assert body.count(o) == 1, (o[:80], body.count(o))
    body = body.replace(o, n)

# reset_officer_500: an officer's PIN to the secretary alone, neither checked nor said: 500.
brk('''	if officer {
		return api.ResetMemberPin409JSONResponse(fzOfficerNeedsTwo), nil
	}''', '''	if officer && f.brk != "reset_officer_500" {
		return api.ResetMemberPin409JSONResponse(fzOfficerNeedsTwo), nil
	}''')
brk('''		case c == "signatory_reset_needs_two":
			return api.ResetMemberPin409JSONResponse''', '''		case c == "signatory_reset_needs_two" && f.brk != "reset_officer_500":
			return api.ResetMemberPin409JSONResponse''')
# reset_secretary_500: anyone's reset reaches the database, its refusal unsaid: 500.
brk('''	if ok, err := f.fzHoldsPostIn(ctx, caller, society, "secretary"); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return api.ResetMemberPin403JSONResponse''', '''	if ok, err := f.fzHoldsPostIn(ctx, caller, society, "secretary"); f.brk != "reset_secretary_500" && (err != nil || !ok) {
		if err != nil {
			return nil, err
		}
		return api.ResetMemberPin403JSONResponse''')
brk('''		case c == "reset_by_secretary" || c == "reset_by_current_officer":
			return api.ResetMemberPin403JSONResponse''', '''		case (c == "reset_by_secretary" || c == "reset_by_current_officer") && f.brk != "reset_secretary_500":
			return api.ResetMemberPin403JSONResponse''')
# present_unmapped: a change not saying the member is present reaches the database: 500.
brk('''	if !b.MemberPresent {''', '''	if !b.MemberPresent && f.brk != "present_unmapped" {''')
brk('''		case c == "phone_change_member_present":
			return api.ChangeMemberPhone422JSONResponse''', '''		case c == "phone_change_member_present" && f.brk != "present_unmapped":
			return api.ChangeMemberPhone422JSONResponse''')
# phone_officer_alone: an officer's number to the secretary alone, unsaid: 500.
brk('''	if officer {
		return api.ChangeMemberPhone409JSONResponse(fzOfficerNeedsTwo), nil
	}''', '''	if officer && f.brk != "phone_officer_alone" {
		return api.ChangeMemberPhone409JSONResponse(fzOfficerNeedsTwo), nil
	}''')
brk('''		case c == "signatory_phone_needs_two":''', '''		case c == "signatory_phone_needs_two" && f.brk != "phone_officer_alone":''')
# The old number: never told, told at the new one, told without who, told the whole new number, a refusal shown as told.
brk('''	if testNoticeSMS != nil {''', '''	if testNoticeSMS != nil && f.brk != "phone_old_untold" {''')
brk('''		sent, err := testNoticeSMS.Send(ctx, pc.OldPhone, text)''', '''		if f.brk == "phone_text_unnamed" {
			text = strings.Replace(text, " by "+pc.ByName, "", 1)
		}
		if f.brk == "phone_text_whole_number" {
			text = strings.Replace(text, "one ending "+fzLast4Of(pc.NewPhone), pc.NewPhone, 1)
		}
		to := pc.OldPhone
		if f.brk == "phone_new_number_texted" {
			to = pc.NewPhone
		}
		sent, err := testNoticeSMS.Send(ctx, to, text)''')
brk('''	pc.OldNumberTold = api.PhoneChangeOldNumberTold(outcome)''', '''	pc.OldNumberTold = api.PhoneChangeOldNumberTold(outcome)
	if f.brk == "phone_unreachable_hidden" {
		pc.OldNumberTold = "accepted"
	}''')
# The officer path: done on one approval; a missing reason unsaid; the old number untold.
brk('''	case len(approvers) >= 2:''', '''	case len(approvers) >= 2 || (f.brk == "approval_state_early" && len(approvers) >= 1):''')
brk('''	reason := strings.TrimSpace(b.Reason)
	if len(reason) <= 10 {
		return api.ApproveOfficerRequest422JSONResponse''', '''	reason := strings.TrimSpace(b.Reason)
	if len(reason) <= 10 && f.brk != "approval_reason_500" {
		return api.ApproveOfficerRequest422JSONResponse''')
brk('''		case c == "approval_states_reason":''', '''		case c == "approval_states_reason" && f.brk != "approval_reason_500":''')
brk('''		if wrote {''', '''		if wrote && f.brk != "approval_untold" {''')
# banks_vfd_own_listed: VFD's own bank offered.
brk('''FROM vfd_banks_current WHERE code <> $1 ORDER BY name, code`, fzVfdBankCode)''', '''FROM vfd_banks_current WHERE code <> $1 ORDER BY name, code`, map[bool]string{true: "none", false: fzVfdBankCode}[f.brk == "banks_vfd_own_listed"])''')

hdr = '''//go:build tier1

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

var fzE164NG = regexp.MustCompile(`^\\+234[0-9]{10}$`)

var fzReasonRequired = api.Problem{Code: "reason_required", Message: "say why: it is recorded as written"}

func (f *fakeAPI) fzHoldsPostIn(ctx context.Context, user, society, post string) (bool, error) {
	var ok bool
	err := f.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM appointments a JOIN members m ON m.id = a.member_id
		WHERE m.user_id = $1 AND a.society_id = $2 AND ($3 = '' OR a.post::text = $3)
		  AND a.held_from <= current_date AND (a.held_until IS NULL OR a.held_until >= current_date))`, user, society, post).Scan(&ok)
	return ok, err
}
'''
open('test/tier1/apifake_resets_test.go', 'w', encoding='utf-8', newline='\n').write(hdr + body)
print('ok')
