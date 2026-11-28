//go:build tier1

// ACCEPTANCE TESTS — A FORGOTTEN PIN AND A CHANGED PHONE OVER HTTP (0045,
// 2026-11-28, the reviewer's decisions), and the banks as VFD lists them
// (0046). The secretary resets an ordinary member's PIN and changes their
// number, the member present; an officer's goes through two other
// signatories; every changed number's OLD phone is told who did it, and what
// came of telling it is said and kept; the banks are VFD's newest list, never
// VFD's own bank.
package tier1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/argon/backend/internal/auth"
)

// noticeSMS: the SMS provider as the notices meet it: it records every send
// and answers as `outcome` says.
type noticeSMS struct {
	mu      sync.Mutex
	outcome auth.SendOutcome
	sent    []sentSMS
}

func (n *noticeSMS) Send(_ context.Context, to, text string) (auth.Sent, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, sentSMS{to, text})
	switch n.outcome {
	case auth.SendAccepted:
		return auth.Sent{Provider: "termii", Outcome: auth.SendAccepted, MessageID: "msg-" + randHex()[:8]}, nil
	case auth.SendRefused:
		return auth.Sent{Provider: "termii", Outcome: auth.SendRefused, ProviderCode: "R07"}, errors.New("refused")
	default:
		return auth.Sent{Provider: "termii", Outcome: n.outcome}, errors.New("not sent")
	}
}

func (n *noticeSMS) to(number string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, s := range n.sent {
		if s.to == number {
			out = append(out, s.text)
		}
	}
	return out
}

// testNoticeSMS: the sender the next API server is built with (apiServer).
var testNoticeSMS = &noticeSMS{outcome: auth.SendAccepted}

func noticesAnswer(t *testing.T, o auth.SendOutcome) *noticeSMS {
	t.Helper()
	testNoticeSMS = &noticeSMS{outcome: o}
	t.Cleanup(func() { testNoticeSMS = &noticeSMS{outcome: auth.SendAccepted} })
	return testNoticeSMS
}

type rpapi struct {
	*rpWorld
	url string
}

func newResetAPI(t *testing.T) *rpapi {
	t.Helper()
	return &rpapi{rpWorld: newResetWorld(t), url: apiServer(t).URL}
}

func (w *rpapi) call(t *testing.T, method, as, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.url+path, rd)
	req.Header.Set("Authorization", "Bearer "+signIn(t, as))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := http.DefaultClient.Do(req)
	fixture(t, err, method+" "+path)
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r.StatusCode, b}
}

type rpPhoneOut struct {
	NoticeId, MemberId, OldPhone, NewPhone, ByUserId, ByName, OldNumberTold string
}

type rpRequestOut struct {
	RequestId, Kind, State string
	Approvals              []struct{ ByUserId, Reason string }
	PhoneChange            *rpPhoneOut
}

func decodeAs[T any](t *testing.T, r resp, status int, what string) T {
	t.Helper()
	var v T
	if r.status != status || json.Unmarshal(r.body, &v) != nil {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, status)
	}
	return v
}

func (w *rpapi) reset(t *testing.T, as, member, reason string) resp {
	return w.call(t, http.MethodPost, as, "/societies/"+w.s.id+"/members/"+member+"/pin-reset",
		map[string]any{"reason": reason, "pinProofId": stepUp(t, as, "member_reset")})
}

func (w *rpapi) changePhone(t *testing.T, as, member, fresh string, present bool) resp {
	return w.call(t, http.MethodPut, as, "/societies/"+w.s.id+"/members/"+member+"/phone",
		map[string]any{"newPhone": fresh, "reason": rpWhy, "memberPresent": present, "pinProofId": stepUp(t, as, "phone_change")})
}

func (w *rpapi) raise(t *testing.T, as, kind, member, fresh string) resp {
	act := "signatory_reset_request"
	body := map[string]any{"kind": kind, "memberId": member, "reason": rpWhy}
	if kind == "phone_change" {
		act = "signatory_phone_request"
		body["newPhone"], body["memberPresent"] = fresh, true
	}
	body["pinProofId"] = stepUp(t, as, act)
	return w.call(t, http.MethodPost, as, "/societies/"+w.s.id+"/officer-requests", body)
}

func (w *rpapi) approve(t *testing.T, as, req, reason string) resp {
	return w.call(t, http.MethodPost, as, "/societies/"+w.s.id+"/officer-requests/"+req+"/approvals",
		map[string]any{"reason": reason, "pinProofId": stepUp(t, as, "signatory_approval")})
}

