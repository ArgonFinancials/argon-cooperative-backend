//go:build tier1

// ACCEPTANCE TESTS — 0040, A MEMBER IS PAID INTO THEIR OWN BANK ACCOUNT
// (2026-11-25, the reviewer, overturning the 2026-11-10 ruling: a payout into
// a member's VFD sub-account is swept back into Argon's pool).
//
//   - the member sets their details on their own step-up; an officer (not
//     the auditor) only for a member who cannot sign in; the database says
//     who, and whether it was the member; never edited;
//   - VFD's answer is recorded once, found with a name or not found without
//     one; whether the name resembles the register is the database's;
//   - a name that differs is confirmed once, by a current signatory who
//     neither set the details nor is the member, on their own act, with what
//     they looked at;
//   - settled: found, and matching or confirmed; the latest set is current;
//   - in a society on VFD: no details, no request; the hold needs the current
//     details checked, found and settled; whoever set them never signs;
//   - a transfer goes to the current settled account, at its bank.
package tier1

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// bankMember: a member of w's society, on the register as `name`, with
// savings; a PIN (they can sign in) or none.
func bankMember(t *testing.T, w *mworld, name string, pin bool) (user, member string) {
	t.Helper()
	user = w.c.s.user(name)
	fixture(t, db.QueryRow(bg(), `INSERT INTO members (society_id, branch_id, member_number, full_name, phone, status, collection_channel, joined_on, user_id)
		VALUES ($1, $2, $3, $4, $5, 'pending', 'cash', '2020-01-01', $6) RETURNING id`,
		w.c.s.id, w.c.s.branch, fmt.Sprintf("B-%d", seq.Add(1)), name, fmt.Sprintf("+23471%08d", seq.Add(1)), user).Scan(&member), "the member")
	if pin {
		fixture(t, one(`INSERT INTO auth_credentials (user_id, pin_hash) VALUES ($1, $2)`, user, hashPIN("4827")), "the member's PIN")
	}
	w.saved(t, member, loanSavings)
	return
}

type bankSet struct {
	actor, member, number, bank string
	kind                        string // "" = member_bank_account
	apart                       bool   // the act in a transaction of its own
	claimSetter                 string // a set_by_user the writer supplies
}

// setBank: the actor's act and the details, in one transaction (unless apart).
func setBank(t *testing.T, s bankSet) (string, error) {
	t.Helper()
	if s.kind == "" {
		s.kind = "member_bank_account"
	}
	if s.bank == "" {
		s.bank = testBank
	}
	if s.number == "" {
		s.number = vfdNumber()
	}
	proof := stepUp(t, s.actor, s.kind)
	var act, id string
	mk := func(x pgx.Tx) error {
		return x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, s.kind, s.actor, proof).Scan(&act)
	}
	rec := func(x pgx.Tx) error {
		if s.claimSetter != "" {
			return x.QueryRow(bg(), `INSERT INTO member_bank_accounts (member_id, account_number, bank_code, set_by_act, set_by_user, by_member)
				VALUES ($1, $2, $3, $4, $5, true) RETURNING id::text`, s.member, s.number, s.bank, act, s.claimSetter).Scan(&id)
		}
		return x.QueryRow(bg(), `INSERT INTO member_bank_accounts (member_id, account_number, bank_code, set_by_act) VALUES ($1, $2, $3, $4) RETURNING id::text`,
			s.member, s.number, s.bank, act).Scan(&id)
	}
	if s.apart {
		if err := tx(mk); err != nil {
			return "", err
		}
		return id, tx(rec)
	}
	return id, tx(func(x pgx.Tx) error {
		if err := mk(x); err != nil {
			return err
		}
		return rec(x)
	})
}

func mustSetBank(t *testing.T, s bankSet) string {
	t.Helper()
	id, err := setBank(t, s)
	fixture(t, err, "the bank details")
	return id
}

// vfdSaid: VFD's answer on a set of details, as the wallet records it.
func vfdSaid(bankAccount string, found bool, name, claimed string) error {
	return one(`INSERT INTO bank_name_checks (bank_account_id, found, vfd_code, vfd_name, name_check) VALUES ($1, $2, $3, $4, $5)`,
		bankAccount, found, map[bool]string{true: "00", false: "104"}[found], nz(name), nz(claimed))
}

