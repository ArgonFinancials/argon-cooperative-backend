//go:build tier1

// ACCEPTANCE TESTS — A MEMBER'S BANK DETAILS OVER HTTP (0040, 2026-11-25).
// The member sets their own, on their own PIN: PUT /members/{m}/bank-account.
// An officer only for a member who cannot sign in: PUT
// /societies/{s}/members/{m}/bank-account. The answer says "checking": the
// wallet asks VFD, never this server. A signatory who neither set them nor is
// the member confirms a name that differs: POST .../bank-account/confirmation.
// GET /members/{m}/bank-account: the member and their society's officers.
// A withdrawal with no bank details is refused in the member's own words,
// when asked for and at the last signature; whoever set the details never
// signs.
package tier1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type bankOut struct {
	BankAccountId, AccountNumber, BankCode, Check, RecordedAt string
	VfdName                                                   *string
	NameSettled, ByMember                                     bool
	SetBy                                                     struct{ UserId, Name string }
	ConfirmedBy                                               *struct{ UserId, Name string }
}

type bankRecordOut struct {
	Current  *bankOut
	History  []bankOut
	Required bool
}

func (w *mapi) setOwnBank(t *testing.T, as, member, number, bank, proof string) resp {
	t.Helper()
	if proof == "" {
		proof = stepUp(t, as, "member_bank_account")
	}
	return w.call(t, http.MethodPut, as, "/members/"+member+"/bank-account", map[string]string{"accountNumber": number, "bankCode": bank, "pinProofId": proof})
}

func (w *mapi) setMemberBank(t *testing.T, as, member, number string) resp {
	t.Helper()
	return w.call(t, http.MethodPut, as, "/societies/"+w.c.s.id+"/members/"+member+"/bank-account",
		map[string]string{"accountNumber": number, "bankCode": testBank, "pinProofId": stepUp(t, as, "member_bank_account")})
}

func (w *mapi) confirmBank(t *testing.T, as, member, reason string) resp {
	t.Helper()
	return w.call(t, http.MethodPost, as, "/societies/"+w.c.s.id+"/members/"+member+"/bank-account/confirmation",
		map[string]string{"reason": reason, "pinProofId": stepUp(t, as, "bank_name_confirmation")})
}

func (w *mapi) bankRecord(t *testing.T, as, member string) bankRecordOut {
	t.Helper()
	r := w.call(t, http.MethodGet, as, "/members/"+member+"/bank-account", nil)
	var out bankRecordOut
	if r.status != http.StatusOK || json.Unmarshal(r.body, &out) != nil {
		t.Fatalf("NOT READ — the member's bank details: %d %s", r.status, r.body)
	}
	return out
}

func bankFrom(t *testing.T, r resp, status int, what string) bankOut {
	t.Helper()
	var b bankOut
	if r.status != status || json.Unmarshal(r.body, &b) != nil {
		t.Fatalf("REFUSED — %s: %d %s; want %d", what, r.status, r.body, status)
	}
	return b
}

// bankIDOf: the member's current details' id, as recorded.
func bankIDOf(t *testing.T, member string) string {
	t.Helper()
	var id string
	fixture(t, db.QueryRow(bg(), `SELECT id::text FROM member_bank_current WHERE member_id = $1`, member).Scan(&id), "the member's current details")
	return id
}

func TestApiBank01_TheMemberSetsTheirOwnAndItIsBeingChecked(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	b := bankFrom(t, w.setOwnBank(t, user, mem, "0123456789", testBank, ""), http.StatusOK, "the member setting their own bank details")
	if b.AccountNumber != "0123456789" || b.BankCode != testBank || !b.ByMember || b.SetBy.UserId != user || b.SetBy.Name != "Folake Adebayo" {
		t.Fatalf("WRONG RECORD — %+v; want 0123456789 at %s, set by the member themselves", b, testBank)
	}
	if b.Check != "checking" || b.VfdName != nil || b.NameSettled {
		t.Fatalf("SAID TOO SOON — %+v; VFD has not answered: checking, no name, not settled", b)
	}
	if rec := w.bankRecord(t, user, mem); rec.Current == nil || rec.Current.BankAccountId != b.BankAccountId {
		t.Fatalf("NOT SHOWN — the member reads %+v; want the details just set as current", rec)
	}
}

