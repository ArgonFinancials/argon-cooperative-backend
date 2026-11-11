//go:build tier1

// Suite 0032 — A CONTRIBUTION CLAIMED BY THE MEMBER, CONFIRMED BY VFD. Written
// before the migration (stage 4, 2026-11-11). The reviewer's rulings (§12,
// 2026-11-10 and 2026-11-11): a member pays into their society's VFD account
// and claims it on their own phone; it is posted as their contribution only
// when a VFD inflow confirms the claim; an inflow nobody claimed waits for an
// officer, never credited to a guessed member. A claim names the payment's
// SESSION ID and its AMOUNT. Money from the member's OWN VFD account needs no
// claim: whose it is is certain.
//
//	A CLAIM is the member's own (their person, their society), names a
//	session id and a positive amount, once per member per session. It moves
//	no money.
//	A MATCH posts: the one inflow in the society with that session id, the
//	amount EQUAL, and every claim on that session the same member's, in
//	whichever transaction arrives second (claim or inflow). Anything else
//	posts nothing and is put in front of a person: amounts that differ, two
//	members claiming one payment, a claim on a payment already credited to
//	someone else.
//	FROM THE MEMBER'S OWN ACCOUNT: an inflow whose sender is a member's own
//	VFD account in that society is theirs, claim or none.
//	THE POSTING is the system's, once per inflow, named by the inflow and by
//	no user: the bank up by what VFD confirmed, the member's savings up by
//	that less the series' charge (uncharged and flagged where the series
//	cannot say), dated the day VFD confirmed it. Nobody else can write it.
package tier1

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

type cworld struct {
	*vworld
	other                             *soc // another society, with the same kind of account
	otherAcct, otherMemUser, otherMem string
}

func newClaimWorld(t *testing.T) *cworld {
	t.Helper()
	w := &cworld{vworld: newWalletWorld(t), other: newSociety(t), otherAcct: vfdNumber()}
	_, err := recordSocietyAccount(w.other.id, w.otherAcct, opName, opHost, socAcctReason)
	fixture(t, err, "the other society's VFD account")
	w.otherMemUser = w.other.user("Member elsewhere")
	w.otherMem = w.other.member(w.otherMemUser)
	fixture(t, one(`UPDATE members SET status = 'active' WHERE id = $1`, w.otherMem), "the member elsewhere, active")
	return w
}

// A NIP-style session id: thirty digits, new each call.
func sessionNew() string { return fmt.Sprintf("0000%026d", seq.Add(1)) }

func claim(society, member, user, session string, kobo int64) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO contribution_claims (society_id, member_id, user_id, session_id, amount_kobo)
		VALUES ($1, $2, $3, $4, $5) RETURNING id::text`, society, member, user, nz(session), kobo).Scan(&id)
	return id, err
}

// inflowFrom: VFD's confirmation of money into `account`, with its session id
// and the sending account.
func inflowFrom(society, account, session, fromAcct string, kobo int64) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo, session_id, originator_name, originator_account, originator_bank)
		VALUES ($1, $2, $3, $4, $5, 'ADEBAYO FOLAKE', $6, 'ANY BANK') RETURNING id::text`, vfdRefNew(), society, account, kobo, nz(session), nz(fromAcct)).Scan(&id)
	return id, err
}

// inflowAt: as inflowFrom, confirmed at `at` (SQL).
func inflowAt(society, account, session, fromAcct string, kobo int64, at string) (string, error) {
	var id string
	err := db.QueryRow(bg(), `INSERT INTO vfd_inflows (vfd_reference, society_id, account_number, amount_kobo, session_id, originator_name, originator_account, originator_bank, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, 'ADEBAYO FOLAKE', $6, 'ANY BANK', `+at+`) RETURNING id::text`, vfdRefNew(), society, account, kobo, nz(session), nz(fromAcct)).Scan(&id)
	return id, err
}

type posted struct {
	entry, member, label, valueDate string
	actor                           *string
	bank, savings, charges          int64
}

