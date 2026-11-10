//go:build tier1

// Suite 0031 — THE REGISTER: A SOCIETY'S MEMBERS, REGISTERED BY ITS SECRETARY.
// Written before the migration (2026-11-10; the reviewer's rulings of the
// same day, §12: members arrive by import from the society's own register,
// and the secretary adds whoever joins next month).
//
//   WHO: the society's current secretary, on their own step-up (act
//   member_register), in the registration's transaction. Nobody else.
//   WHAT: a name; the society's own member number, once; a phone, +234 E.164
//   (sign-in is by phone: required); one of the society's branches; a join
//   date not in the future; next of kin whole or absent.
//   HOW: ACTIVE (the register is the society's own record), their own person,
//   attributed to the act; no PIN and no device (they activate by slip).
//   A batch: the society's reference once.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

type rworld struct {
	s                       *soc
	secretary, treasurer    string // users holding those posts today
	pastSecretary           string // held the post, no longer
	elsewhereSecretary      string // the secretary of another society
}

func newRegisterWorld(t *testing.T) *rworld {
	t.Helper()
	w := &rworld{s: newSociety(t)}
	w.secretary = w.s.user("Secretary")
	fixture(t, w.s.appoint(w.s.member(w.secretary), "secretary", false, "2020-01-01", ""), "the secretary")
	w.treasurer = w.s.user("Treasurer")
	fixture(t, w.s.appoint(w.s.member(w.treasurer), "treasurer", true, "2020-01-01", ""), "the treasurer")
	w.pastSecretary = w.s.user("Secretary until last year")
	fixture(t, w.s.appoint(w.s.member(w.pastSecretary), "secretary", false, "2018-01-01", "2025-12-31"), "the past secretary")
	other := newSociety(t)
	w.elsewhereSecretary = other.user("Secretary elsewhere")
	fixture(t, other.appoint(other.member(w.elsewhereSecretary), "secretary", false, "2020-01-01", ""), "a secretary elsewhere")
	return w
}

type newMember struct {
	number, name, phone, branch, joined, channel string
	nokName, nokPhone, nokRel                    string
}

func aNewMember() newMember {
	n := seq.Add(1)
	return newMember{number: fmt.Sprintf("REG-%d", n), name: "Ngozi Okafor", phone: fmt.Sprintf("+23480%08d", n%100000000),
		branch: "HQ", joined: "2019-04-01", channel: "payroll", nokName: "Emeka Okafor", nokPhone: fmt.Sprintf("+23481%08d", n%100000000), nokRel: "husband"}
}