// A refusal spends nothing: the same proof then records the details.
func TestApiBank02_OnlyTheirOwnAndTheShapeFirst(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	other, _ := bankMember(t, w.mworld, "Chidi Eze", true)
	if r := w.setOwnBank(t, other, mem, "0123456789", testBank, ""); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another member setting this member's bank details: %d %s; want 404", r.status, r.body)
	}
	proof := stepUp(t, user, "member_bank_account")
	wantProblem(t, w.setOwnBank(t, user, mem, "12345", testBank, proof), http.StatusUnprocessableEntity, "bad_account", "an account number that is not ten digits")
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456789", testBank, proof), http.StatusOK, "the same proof, once the number is right")
}

func TestApiBank03_AnOfficerForAMemberWhoCannotSignIn(t *testing.T) {
	w := newMoneyAPI(t)
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	b := bankFrom(t, w.setMemberBank(t, w.s1, noPin, "0123456789"), http.StatusOK, "the treasurer setting the details of a member who cannot sign in")
	if b.ByMember || b.SetBy.UserId != w.s1 {
		t.Fatalf("WRONG SETTER — %+v; want set by the treasurer, not the member", b)
	}
	_, withPin := bankMember(t, w.mworld, "Folake Adebayo", true)
	wantProblem(t, w.setMemberBank(t, w.s1, withPin, "0123456789"), http.StatusForbidden, "member_sets_own", "an officer setting the details of a member who can sign in")
	if r := w.setMemberBank(t, w.abroad, noPin, "0123456789"); r.status != http.StatusNotFound {
		t.Fatalf("TOLD — another society's officer: %d %s; want 404", r.status, r.body)
	}
}

func TestApiBank04_ASignatoryConfirmsANameThatDiffers(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456789", testBank, ""), http.StatusOK, "the details")
	fixture(t, vfdSaid(bankIDOf(t, mem), true, "OKORO JAMES", ""), "VFD's name, which differs")
	cur := w.bankRecord(t, user, mem).Current
	if cur == nil || cur.Check != "differs" || cur.VfdName == nil || *cur.VfdName != "OKORO JAMES" || cur.NameSettled {
		t.Fatalf("WRONG STATE — %+v; want differs, VFD's name OKORO JAMES, not settled until a signatory confirms", cur)
	}
	wantProblem(t, w.confirmBank(t, w.s2, mem, "ok"), http.StatusUnprocessableEntity, "reason_required", "a confirmation that does not say what was looked at")
	b := bankFrom(t, w.confirmBank(t, w.s2, mem, lookedAt), http.StatusCreated, "the chairman confirming")
	if !b.NameSettled || b.ConfirmedBy == nil || b.ConfirmedBy.UserId != w.s2 {
		t.Fatalf("NOT SETTLED — %+v; want settled, confirmed by the chairman", b)
	}
	wantProblem(t, w.confirmBank(t, w.s1, mem, lookedAt), http.StatusConflict, "nothing_to_confirm", "a second confirmation")
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456780", testBank, ""), http.StatusOK, "new details")
	fixture(t, vfdSaid(bankIDOf(t, mem), true, "ADEBAYO FOLAKE", ""), "VFD's name, which matches")
	wantProblem(t, w.confirmBank(t, w.s1, mem, lookedAt), http.StatusConflict, "nothing_to_confirm", "a name that matches the register")
}

func TestApiBank05_WhoeverSetThemNeverConfirms(t *testing.T) {
	w := newMoneyAPI(t)
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	bankFrom(t, w.setMemberBank(t, w.s1, noPin, "0123456789"), http.StatusOK, "the treasurer setting the details")
	fixture(t, vfdSaid(bankIDOf(t, noPin), true, "OKORO JAMES", ""), "VFD's name, which differs")
	wantProblem(t, w.confirmBank(t, w.s1, noPin, lookedAt), http.StatusForbidden, "conflict_of_interest", "the treasurer confirming the name on details they set")
}

func TestApiBank06_TheMemberAndTheirOfficersReadNewestFirst(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456789", testBank, ""), http.StatusOK, "the first details")
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456780", testBank, ""), http.StatusOK, "the second details")
	rec := w.bankRecord(t, w.auditor, mem)
	if rec.Current == nil || rec.Current.AccountNumber != "0123456780" || len(rec.History) != 2 || rec.History[0].AccountNumber != "0123456780" {
		t.Fatalf("WRONG ORDER — %+v; want the latest (0123456780) current, and both, newest first", rec)
	}
	strangerUser := userOf(t, w.stranger)
	for _, as := range []string{strangerUser, w.abroad} {
		if r := w.call(t, http.MethodGet, as, "/members/"+mem+"/bank-account", nil); r.status != http.StatusNotFound {
			t.Fatalf("SHOWN — someone outside the society read a member's bank details: %d %s; want 404", r.status, r.body)
		}
	}
}