const rpApproveWhy = "I know them; they asked me in person today"

func TestApiResets01_TheSecretaryAloneResetsAMembersPIN(t *testing.T) {
	w := newResetAPI(t)
	wantProblem(t, w.reset(t, w.treas, w.memberMem, rpWhy), http.StatusForbidden, "not_permitted", "the treasurer resetting a member's PIN")
	wantProblem(t, w.reset(t, w.sec, w.memberMem, "forgot"), http.StatusUnprocessableEntity, "reason_required", "a reset with no real reason")
	decodeAs[struct{ MemberId, ByName string }](t, w.reset(t, w.sec, w.memberMem, rpWhy), http.StatusCreated, "the secretary resetting a member's PIN")
	if pin, s, d := w.alive(t, w.member); pin || s != 0 || d != 0 {
		t.Fatalf("STILL ALIVE — after the reset the member keeps PIN=%v, %d sessions, %d devices", pin, s, d)
	}
}

func TestApiResets02_AnOfficersPINIsTwoOtherSignatoriesEachWithAReason(t *testing.T) {
	w := newResetAPI(t)
	wantProblem(t, w.reset(t, w.sec, w.loanMem, rpWhy), http.StatusConflict, "officer_needs_two", "the secretary alone resetting a loan officer's PIN")
	r := decodeAs[rpRequestOut](t, w.raise(t, w.sec, "pin_reset", w.loanMem, ""), http.StatusCreated, "the secretary raising the officer's reset")
	wantProblem(t, w.approve(t, w.chair, r.RequestId, "yes"), http.StatusUnprocessableEntity, "reason_required", "an approval with no reason")
	one := decodeAs[rpRequestOut](t, w.approve(t, w.chair, r.RequestId, rpApproveWhy), http.StatusCreated, "the chairman's approval")
	if one.State != "waiting" || len(one.Approvals) != 1 {
		t.Fatalf("WRONG STATE — after one approval: %+v; want waiting, one approval", one)
	}
	if pin, _, _ := w.alive(t, w.loanOfficer); !pin {
		t.Fatalf("RESET — one approval reset the officer's PIN")
	}
	wantProblem(t, w.approve(t, w.chair, r.RequestId, rpApproveWhy), http.StatusConflict, "already_approved", "the chairman approving twice")
	two := decodeAs[rpRequestOut](t, w.approve(t, w.treas, r.RequestId, rpApproveWhy), http.StatusCreated, "the treasurer's approval")
	if two.State != "done" || len(two.Approvals) != 2 {
		t.Fatalf("WRONG STATE — after two approvals: %+v; want done", two)
	}
	if pin, s, d := w.alive(t, w.loanOfficer); pin || s != 0 || d != 0 {
		t.Fatalf("STILL ALIVE — two signatories approved; the officer keeps PIN=%v, %d sessions, %d devices", pin, s, d)
	}
}

func TestApiResets03_ANumberChangeTellsTheOldPhoneWhoChangedIt(t *testing.T) {
	sms := noticesAnswer(t, auth.SendAccepted)
	w := newResetAPI(t)
	fresh := unregistered()
	p := decodeAs[rpPhoneOut](t, w.changePhone(t, w.sec, w.memberMem, fresh, true), http.StatusOK, "the secretary changing a member's number")
	if p.OldPhone != w.phone || p.NewPhone != fresh || p.ByUserId != w.sec || p.OldNumberTold != "accepted" {
		t.Fatalf("WRONG CHANGE — %+v; want %s to %s, by the secretary, the old number told", p, w.phone, fresh)
	}
	texts := sms.to(w.phone)
	if len(texts) != 1 || len(sms.to(fresh)) != 0 {
		t.Fatalf("UNTOLD — the OLD number got %d texts and the new one %d; want one, to the old", len(texts), len(sms.to(fresh)))
	}
	if !strings.Contains(texts[0], "Secretary") || !strings.Contains(texts[0], fresh[len(fresh)-4:]) || strings.Contains(texts[0], fresh) {
		t.Fatalf("UNSAID — the old number was told %q; want who changed it and the new number's last four digits, never the whole number", texts[0])
	}
	if phoneOf(t, w.memberMem) != fresh {
		t.Fatalf("NOT APPLIED — the number is %s", phoneOf(t, w.memberMem))
	}
	var told string
	fixture(t, db.QueryRow(bg(), `SELECT s.outcome FROM phone_change_notice_sends s JOIN phone_change_notices n ON n.id = s.notice_id WHERE n.member_id = $1`, w.memberMem).Scan(&told), "what was recorded")
	if told != "accepted" {
		t.Fatalf("UNRECORDED — the notice's outcome is recorded as %q", told)
	}
}