// postingOf: the contribution posted from an inflow, or "" if none.
func postingOf(t *testing.T, inflow string) (p posted, n int) {
	t.Helper()
	rows, err := db.Query(bg(), `SELECT e.id::text, e.actor_user_id::text, coalesce(e.actor_post_label, ''), e.value_date::text,
		  coalesce((SELECT sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'bank'), 0),
		  coalesce((SELECT -sum(l.amount_kobo) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'member_savings'), 0),
		  coalesce((SELECT l.member_id::text FROM ledger_lines l JOIN accounts a ON a.id = l.account_id WHERE l.entry_id = e.id AND a.role = 'member_savings' LIMIT 1), ''),
		  coalesce((SELECT -sum(amount_kobo) FROM ledger_lines WHERE entry_id = e.id AND charge_role NOT IN ('principal')), 0)
		FROM ledger_entries e WHERE e.vfd_inflow_id = $1 AND e.kind = 'contribution_in'`, inflow)
	fixture(t, err, "the posting")
	defer rows.Close()
	for rows.Next() {
		n++
		fixture(t, rows.Scan(&p.entry, &p.actor, &p.label, &p.valueDate, &p.bank, &p.savings, &p.member, &p.charges), "a posting")
	}
	return p, n
}

func wantPosted(t *testing.T, inflow, member string, kobo int64, what string) posted {
	t.Helper()
	p, n := postingOf(t, inflow)
	switch {
	case n == 0:
		t.Fatalf("NOT POSTED — %s: VFD confirmed it and nothing was credited", what)
	case n > 1:
		t.Fatalf("POSTED TWICE — %s: %d contributions from one inflow", what, n)
	case p.member != member:
		t.Fatalf("WRONG MEMBER — %s: credited to %s, not %s", what, p.member, member)
	case p.bank != kobo:
		t.Fatalf("WRONG AMOUNT — %s: the bank up %d, VFD confirmed %d", what, p.bank, kobo)
	case p.savings+p.charges != kobo:
		t.Fatalf("UNBALANCED — %s: savings %d and charges %d against %d arrived", what, p.savings, p.charges, kobo)
	}
	return p
}

func wantNotPosted(t *testing.T, inflow, what string) {
	t.Helper()
	if p, n := postingOf(t, inflow); n > 0 {
		t.Fatalf("POSTED — %s: credited to %s, and it must not be", what, p.member)
	}
}

func claimState(t *testing.T, claimID string) string {
	t.Helper()
	var s string
	fixture(t, db.QueryRow(bg(), `SELECT state FROM contribution_claim_states WHERE claim_id = $1`, claimID).Scan(&s), "the claim's state")
	return s
}

// ---- the claim ----------------------------------------------------------------

func TestClaim01_AClaimIsTheMembersOwnInTheirSociety(t *testing.T) {
	w := newClaimWorld(t)
	refused(t, second(claim(w.c.s.id, w.bMem, w.g1, sessionNew(), 50000)), "a claim for one member by another person", refusedAs("claim_is_the_members_own"))
	refused(t, second(claim(w.c.s.id, w.otherMem, w.otherMemUser, sessionNew(), 50000)), "a claim in a society the member is not in", refusedAs("claim_is_the_members_own"))
}

func TestClaim02_AClaimNamesASessionAndAPositiveAmount(t *testing.T) {
	w := newClaimWorld(t)
	refused(t, second(claim(w.c.s.id, w.bMem, w.borrower, "", 50000)), "a claim with no session id", refusedAs("claim_session_shape"), want{codes: []string{"23502"}})
	refused(t, second(claim(w.c.s.id, w.bMem, w.borrower, "ref 12; drop", 50000)), "a session id that is not one", refusedAs("claim_session_shape"))
	refused(t, second(claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), 0)), "a claim of nothing", refusedAs("claim_positive"))
	refused(t, second(claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), -100)), "a negative claim", refusedAs("claim_positive"))
}

func TestClaim03_OneClaimPerMemberPerSession(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	_, err := claim(w.c.s.id, w.bMem, w.borrower, s, 50000)
	fixture(t, err, "the first claim")
	refused(t, second(claim(w.c.s.id, w.bMem, w.borrower, s, 50000)), "the same payment claimed twice by one member", want{codes: []string{"23505"}, constraint: "claim_once_per_member"})
}