// confirmName: the actor confirms the name on a set of details.
func confirmName(t *testing.T, actor, bankAccount, reason, kind string) error {
	t.Helper()
	if kind == "" {
		kind = "bank_name_confirmation"
	}
	proof := stepUp(t, actor, kind)
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO bank_name_confirmations (bank_account_id, act_id, reason) VALUES ($1, $2, $3)`, bankAccount, act, reason)
		return err
	})
}

const lookedAt = "the member showed their bank statement in person"

// signHold: the chairman's signature reaching quorum, carrying the hold for
// `member` (the treasurer signed first).
func signHold(t *testing.T, w *mworld, req, member string) error {
	t.Helper()
	user := userOf(t, w.s2Mem)
	proof := stepUp(t, user, "approval_signature")
	return tx(func(x pgx.Tx) error {
		var act string
		if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('approval_signature', $1, $2) RETURNING id`, user, proof).Scan(&act); err != nil {
			return err
		}
		if _, err := x.Exec(bg(), `INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $2, 'chairman', $3)`, req, w.s2Mem, act); err != nil {
			return err
		}
		_, err := x.Exec(bg(), `INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, actor_user_id, act_id)
			VALUES ($1, $2, $3, $4, $5, $6)`, req, w.c.s.id, member, payKobo, user, act)
		return err
	})
}

// firstSigned: a withdrawal for `member`, raised by them, signed by the treasurer.
func firstSigned(t *testing.T, w *mworld, member string) string {
	t.Helper()
	req, err := w.raise(member, payKobo, 2)
	fixture(t, err, "the withdrawal request")
	fixture(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the first signature")
	return req
}

type bankRow struct {
	byMember      bool
	setBy, number string
	checked       bool
	settled       bool
	check         string
}

func currentBank(t *testing.T, member string) bankRow {
	t.Helper()
	var r bankRow
	fixture(t, db.QueryRow(bg(), `SELECT by_member, set_by_user::text, account_number, checked, name_settled, coalesce(name_check, '')
		FROM member_bank_current WHERE member_id = $1`, member).Scan(&r.byMember, &r.setBy, &r.number, &r.checked, &r.settled, &r.check), "the member's current details")
	return r
}

func nameCheckOf(t *testing.T, bankAccount string) string {
	t.Helper()
	var c string
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(name_check, '') FROM bank_name_checks WHERE bank_account_id = $1`, bankAccount).Scan(&c), "the name check")
	return c
}

func TestBankRules01_TheDatabaseSaysWhoSetThem(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	mustSetBank(t, bankSet{actor: user, member: mem})
	if r := currentBank(t, mem); !r.byMember || r.setBy != user {
		t.Fatalf("WRONG SETTER — the member set their own: by_member %v, set by %s; want true, by %s", r.byMember, r.setBy, user)
	}
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	mustSetBank(t, bankSet{actor: w.s1, member: noPin})
	if r := currentBank(t, noPin); r.byMember || r.setBy != w.s1 {
		t.Fatalf("WRONG SETTER — an officer set them for a member who cannot sign in: by_member %v, set by %s; want false, by the officer %s", r.byMember, r.setBy, w.s1)
	}
	// The writer's word on who is not taken.
	mustSetBank(t, bankSet{actor: user, member: mem, claimSetter: w.s2})
	if r := currentBank(t, mem); r.setBy != user {
		t.Fatalf("THE WRITER'S WORD — set by %s, recorded as set by %s; the act's actor is the setter", user, r.setBy)
	}
}

func TestBankRules02_OnTheSettersOwnActTakenWithThem(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	_, err := setBank(t, bankSet{actor: user, member: mem, kind: "member_status"})
	refused(t, err, "bank details set on another kind of act", refusedAs("bank_account_act"))
	_, err = setBank(t, bankSet{actor: user, member: mem, apart: true})
	refused(t, err, "bank details set on an act taken in another transaction", refusedAs("bank_account_act"))
}

func TestBankRules03_AnOfficerOnlyForAMemberWhoCannotSignIn(t *testing.T) {
	w := newWalletWorld(t)
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	other, _ := bankMember(t, w.mworld, "Chidi Eze", true)
	_, err := setBank(t, bankSet{actor: other, member: noPin})
	refused(t, err, "a member who is no officer setting another member's bank details", refusedAs("bank_account_by_member"))
	auditor, auditorMem := bankMember(t, w.mworld, "Ngozi Audit", true)
	fixture(t, w.c.s.appoint(auditorMem, "internal_auditor", false, "2020-01-01", ""), "the auditor")
	_, err = setBank(t, bankSet{actor: auditor, member: noPin})
	refused(t, err, "the internal auditor setting a member's bank details", refusedAs("bank_account_by_member"))
	_, withPin := bankMember(t, w.mworld, "Folake Adebayo", true)
	_, err = setBank(t, bankSet{actor: w.s1, member: withPin})
	refused(t, err, "an officer setting the bank details of a member who can sign in", refusedAs("bank_account_member_can_sign_in"))
	_, err = setBank(t, bankSet{actor: w.s1, member: noPin})
	accepted(t, err, "an officer setting them for a member who cannot sign in")
}

func TestBankRules04_DetailsAreNeverEdited(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	id := mustSetBank(t, bankSet{actor: user, member: mem})
	refused(t, one(`UPDATE member_bank_accounts SET account_number = '0123456789' WHERE id = $1`, id), "a member's account number rewritten", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM member_bank_accounts WHERE id = $1`, id), "a member's bank details deleted", refusedAs("vfd_records_append_only"))
}