func TestApiResets04_AnOldPhoneNotToldIsSaidAndKept(t *testing.T) {
	noticesAnswer(t, auth.SendRefused)
	w := newResetAPI(t)
	p := decodeAs[rpPhoneOut](t, w.changePhone(t, w.sec, w.memberMem, unregistered(), true), http.StatusOK, "the change")
	if p.OldNumberTold != "refused" {
		t.Fatalf("HIDDEN — the SMS to the old number was refused and the change says %q; want refused (someone should look twice)", p.OldNumberTold)
	}
	l := decodeAs[struct{ Changes []rpPhoneOut }](t, w.call(t, http.MethodGet, w.chair, "/societies/"+w.s.id+"/members/"+w.memberMem+"/phone-changes", nil), http.StatusOK, "the changes")
	if len(l.Changes) != 1 || l.Changes[0].OldNumberTold != "refused" {
		t.Fatalf("HIDDEN — the officers see %+v; want the change, its old number not told", l.Changes)
	}
}

func TestApiResets05_OnlyTheSecretaryWithTheMemberPresent(t *testing.T) {
	w := newResetAPI(t)
	wantProblem(t, w.changePhone(t, w.sec, w.memberMem, unregistered(), false), http.StatusUnprocessableEntity, "member_present_required", "a change not saying the member is present")
	wantProblem(t, w.changePhone(t, w.treas, w.memberMem, unregistered(), true), http.StatusForbidden, "not_permitted", "the treasurer changing a member's number")
	wantProblem(t, w.changePhone(t, w.sec, w.loanMem, unregistered(), true), http.StatusConflict, "officer_needs_two", "the secretary alone changing an officer's number")
	if phoneOf(t, w.memberMem) != w.phone {
		t.Fatalf("CHANGED — a refused change moved the number")
	}
}

func TestApiResets06_AnOfficersNumberTwoSignatoriesAndTheOldPhoneIsTold(t *testing.T) {
	sms := noticesAnswer(t, auth.SendAccepted)
	w := newResetAPI(t)
	old, fresh := phoneOf(t, w.loanMem), unregistered()
	r := decodeAs[rpRequestOut](t, w.raise(t, w.sec, "phone_change", w.loanMem, fresh), http.StatusCreated, "the request")
	decodeAs[rpRequestOut](t, w.approve(t, w.chair, r.RequestId, rpApproveWhy), http.StatusCreated, "the first approval")
	if len(sms.to(old)) != 0 {
		t.Fatalf("TOLD EARLY — the old number was texted on one approval; nothing has changed yet")
	}
	done := decodeAs[rpRequestOut](t, w.approve(t, w.treas, r.RequestId, rpApproveWhy), http.StatusCreated, "the second approval")
	if done.State != "done" || done.PhoneChange == nil || done.PhoneChange.OldNumberTold != "accepted" || len(sms.to(old)) != 1 {
		t.Fatalf("UNTOLD — after two approvals: %+v, %d texts to the old number; want done, the old number told once", done, len(sms.to(old)))
	}
}

func TestApiResets07_TheBanksAreVFDsNewestListWithoutVFDsOwn(t *testing.T) {
	w := newResetAPI(t)
	a, b := bankCode(), bankCode()
	_, err := takeBanks(3, [2]string{a, "ZENITH TEST BANK"}, [2]string{b, "ACCESS TEST BANK"}, [2]string{"090110", "VFD MICROFINANCE BANK"})
	fixture(t, err, "a list taken from VFD")
	l := decodeAs[struct {
		TakenAt string
		Banks   []struct{ Code, Name string }
	}](t, w.call(t, http.MethodGet, w.member, "/banks", nil), http.StatusOK, "the banks")
	codes := map[string]bool{}
	for _, x := range l.Banks {
		codes[x.Code] = true
	}
	if !codes[a] || !codes[b] || codes["090110"] || l.TakenAt == "" {
		t.Fatalf("WRONG LIST — %+v; want the newest list's banks, when it was taken, and never VFD's own (090110)", l)
	}
}