// ---- the match -------------------------------------------------------------------

func TestClaim04_AClaimAndItsInflowPostTheContributionWhicheverArrivesSecond(t *testing.T) {
	w := newClaimWorld(t)
	before := savingsKobo(t, w.bMem)
	// The claim first; VFD's confirmation later.
	s1 := sessionNew()
	_, err := claim(w.c.s.id, w.bMem, w.borrower, s1, 250000)
	fixture(t, err, "the claim")
	// Confirmed at 23:30 UTC yesterday: 00:30 today in Lagos. A posting dated
	// by UTC, or by the server's clock, lands on the wrong day.
	in1, err := inflowAt(w.c.s.id, w.socAcct, s1, "0123456789", 250000, "date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' - interval '30 minutes'")
	accepted(t, err, "the inflow after the claim")
	p := wantPosted(t, in1, w.bMem, 250000, "an inflow arriving after its claim")
	if p.actor != nil || p.label != "Argon system, on VFD's confirmation" {
		t.Fatalf("ATTRIBUTED — the posting names user %v, label %q: VFD's confirmation posts it, no person", p.actor, p.label)
	}
	var lagos string
	fixture(t, db.QueryRow(bg(), `SELECT (now() AT TIME ZONE 'UTC')::date::text`).Scan(&lagos), "today")
	if p.valueDate != lagos {
		t.Fatalf("MISDATED — posted on %s; VFD confirmed it at 00:30 on %s in Lagos", p.valueDate, lagos)
	}
	// The inflow first; the claim later.
	s2 := sessionNew()
	in2, err := inflowFrom(w.c.s.id, w.socAcct, s2, "0123456789", 120000)
	fixture(t, err, "the inflow")
	wantNotPosted(t, in2, "an inflow nobody has claimed yet")
	c2, err := claim(w.c.s.id, w.bMem, w.borrower, s2, 120000)
	accepted(t, err, "the claim after the inflow")
	wantPosted(t, in2, w.bMem, 120000, "a claim arriving after its inflow")
	if st := claimState(t, c2); st != "posted" {
		t.Fatalf("UNTOLD — the member's claim says %q after it was posted", st)
	}
	if got := savingsKobo(t, w.bMem) - before; got != 370000 {
		t.Fatalf("NOT CREDITED — the member's savings rose %d, not 370000 (uncharged: no contribution series here)", got)
	}
}

func TestClaim05_TheAmountMustBeEqual(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 250000)
	fixture(t, err, "a claim of 2,500")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 25000)
	accepted(t, err, "VFD confirms 250")
	wantNotPosted(t, in, "a claim of ₦2,500 against ₦250 arrived")
	if st := claimState(t, c); st != "amount_differs" {
		t.Fatalf("UNTOLD — the claim says %q; the amounts differ", st)
	}
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "claim_amount_differs", c, "a claim whose amount is not what arrived")
}

func TestClaim06_TheSessionIsTheSocietysOwnPayment(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	_, err := claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), 250000)
	fixture(t, err, "a claim naming another session")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 250000)
	fixture(t, err, "the inflow")
	wantNotPosted(t, in, "an inflow whose session nobody claimed")
	// The same session claimed in ANOTHER society: not this society's payment.
	_, err = claim(w.other.id, w.otherMem, w.otherMemUser, s, 250000)
	fixture(t, err, "a claim elsewhere naming this session")
	wantNotPosted(t, in, "an inflow claimed only by a member of another society")
}