func TestBankRules05_TheDatabaseJudgesTheName(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	same := mustSetBank(t, bankSet{actor: user, member: mem})
	accepted(t, vfdSaid(same, true, "ADEBAYO FOLAKE", ""), "VFD's name for the account")
	if c := nameCheckOf(t, same); c != "matches" {
		t.Fatalf("WRONG JUDGEMENT — ADEBAYO FOLAKE for Folake Adebayo is %q; want matches", c)
	}
	other := mustSetBank(t, bankSet{actor: user, member: mem})
	accepted(t, vfdSaid(other, true, "OKORO JAMES", "matches"), "VFD's name, with the writer's word that it matches")
	if c := nameCheckOf(t, other); c != "differs" {
		t.Fatalf("THE WRITER'S WORD — OKORO JAMES for Folake Adebayo is %q; want differs, whatever the writer says", c)
	}
}

func TestBankRules06_TwoWordsInCommonOrOneForAOneWordName(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Grace Okoro", true)
	for _, c := range []struct{ name, want string }{
		{"ADEBAYO FOLAKE GRACE", "differs"}, // one word in common, both names of more than one
		{"OKORO-GRACE", "matches"},          // both, any order, punctuation aside
		{"GRACE", "matches"},                // a one-word name: one in common
	} {
		id := mustSetBank(t, bankSet{actor: user, member: mem})
		accepted(t, vfdSaid(id, true, c.name, ""), "VFD's name "+c.name)
		if got := nameCheckOf(t, id); got != c.want {
			t.Fatalf("WRONG JUDGEMENT — %s for Grace Okoro is %q; want %s", c.name, got, c.want)
		}
	}
}

func TestBankRules07_FoundHasANameNotFoundHasNone(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	id := mustSetBank(t, bankSet{actor: user, member: mem})
	refused(t, vfdSaid(id, true, "", ""), "found with no name", refusedAs("bank_name_when_found"))
	refused(t, vfdSaid(id, false, "ADEBAYO FOLAKE", ""), "not found, with a name", refusedAs("bank_name_when_found"))
}

func TestBankRules08_OneAnswerEachNeverEdited(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	id := mustSetBank(t, bankSet{actor: user, member: mem})
	accepted(t, vfdSaid(id, false, "", ""), "VFD's not-found")
	refused(t, vfdSaid(id, true, "ADEBAYO FOLAKE", ""), "a second answer on the same details", want{codes: []string{"23505"}})
	refused(t, one(`UPDATE bank_name_checks SET vfd_code = '00' WHERE bank_account_id = $1`, id), "VFD's answer rewritten", refusedAs("vfd_records_append_only"))
}

func TestBankRules09_ASignatoryWhoNeitherSetNorOwnsThemConfirms(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	differs := mustSetBank(t, bankSet{actor: user, member: mem})
	fixture(t, vfdSaid(differs, true, "OKORO JAMES", ""), "a name that differs")
	refused(t, confirmName(t, w.s1, differs, lookedAt, "approval_signature"), "a confirmation on another kind of act", refusedAs("bank_name_confirmation_act"))
	sec, secMem := bankMember(t, w.mworld, "Sade Secretary", true)
	fixture(t, w.c.s.appoint(secMem, "secretary", false, "2020-01-01", ""), "a secretary who does not sign")
	refused(t, confirmName(t, sec, differs, lookedAt, ""), "an officer who is no signatory confirming", refusedAs("bank_name_confirmed_by_signatory"))
	refused(t, confirmName(t, w.s1, differs, "looked", ""), "a confirmation that does not say what was looked at", refusedAs("bank_name_confirmation_says_why"))

	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	bySetter := mustSetBank(t, bankSet{actor: w.s1, member: noPin})
	fixture(t, vfdSaid(bySetter, true, "OKORO JAMES", ""), "a name that differs")
	refused(t, confirmName(t, w.s1, bySetter, lookedAt, ""), "the officer who set the details confirming their name", refusedAs("bank_name_confirmed_by_another"))

	matches := mustSetBank(t, bankSet{actor: user, member: mem})
	fixture(t, vfdSaid(matches, true, "ADEBAYO FOLAKE", ""), "a name that matches")
	refused(t, confirmName(t, w.s1, matches, lookedAt, ""), "a name that matches the register confirmed", refusedAs("bank_name_confirmation_needed"))
	unchecked := mustSetBank(t, bankSet{actor: user, member: mem})
	refused(t, confirmName(t, w.s1, unchecked, lookedAt, ""), "a name VFD has not given confirmed", refusedAs("bank_name_confirmation_needed"))

	accepted(t, confirmName(t, w.s2, bySetter, lookedAt, ""), "another signatory confirming")
}