// registerBy: `actor` takes a step-up for `kind` (act), and in one
// transaction writes the act and registers m. act "none": no act; actID: an
// act from elsewhere.
func (w *rworld) registerBy(t *testing.T, actor string, m newMember, kind, actID string) (string, error) {
	t.Helper()
	if kind == "" {
		kind = "member_register"
	}
	var proof string
	if kind != "none" && actID == "" {
		proof = stepUp(t, actor, kind)
	}
	var id string
	err := tx(func(x pgx.Tx) error {
		act := actID
		if kind != "none" && act == "" {
			if err := x.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ($1, $2, $3) RETURNING id`, kind, actor, proof).Scan(&act); err != nil {
				return fmt.Errorf("FIXTURE: the act: %w", err)
			}
		}
		return x.QueryRow(bg(), `SELECT register_member($1, $2, $3, $4, $5, $6::date, $7::collection_channel, $8, $9, $10, $11, NULL, NULL)::text`,
			w.s.id, m.branch, m.number, m.name, m.phone, m.joined, m.channel, nz(m.nokName), nz(m.nokPhone), nz(m.nokRel), nz(act)).Scan(&id)
	})
	return id, err
}

func (w *rworld) register(t *testing.T, m newMember) (string, error) {
	t.Helper()
	return w.registerBy(t, w.secretary, m, "", "")
}

func nobodyRegistered(t *testing.T, w *rworld, m newMember) {
	t.Helper()
	var n int
	fixture(t, db.QueryRow(bg(), `SELECT count(*) FROM members WHERE society_id = $1 AND member_number = $2`, w.s.id, m.number).Scan(&n), "members")
	if n != 0 {
		t.Fatalf("WRITTEN — a refused registration left %d member", n)
	}
}

// =============================================================================
// WHO
// =============================================================================

func TestRegister01_OnlyTheSocietysCurrentSecretaryRegisters(t *testing.T) {
	w := newRegisterWorld(t)
	for _, c := range []struct{ who, what string }{
		{w.treasurer, "the treasurer"}, {w.pastSecretary, "last year's secretary"}, {w.elsewhereSecretary, "another society's secretary"},
	} {
		m := aNewMember()
		_, err := w.registerBy(t, c.who, m, "", "")
		refused(t, err, c.what+" registering a member", refusedAs("member_registered_by_secretary"))
		nobodyRegistered(t, w, m)
	}
}

func TestRegister02_ItRestsOnTheSecretarysOwnActFromThisTransaction(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	_, err := w.registerBy(t, w.secretary, m, "none", "")
	refused(t, err, "a member registered on no act", refusedAs("member_register_act_kind"))
	_, err = w.registerBy(t, w.secretary, m, "ledger_batch", "")
	refused(t, err, "a member registered on another kind of act", refusedAs("member_register_act_kind"))
	// An act from a transaction already committed.
	proof := stepUp(t, w.secretary, "member_register")
	var old string
	fixture(t, db.QueryRow(bg(), `INSERT INTO credential_acts (kind, actor_user_id, proof_id) VALUES ('member_register', $1, $2) RETURNING id`, w.secretary, proof).Scan(&old), "an earlier act")
	_, err = w.registerBy(t, w.secretary, m, "", old)
	refused(t, err, "a member registered on an act from another transaction", refusedAs("member_register_act_same_transaction"))
	nobodyRegistered(t, w, m)
}

// =============================================================================
// WHAT
// =============================================================================

func TestRegister03_APhoneIsNigerianE164(t *testing.T) {
	w := newRegisterWorld(t)
	for _, p := range []string{"08031234567", "+2348031", "+447911123456", ""} {
		m := aNewMember()
		m.phone = p
		_, err := w.register(t, m)
		refused(t, err, fmt.Sprintf("a member's phone %q", p), refusedAs("member_phone_e164"))
		nobodyRegistered(t, w, m)
	}
}

func TestRegister04_ABranchIsTheSocietysOwn(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	m.branch = "NOWHERE"
	_, err := w.register(t, m)
	refused(t, err, "a member in a branch the society does not have", refusedAs("member_branch_known"))
	nobodyRegistered(t, w, m)
}

func TestRegister05_JoinedIsNotInTheFuture(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	m.joined = "2099-01-01"
	_, err := w.register(t, m)
	refused(t, err, "a member who joins in 2099", refusedAs("member_joined_not_future"))
	nobodyRegistered(t, w, m)
}

func TestRegister06_NextOfKinIsWholeOrAbsent(t *testing.T) {
	w := newRegisterWorld(t)
	for _, set := range []func(*newMember){
		func(m *newMember) { m.nokPhone = "" }, func(m *newMember) { m.nokRel = " " }, func(m *newMember) { m.nokName = "" },
		func(m *newMember) { m.nokPhone = "0803 123 4567" },
	} {
		m := aNewMember()
		set(&m)
		_, err := w.register(t, m)
		refused(t, err, "a next of kin in part, or with a phone not in +234 form", refusedAs("member_next_of_kin_whole"))
		nobodyRegistered(t, w, m)
	}
}

func TestRegister07_ANameIsGiven(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	m.name = "  "
	_, err := w.register(t, m)
	refused(t, err, "a member with no name", refusedAs("member_name_given"))
	nobodyRegistered(t, w, m)
}

// =============================================================================
// HOW
// =============================================================================

func TestRegister08_ARegisteredMemberIsActiveTheirOwnPersonAndAttributed(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	id, err := w.register(t, m)
	accepted(t, err, "the secretary registering a member")
	var status, name, nok, actor string
	var person, pin, device bool
	fixture(t, db.QueryRow(bg(), `SELECT m.status::text, u.full_name, coalesce(m.next_of_kin_name || '|' || m.next_of_kin_phone || '|' || m.next_of_kin_relationship, ''),
		  coalesce(a.actor_user_id::text, ''), m.user_id IS NOT NULL,
		  EXISTS (SELECT 1 FROM auth_credentials c WHERE c.user_id = m.user_id), EXISTS (SELECT 1 FROM auth_devices d WHERE d.user_id = m.user_id)
		FROM members m LEFT JOIN users u ON u.id = m.user_id LEFT JOIN credential_acts a ON a.id = m.registered_by_act
		WHERE m.id = $1`, id).Scan(&status, &name, &nok, &actor, &person, &pin, &device), "the member")
	switch {
	case status != "active":
		t.Fatalf("PENDING — a member from the society's own register is %s; want active", status)
	case !person || name != m.name:
		t.Fatalf("NOBODY — the member's person is %v %q; want their own, named %q", person, name, m.name)
	case nok != m.nokName+"|"+m.nokPhone+"|"+m.nokRel:
		t.Fatalf("LOST — next of kin reads %q", nok)
	case actor != w.secretary:
		t.Fatalf("UNATTRIBUTED — registered by %q; want the secretary's act", actor)
	case pin || device:
		t.Fatalf("ENROLLED — a new member has a PIN (%v) or a device (%v); they activate by slip", pin, device)
	}
}

func TestRegister09_ABatchIsTheSocietysReferenceOnce(t *testing.T) {
	w := newRegisterWorld(t)
	ref := "REGFILE-" + digest()[:16]
	ins := func() error {
		return one(`INSERT INTO member_register_batches (society_id, reference, row_count) VALUES ($1, $2, 1)`, w.s.id, ref)
	}
	fixture(t, ins(), "a register batch")
	refused(t, ins(), "one file's reference registered twice", uniqueOrCheck("member_register_batch_once"))
}

func TestRegister_Control_AMemberWithNoNextOfKin(t *testing.T) {
	w := newRegisterWorld(t)
	m := aNewMember()
	m.nokName, m.nokPhone, m.nokRel = "", "", ""
	_, err := w.register(t, m)
	accepted(t, err, "a member from an old ledger book, with no next of kin")
}