func TestClaim07_TwoMembersClaimingOnePaymentGetNeither(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	a, err := claim(w.c.s.id, w.bMem, w.borrower, s, 250000)
	fixture(t, err, "one member's claim")
	b, err := claim(w.c.s.id, w.g1Mem, w.g1, s, 250000)
	fixture(t, err, "another member's claim on the same payment")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 250000)
	accepted(t, err, "the inflow")
	wantNotPosted(t, in, "one payment claimed by two members")
	got := vfdExceptions(t, w.c.s.id)
	wantVfdException(t, got, "claim_contested", a, "the first of two claims on one payment")
	wantVfdException(t, got, "claim_contested", b, "the second of two claims on one payment")

	// Credited to one member, then claimed by another: no second posting.
	s2 := sessionNew()
	_, err = claim(w.c.s.id, w.bMem, w.borrower, s2, 90000)
	fixture(t, err, "the claim")
	in2, err := inflowFrom(w.c.s.id, w.socAcct, s2, "", 90000)
	fixture(t, err, "the inflow")
	wantPosted(t, in2, w.bMem, 90000, "the first claimant's payment")
	late, err := claim(w.c.s.id, w.g1Mem, w.g1, s2, 90000)
	accepted(t, err, "a later claim on a payment already credited (recorded, never credited)")
	wantPosted(t, in2, w.bMem, 90000, "a payment claimed again by someone else")
	if st := claimState(t, late); st != "contested" {
		t.Fatalf("UNTOLD — the late claim says %q; the payment was credited to another member", st)
	}
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "claim_contested", late, "a claim on a payment credited to someone else")
}

func TestClaim08_FromTheMembersOwnAccountNoClaimIsNeeded(t *testing.T) {
	w := newClaimWorld(t)
	in, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), w.memAcct, 180000)
	accepted(t, err, "money from the member's own VFD account")
	wantPosted(t, in, w.bMem, 180000, "a payment from the member's own account, unclaimed")
	// Their own claim on it as well: still one contribution.
	var s string
	fixture(t, db.QueryRow(bg(), `SELECT session_id FROM vfd_inflows WHERE id = $1`, in).Scan(&s), "its session")
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 180000)
	accepted(t, err, "the member claims it too")
	wantPosted(t, in, w.bMem, 180000, "a payment from their own account that they also claimed")
	if st := claimState(t, c); st != "posted" {
		t.Fatalf("UNTOLD — the member's claim says %q; their payment was credited", st)
	}
	// A member's account in ANOTHER society says nothing about this one.
	elsewhere := vfdNumber()
	fixture(t, memberAccount(w.other.id, w.otherMem, elsewhere, "C-MEM-"+elsewhere), "the other member's account")
	in2, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), elsewhere, 180000)
	accepted(t, err, "money from another society's member")
	wantNotPosted(t, in2, "money from a member of another society, into this one")
}

func TestClaim09_ThePostingIsChargedByTheSeries(t *testing.T) {
	w := newClaimWorld(t)
	for _, s := range transferSeries() {
		_, err := chargeSeries(w.c.s.id, "contribution_in", s.comp, "2020-01-01", s.src, s.bands...)
		fixture(t, err, "the contribution "+s.comp+" series")
	}
	const kobo = int64(2000000) // ₦20,000: charged in every component that has a band
	var want int64
	fixture(t, db.QueryRow(bg(), `SELECT partner_fee_kobo + vat_kobo + statutory_levy_kobo + argon_margin_kobo FROM charge_quote($1, 'contribution_in', $2, current_date)`, w.c.s.id, kobo).Scan(&want), "the series' charge")
	if want == 0 {
		t.Fatalf("FIXTURE: the series charges nothing on %d: the test cannot tell charged from uncharged", kobo)
	}
	in, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), w.memAcct, kobo)
	accepted(t, err, "a charged contribution")
	p := wantPosted(t, in, w.bMem, kobo, "a charged contribution")
	if p.charges != want {
		t.Fatalf("WRONG CHARGE — the posting carries %d; the series says %d", p.charges, want)
	}
}

func TestClaim10_OnlyTheSystemPostsFromAnInflow(t *testing.T) {
	w := newClaimWorld(t)
	in, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), "", 50000)
	fixture(t, err, "an unclaimed inflow")
	// A contribution naming the inflow, written by anyone but the match.
	refused(t, tx(func(x pgx.Tx) error {
		var e string
		if err := x.QueryRow(bg(), `INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post_label, client_reference, vfd_inflow_id)
			VALUES ($1, 'contribution_in', current_date, NULL, 'Argon system, on VFD''s confirmation', 'forged-'||$2, $2) RETURNING id`, w.c.s.id, in).Scan(&e); err != nil {
			return err
		}
		return nil
	}), "a contribution naming an inflow nobody matched", refusedAs("contribution_from_inflow_is_the_matchs"))
	wantNotPosted(t, in, "an inflow after a forged posting was refused")
}