func TestBankRules10_ConfirmedOnceNeverEdited(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	id := mustSetBank(t, bankSet{actor: user, member: mem})
	fixture(t, vfdSaid(id, true, "OKORO JAMES", ""), "a name that differs")
	accepted(t, confirmName(t, w.s1, id, lookedAt, ""), "the confirmation")
	refused(t, confirmName(t, w.s2, id, lookedAt, ""), "a second confirmation", want{codes: []string{"23505"}})
	refused(t, one(`UPDATE bank_name_confirmations SET reason = 'rewritten afterwards' WHERE bank_account_id = $1`, id), "a confirmation rewritten", refusedAs("vfd_records_append_only"))
}

func TestBankRules11_SettledMeansFoundAndMatchingOrConfirmed(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	unchecked := mustSetBank(t, bankSet{actor: user, member: mem})
	if r := currentBank(t, mem); r.settled {
		t.Fatalf("SETTLED UNCHECKED — %+v; VFD has not answered", r)
	}
	fixture(t, vfdSaid(unchecked, false, "", ""), "not found")
	if r := currentBank(t, mem); r.settled {
		t.Fatalf("SETTLED NOT FOUND — %+v; VFD says there is no such account", r)
	}
	differs := mustSetBank(t, bankSet{actor: user, member: mem})
	fixture(t, vfdSaid(differs, true, "OKORO JAMES", ""), "a name that differs")
	if r := currentBank(t, mem); r.settled {
		t.Fatalf("SETTLED UNCONFIRMED — %+v; a name that differs waits for a signatory", r)
	}
	fixture(t, confirmName(t, w.s1, differs, lookedAt, ""), "the confirmation")
	if r := currentBank(t, mem); !r.settled {
		t.Fatalf("NOT SETTLED — %+v; confirmed by a signatory", r)
	}
	number := vfdNumber()
	latest := mustSetBank(t, bankSet{actor: user, member: mem, number: number})
	fixture(t, vfdSaid(latest, true, "ADEBAYO FOLAKE", ""), "a name that matches")
	if r := currentBank(t, mem); !r.settled || r.number != number {
		t.Fatalf("NOT CURRENT — %+v; want the latest set, %s, settled (its name matches)", r, number)
	}
}

func TestBankRules12_NoBankDetailsNoRequest(t *testing.T) {
	w := newWalletWorld(t)
	user, mem := bankMember(t, w.mworld, "Folake Adebayo", true)
	_, err := w.raise(mem, payKobo, 2)
	refused(t, err, "a withdrawal asked for with no bank details, in a society on VFD", refusedAs("payout_needs_bank_account"))
	mustSetBank(t, bankSet{actor: user, member: mem})
	_, err = w.raise(mem, payKobo, 2)
	accepted(t, err, "a withdrawal asked for once the details are recorded (checked or not)")
	m := newMoneyWorld(t) // a society not on VFD: the officers pay by hand
	_, err = m.raise(m.bMem, payKobo, 2)
	accepted(t, err, "a withdrawal asked for in a society not on VFD")
}

// holdSentences: the hold's refusals over bank details, each a sentence the
// member reads and acts on (add an account, wait, add it again, ask an officer).
var holdSentences = map[string]bool{"payout_needs_bank_account": true, "payout_bank_not_checked": true,
	"payout_bank_not_found": true, "payout_bank_name_unconfirmed": true}

// refusedSaying: refused with the sentence for THIS state. The money is held
// whichever of the hold's sentences refuses it; the defect a wrong one makes
// is the member told the wrong thing to do, and it is named as that.
func refusedSaying(t *testing.T, err error, what, code string) {
	t.Helper()
	var pe *pgconn.PgError
	if errors.As(err, &pe) && holdSentences[pe.ConstraintName] && pe.ConstraintName != code {
		t.Fatalf("WRONG SENTENCE — %s: the member is told %q (%s); want %s", what, pe.Message, pe.ConstraintName, code)
	}
	refused(t, err, what, refusedAs(code))
}