func TestApiBank07_NoBankDetailsIsRefusedInTheMembersWords(t *testing.T) {
	w := newMoneyAPI(t)
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	r := w.raiseVia(t, user, mem, "600000", stepUp(t, user, "withdrawal_request"), "", nil)
	wantProblem(t, r, http.StatusUnprocessableEntity, "needs_bank_account", "a withdrawal asked for with no bank details")
	if !strings.Contains(string(r.body), "Add your bank account") {
		t.Fatalf("NOT THE MEMBER'S WORDS — %s; want the sentence the member reads", r.body)
	}
}

func TestApiBank08_TheLastSignatureSaysWhatTheMemberMustDo(t *testing.T) {
	w := newMoneyAPI(t)
	req := firstSigned(t, w.mworld, w.bMem) // asked for before the society was on VFD
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	r := w.signVia(t, w.s2, req)
	wantProblem(t, r, http.StatusUnprocessableEntity, "needs_bank_account", "the last signature with no bank details to pay into")
	if !strings.Contains(string(r.body), "Add your bank account") {
		t.Fatalf("NOT THE MEMBER'S WORDS — %s; want the sentence the member reads", r.body)
	}
}

func TestApiBank09_TheSetterNeverSigns(t *testing.T) {
	w := newMoneyAPI(t)
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	bankFrom(t, w.setMemberBank(t, w.s1, noPin, "0123456789"), http.StatusOK, "the treasurer setting the details")
	r := w.raiseVia(t, w.secretary, noPin, "600000", stepUp(t, w.secretary, "withdrawal_request"), "", nil)
	var v reqView
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &v) != nil {
		t.Fatalf("FIXTURE: the secretary raising for the member: %d %s", r.status, r.body)
	}
	wantProblem(t, w.signVia(t, w.s1, v.RequestId), http.StatusForbidden, "conflict_of_interest", "the treasurer signing for a member whose bank details they set")
}

// A hold whose transfer is at VFD is VFD's to settle: the request says so
// (atVfd), and a person is never offered it (2026-11-25, the frontend).
func TestApiBank10_TheRequestSaysWhenItsPaymentIsWithVFD(t *testing.T) {
	w := newMoneyAPI(t)
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	number := vfdNumber()
	bankFor(t, w.bMem, number)
	req := firstSigned(t, w.mworld, w.bMem)
	if r := w.signVia(t, w.s2, req); r.status != http.StatusCreated {
		t.Fatalf("FIXTURE: the last signature, with its hold: %d %s", r.status, r.body)
	}
	p := memberRequestOf(t, w, req).Payout
	if p == nil || p.PlacedBy != "system" || p.AtVfd {
		t.Fatalf("WRONG STATE — before anything is sent the payout reads %+v; want placed by the system, not at VFD (a person may still release it)", p)
	}
	fixture(t, tx(func(x pgx.Tx) error {
		_, err := x.Exec(bg(), `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo, to_bank)
			VALUES ($1, vfd_transfer_reference($1), $2, $3, $4, $5)`, p.HoldId, ensurePool(), number, payKobo, testBank)
		return err
	}), "the transfer")
	if p := memberRequestOf(t, w, req).Payout; p == nil || !p.AtVfd {
		t.Fatalf("UNSAID — the transfer is at VFD and the payout reads %+v; want atVfd true (only VFD's answer settles it)", p)
	}
}

// 0041: an account at VFD is refused in words the member acts on, before
// anything is written (a payout there would never leave the pool).
func TestApiBank11_AnAccountAtVFDIsRefusedInWords(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	proof := stepUp(t, user, "member_bank_account")
	wantProblem(t, w.setOwnBank(t, user, mem, "0123456789", "090110", proof), http.StatusUnprocessableEntity, "bank_at_vfd", "a member's account at VFD's own bank")
	bankFrom(t, w.setOwnBank(t, user, mem, "0123456789", testBank, proof), http.StatusOK, "the same proof, at another bank")
}

// The screen asks for bank details exactly where the rules need them: a
// society on VFD (2026-11-26, the frontend: the warning showed everywhere).
func TestApiBank12_TheRecordSaysWhetherBankDetailsAreNeeded(t *testing.T) {
	w := newMoneyAPI(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	if rec := w.bankRecord(t, user, mem); rec.Required {
		t.Fatalf("ASKED — a society not on VFD reads bank details required; its officers pay by hand")
	}
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society on VFD")
	if rec := w.bankRecord(t, user, mem); !rec.Required {
		t.Fatalf("UNSAID — a society on VFD reads bank details not required; a withdrawal there is refused without them")
	}
}