// ---- what a person must act on -------------------------------------------------------

func TestClaim11_UnmatchedClaimsAndUnpostedInflowsAreSeen(t *testing.T) {
	w := newClaimWorld(t)
	// Posted from a claim: the inflow is no longer anyone's to act on.
	s := sessionNew()
	_, err := claim(w.c.s.id, w.bMem, w.borrower, s, 70000)
	fixture(t, err, "the claim")
	done, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 70000)
	fixture(t, err, "its inflow")
	unclaimed, err := inflowFrom(w.c.s.id, w.socAcct, sessionNew(), "", 40000)
	fixture(t, err, "an inflow nobody claimed")
	// A claim with no inflow, a day on (PROVISIONAL: claim_wait_window).
	waiting, err := claim(w.c.s.id, w.g1Mem, w.g1, sessionNew(), 30000)
	fixture(t, err, "a claim VFD has not confirmed")
	got := vfdExceptions(t, w.c.s.id)
	if _, ok := got["inflow_unclaimed|"+done]; ok {
		t.Fatalf("NOISE — an inflow already credited from its claim is still 'nobody has claimed it'")
	}
	wantVfdException(t, got, "inflow_unclaimed", unclaimed, "money nobody has claimed")
	if _, ok := got["claim_unconfirmed|"+waiting]; ok {
		t.Fatalf("EARLY — a claim made a moment ago is already an exception: VFD has not had its day")
	}
	fixture(t, one(`ALTER TABLE contribution_claims DISABLE TRIGGER USER`), "test scaffolding: date the claim back")
	err = one(`UPDATE contribution_claims SET claimed_at = now() - claim_wait_window() - interval '1 minute' WHERE id = $1`, waiting)
	fixture(t, one(`ALTER TABLE contribution_claims ENABLE TRIGGER USER`), "test scaffolding: the triggers back")
	fixture(t, err, "the claim, dated back")
	wantVfdException(t, vfdExceptions(t, w.c.s.id), "claim_unconfirmed", waiting, "a claim VFD has not confirmed in a day")
}

func TestClaim12_AClaimIsNeverEdited(t *testing.T) {
	w := newClaimWorld(t)
	c, err := claim(w.c.s.id, w.bMem, w.borrower, sessionNew(), 50000)
	fixture(t, err, "a claim")
	refused(t, one(`UPDATE contribution_claims SET amount_kobo = 5000000 WHERE id = $1`, c), "a claim's amount changed after it was made", refusedAs("vfd_records_append_only"))
	refused(t, one(`DELETE FROM contribution_claims WHERE id = $1`, c), "a claim deleted", refusedAs("vfd_records_append_only"))
}

// A member's ordinary contributions are untouched: a claim and its inflow,
// matched, are one contribution and nothing is an exception.
func TestClaim_Control_AMatchedClaimIsOneQuietContribution(t *testing.T) {
	w := newClaimWorld(t)
	s := sessionNew()
	c, err := claim(w.c.s.id, w.bMem, w.borrower, s, 330000)
	accepted(t, err, "the claim")
	in, err := inflowFrom(w.c.s.id, w.socAcct, s, "", 330000)
	accepted(t, err, "the inflow")
	wantPosted(t, in, w.bMem, 330000, "a matched claim")
	for k := range vfdExceptions(t, w.c.s.id) {
		t.Fatalf("NOISE — %s is an exception, and the claim was matched and posted (claim %s)", k, c)
	}
}

func savingsKobo(t *testing.T, member string) int64 {
	t.Helper()
	var k int64
	fixture(t, db.QueryRow(bg(), `SELECT coalesce(-sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
		WHERE a.role = 'member_savings' AND l.member_id = $1`, member).Scan(&k), "the member's savings")
	return k
}

func second(_ string, err error) error { return err }