func TestBankRules13_TheHoldNeedsSettledDetails(t *testing.T) {
	w := newMoneyWorld(t)
	req := firstSigned(t, w, w.bMem) // asked for before the society was on VFD
	_, err := recordSocietyAccount(w.c.s.id, vfdNumber(), opName, opHost, socAcctReason)
	fixture(t, err, "the society's VFD account")
	refusedSaying(t, signHold(t, w, req, w.bMem), "the hold with no bank details", "payout_needs_bank_account")
	unchecked := mustSetBank(t, bankSet{actor: w.borrower, member: w.bMem})
	refusedSaying(t, signHold(t, w, req, w.bMem), "the hold before VFD answered", "payout_bank_not_checked")
	fixture(t, vfdSaid(unchecked, false, "", ""), "not found")
	refusedSaying(t, signHold(t, w, req, w.bMem), "the hold on an account VFD says does not exist", "payout_bank_not_found")
	differs := mustSetBank(t, bankSet{actor: w.borrower, member: w.bMem})
	fixture(t, vfdSaid(differs, true, "OKORO JAMES", ""), "a name that differs")
	refusedSaying(t, signHold(t, w, req, w.bMem), "the hold on a name nobody confirmed", "payout_bank_name_unconfirmed")
	fixture(t, confirmName(t, w.s1, differs, lookedAt, ""), "the confirmation")
	accepted(t, signHold(t, w, req, w.bMem), "the hold, the details settled")
}

func TestBankRules14_WhoeverSetThemNeverSigns(t *testing.T) {
	w := newWalletWorld(t)
	_, noPin := bankMember(t, w.mworld, "Bola Okoro", false)
	id := mustSetBank(t, bankSet{actor: w.s1, member: noPin}) // the treasurer set them
	fixture(t, vfdSaid(id, true, "OKORO JAMES", ""), "a name that differs")
	fixture(t, confirmName(t, w.s2, id, lookedAt, ""), "the chairman's confirmation")
	req, err := w.raise(noPin, payKobo, 2)
	fixture(t, err, "the withdrawal request")
	refused(t, w.c.s.sign(req, w.s1Mem, "treasurer"), "the officer who set the member's bank details signing their withdrawal", refusedAs("bank_setter_authorises"))
	// The member's own details bind nobody.
	own, err := w.raise(w.bMem, payKobo, 2)
	fixture(t, err, "the borrower's request")
	accepted(t, w.c.s.sign(own, w.s1Mem, "treasurer"), "a signature on a withdrawal whose details the member set")
}

func TestBankRules15_ATransferGoesToTheCurrentSettledAccountAtItsBank(t *testing.T) {
	w := newWalletWorld(t)
	hold := w.lastSignatureHold(t)
	send := func(to, bank string) error {
		return tx(func(x pgx.Tx) error {
			_, err := x.Exec(bg(), `INSERT INTO vfd_transfers (hold_id, reference, from_account, to_account, amount_kobo, to_bank)
				VALUES ($1, vfd_transfer_reference($1), $2, $3, $4, $5)`, hold, ensurePool(), to, payKobo, bank)
			return err
		})
	}
	refused(t, send(w.memAcct, "000014"), "a transfer to the member's account number at another bank", refusedAs("vfd_transfer_to_members_bank"))
	number := vfdNumber()
	fresh := mustSetBank(t, bankSet{actor: w.borrower, member: w.bMem, number: number})
	refused(t, send(number, testBank), "a transfer to details VFD has not answered on", refusedAs("vfd_transfer_to_members_bank"))
	refused(t, send(w.memAcct, testBank), "a transfer to details no longer current", refusedAs("vfd_transfer_to_members_bank"))
	fixture(t, vfdSaid(fresh, true, "OKORO JAMES", ""), "a name that differs")
	fixture(t, confirmName(t, w.s1, fresh, lookedAt, ""), "the confirmation")
	accepted(t, send(number, testBank), "a transfer to the current settled account, at its bank")
}

func TestBankRules_Control_ASettledMembersWithdrawalIsHeld(t *testing.T) {
	w := newWalletWorld(t) // the borrower's details settled (bankFor)
	req := firstSigned(t, w.mworld, w.bMem)
	accepted(t, signHold(t, w.mworld, req, w.bMem), "the hold for a member whose details are settled")
}
